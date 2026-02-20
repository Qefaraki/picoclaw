package email

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// EmailMonitor periodically checks inboxes for new emails,
// triages them with a cheap LLM, and sends urgent ones immediately.
type EmailMonitor struct {
	accounts   []config.EmailAccount
	provider   providers.LLMProvider
	cheapModel string
	scriptPath string
	workspace  string
	msgBus     *bus.MessageBus
	channel    string // target Telegram channel
	chatID     string // target Telegram chat

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
}

type triageResult struct {
	Action  string `json:"action"`
	Summary string `json:"summary"`
}

type emailEntry struct {
	UID     string `json:"uid"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
}

type digestEntry struct {
	Timestamp string `json:"ts"`
	Account   string `json:"account"`
	From      string `json:"from"`
	Subject   string `json:"subject"`
	Summary   string `json:"summary"`
	UID       string `json:"uid"`
}

func NewEmailMonitor(
	accounts []config.EmailAccount,
	provider providers.LLMProvider,
	cheapModel string,
	workspace string,
	msgBus *bus.MessageBus,
	channel string,
	chatID string,
) *EmailMonitor {
	// Resolve script path
	scriptPath := "/usr/local/lib/picoclaw/scripts/email_dashboard.py"
	candidates := []string{
		scriptPath,
		"scripts/email_dashboard.py",
	}
	for _, p := range candidates {
		if _, err := exec.LookPath(p); err == nil {
			scriptPath = p
			break
		}
	}

	return &EmailMonitor{
		accounts:   accounts,
		provider:   provider,
		cheapModel: cheapModel,
		scriptPath: scriptPath,
		workspace:  workspace,
		msgBus:     msgBus,
		channel:    channel,
		chatID:     chatID,
		stopCh:     make(chan struct{}),
	}
}

// Start begins the email monitoring loop at the given interval.
func (m *EmailMonitor) Start(intervalMins int) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.mu.Unlock()

	if intervalMins < 1 {
		intervalMins = 5
	}

	go func() {
		ticker := time.NewTicker(time.Duration(intervalMins) * time.Minute)
		defer ticker.Stop()

		// Run once immediately on start
		m.checkInboxes()

		for {
			select {
			case <-ticker.C:
				m.checkInboxes()
			case <-m.stopCh:
				return
			}
		}
	}()

	logger.InfoCF("email", "Email monitor started", map[string]interface{}{
		"accounts": len(m.accounts),
		"interval": intervalMins,
	})
}

// Stop stops the email monitoring loop.
func (m *EmailMonitor) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		close(m.stopCh)
		m.running = false
	}
}

func (m *EmailMonitor) checkInboxes() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	for _, account := range m.accounts {
		unread, err := m.fetchUnread(ctx, account)
		if err != nil {
			logger.WarnCF("email", "Failed to fetch unread emails", map[string]interface{}{
				"account": account.Label,
				"error":   err.Error(),
			})
			continue
		}

		if len(unread) == 0 {
			continue
		}

		logger.InfoCF("email", "Found unread emails", map[string]interface{}{
			"account": account.Label,
			"count":   len(unread),
		})

		for _, email := range unread {
			body := m.fetchBody(ctx, account, email.UID)
			triage := m.triageEmail(ctx, email, body)

			switch triage.Action {
			case "urgent":
				m.sendImmediate(email, account.Label, triage.Summary, "🚨 URGENT")
			case "delivery_arrived":
				m.sendImmediate(email, account.Label, triage.Summary, "📦 Delivery arrived")
			default:
				m.saveForDigest(email, account.Label, triage.Summary)
			}

			// Mark as read after processing
			m.markRead(ctx, account, email.UID)
		}
	}
}

func (m *EmailMonitor) fetchUnread(ctx context.Context, account config.EmailAccount) ([]emailEntry, error) {
	out, err := m.runScript(ctx, account.Address, "unread")
	if err != nil {
		return nil, err
	}

	var emails []emailEntry
	if err := json.Unmarshal([]byte(out), &emails); err != nil {
		return nil, fmt.Errorf("parse unread: %w", err)
	}
	return emails, nil
}

func (m *EmailMonitor) fetchBody(ctx context.Context, account config.EmailAccount, uid string) string {
	out, err := m.runScript(ctx, account.Address, "read", uid)
	if err != nil {
		return ""
	}

	var body map[string]interface{}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return out
	}

	// Prefer text over html
	if text, ok := body["text"].(string); ok && text != "" {
		if len(text) > 500 {
			return text[:500]
		}
		return text
	}
	if html, ok := body["html"].(string); ok && html != "" {
		if len(html) > 500 {
			return html[:500]
		}
		return html
	}
	return ""
}

func (m *EmailMonitor) triageEmail(ctx context.Context, email emailEntry, body string) triageResult {
	prompt := fmt.Sprintf(`Classify this email into ONE category:
- "urgent": Time-sensitive, requires immediate action (deadline today, emergency, security alert, payment issue)
- "delivery_arrived": A package/order has been DELIVERED (not shipped, not in transit — ARRIVED/collected)
- "normal": Everything else (newsletters, marketing, receipts, shipping updates, routine messages)

From: %s
Subject: %s
Body: %s

Return ONLY JSON: {"action": "urgent|delivery_arrived|normal", "summary": "1-sentence summary"}`,
		email.From, email.Subject, body)

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	resp, err := m.provider.Chat(ctx, []providers.Message{
		{Role: "user", Content: prompt},
	}, nil, m.cheapModel, map[string]interface{}{
		"max_tokens":  128,
		"temperature": 0.1,
	})
	if err != nil {
		// Default to normal on error
		return triageResult{Action: "normal", Summary: email.Subject}
	}

	content := strings.TrimSpace(resp.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var result triageResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return triageResult{Action: "normal", Summary: email.Subject}
	}
	return result
}

func (m *EmailMonitor) sendImmediate(email emailEntry, accountLabel, summary, prefix string) {
	msg := fmt.Sprintf("%s [%s]\n**From:** %s\n**Subject:** %s\n%s",
		prefix, accountLabel, email.From, email.Subject, summary)

	if m.msgBus != nil && m.channel != "" && m.chatID != "" {
		m.msgBus.PublishOutbound(bus.OutboundMessage{
			Channel: m.channel,
			ChatID:  m.chatID,
			Content: msg,
		})
	}

	logger.InfoCF("email", "Sent immediate email notification", map[string]interface{}{
		"account": accountLabel,
		"subject": email.Subject,
		"action":  prefix,
	})
}

func (m *EmailMonitor) saveForDigest(email emailEntry, accountLabel, summary string) {
	entry := digestEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		Account:   accountLabel,
		From:      email.From,
		Subject:   email.Subject,
		Summary:   summary,
		UID:       email.UID,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	digestPath := filepath.Join(m.workspace, "email_digest.jsonl")
	f, err := os.OpenFile(digestPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	f.Write(data)
	f.Write([]byte("\n"))
}

func (m *EmailMonitor) markRead(ctx context.Context, account config.EmailAccount, uid string) {
	_, _ = m.runScript(ctx, account.Address, "mark-read", uid)
}

func (m *EmailMonitor) runScript(ctx context.Context, emailAddr string, cmdArgs ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	args := []string{m.scriptPath, "--email", emailAddr, "--format", "json"}
	args = append(args, cmdArgs...)

	cmd := exec.CommandContext(ctx, "python3", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
