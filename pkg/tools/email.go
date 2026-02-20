package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const emailBodyMaxChars = 8000

// EmailTool provides access to an M365 mailbox via the email_dashboard.py script.
type EmailTool struct {
	emailAddress string
	scriptPath   string
}

type EmailToolOptions struct {
	EmailAddress string
	ScriptPath   string // path to email_dashboard.py
}

func NewEmailTool(opts EmailToolOptions) *EmailTool {
	scriptPath := opts.ScriptPath
	if scriptPath == "" {
		candidates := []string{
			"/usr/local/lib/picoclaw/scripts/email_dashboard.py",
			"scripts/email_dashboard.py",
		}
		scriptPath = candidates[0]
		for _, p := range candidates {
			if _, err := exec.LookPath(p); err == nil {
				scriptPath = p
				break
			}
		}
	}
	return &EmailTool{
		emailAddress: opts.EmailAddress,
		scriptPath:   scriptPath,
	}
}

func (t *EmailTool) Name() string {
	return "email"
}

func (t *EmailTool) Description() string {
	return "Access your M365 email inbox. Can list recent/unread emails, search by sender or subject, read full email bodies, mark as read, archive, list folders, send new emails, and reply to existing emails. Uses OAuth2 with auto-refresh."
}

func (t *EmailTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"recent", "unread", "search", "read", "mark_read", "archive", "folders", "send", "reply"},
			},
			"uid": map[string]interface{}{
				"type":        "string",
				"description": "Email UID (required for read, mark_read, archive, reply)",
			},
			"sender": map[string]interface{}{
				"type":        "string",
				"description": "Filter by sender email/name (for search)",
			},
			"subject": map[string]interface{}{
				"type":        "string",
				"description": "Email subject (for search filter or send)",
			},
			"days": map[string]interface{}{
				"type":        "integer",
				"description": "Number of days to look back (for recent, default: 7)",
			},
			"to": map[string]interface{}{
				"type":        "string",
				"description": "Recipient email address (required for send)",
			},
			"cc": map[string]interface{}{
				"type":        "string",
				"description": "CC email address(es) (for send)",
			},
			"bcc": map[string]interface{}{
				"type":        "string",
				"description": "BCC email address(es) (for send)",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "Email body text (required for send and reply)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *EmailTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	action, ok := args["action"].(string)
	if !ok {
		return ErrorResult("action is required")
	}

	switch action {
	case "recent":
		return t.recent(ctx, args)
	case "unread":
		return t.unread(ctx)
	case "search":
		return t.search(ctx, args)
	case "read":
		return t.read(ctx, args)
	case "mark_read":
		return t.markRead(ctx, args)
	case "archive":
		return t.archive(ctx, args)
	case "folders":
		return t.folders(ctx)
	case "send":
		return t.send(ctx, args)
	case "reply":
		return t.reply(ctx, args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s (use: recent, unread, search, read, mark_read, archive, folders, send, reply)", action))
	}
}

// -- Actions --

func (t *EmailTool) recent(ctx context.Context, args map[string]interface{}) *ToolResult {
	cmdArgs := []string{"recent"}
	if d, ok := args["days"].(float64); ok && int(d) > 0 {
		cmdArgs = append(cmdArgs, "--days", fmt.Sprintf("%d", int(d)))
	}
	out, err := t.run(ctx, cmdArgs...)
	if err != nil {
		return t.handleError(err)
	}
	return SilentResult(t.formatList(out, "Recent emails"))
}

func (t *EmailTool) unread(ctx context.Context) *ToolResult {
	out, err := t.run(ctx, "unread")
	if err != nil {
		return t.handleError(err)
	}
	return SilentResult(t.formatList(out, "Unread emails"))
}

func (t *EmailTool) search(ctx context.Context, args map[string]interface{}) *ToolResult {
	cmdArgs := []string{"search"}
	if sender, ok := args["sender"].(string); ok && sender != "" {
		cmdArgs = append(cmdArgs, "--sender", sender)
	}
	if subject, ok := args["subject"].(string); ok && subject != "" {
		cmdArgs = append(cmdArgs, "--subject", subject)
	}
	out, err := t.run(ctx, cmdArgs...)
	if err != nil {
		return t.handleError(err)
	}
	return SilentResult(t.formatList(out, "Search results"))
}

func (t *EmailTool) read(ctx context.Context, args map[string]interface{}) *ToolResult {
	uid, ok := args["uid"].(string)
	if !ok || uid == "" {
		return ErrorResult("uid is required for read action")
	}
	out, err := t.run(ctx, "read", uid)
	if err != nil {
		return t.handleError(err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		return ErrorResult(fmt.Sprintf("failed to parse email body: %v", err))
	}

	if errMsg, ok := body["error"].(string); ok {
		return ErrorResult(errMsg)
	}

	// Truncate long bodies
	for _, key := range []string{"text", "html"} {
		if text, ok := body[key].(string); ok && len(text) > emailBodyMaxChars {
			body[key] = text[:emailBodyMaxChars] + "\n... [truncated]"
		}
	}

	// Prefer text over html; drop html if text exists
	if text, ok := body["text"].(string); ok && text != "" {
		delete(body, "html")
	}

	formatted, _ := json.MarshalIndent(body, "", "  ")
	return SilentResult(string(formatted))
}

func (t *EmailTool) markRead(ctx context.Context, args map[string]interface{}) *ToolResult {
	uid, ok := args["uid"].(string)
	if !ok || uid == "" {
		return ErrorResult("uid is required for mark_read action")
	}
	out, err := t.run(ctx, "mark-read", uid)
	if err != nil {
		return t.handleError(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return SilentResult(out)
	}
	if marked, ok := result["marked_read"].(bool); ok && marked {
		return SilentResult(fmt.Sprintf("Email UID %s marked as read.", uid))
	}
	return ErrorResult(fmt.Sprintf("Failed to mark UID %s as read.", uid))
}

func (t *EmailTool) archive(ctx context.Context, args map[string]interface{}) *ToolResult {
	uid, ok := args["uid"].(string)
	if !ok || uid == "" {
		return ErrorResult("uid is required for archive action")
	}
	out, err := t.run(ctx, "archive", uid)
	if err != nil {
		return t.handleError(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return SilentResult(out)
	}
	if archived, ok := result["archived"].(bool); ok && archived {
		return SilentResult(fmt.Sprintf("Email UID %s archived.", uid))
	}
	return ErrorResult(fmt.Sprintf("Failed to archive UID %s.", uid))
}

func (t *EmailTool) folders(ctx context.Context) *ToolResult {
	out, err := t.run(ctx, "folders")
	if err != nil {
		return t.handleError(err)
	}
	return SilentResult("Mailbox folders:\n" + out)
}

func (t *EmailTool) send(ctx context.Context, args map[string]interface{}) *ToolResult {
	to, _ := args["to"].(string)
	if to == "" {
		return ErrorResult("to is required for send action")
	}
	subject, _ := args["subject"].(string)
	if subject == "" {
		return ErrorResult("subject is required for send action")
	}
	body, _ := args["body"].(string)
	if body == "" {
		return ErrorResult("body is required for send action")
	}

	cmdArgs := []string{"send", "--to", to, "--subject", subject, "--body", body}
	if cc, ok := args["cc"].(string); ok && cc != "" {
		cmdArgs = append(cmdArgs, "--cc", cc)
	}
	if bcc, ok := args["bcc"].(string); ok && bcc != "" {
		cmdArgs = append(cmdArgs, "--bcc", bcc)
	}

	out, err := t.run(ctx, cmdArgs...)
	if err != nil {
		return t.handleError(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return SilentResult(out)
	}
	if sent, ok := result["sent"].(bool); ok && sent {
		return SilentResult(fmt.Sprintf("Email sent to %s: %s", to, subject))
	}
	return ErrorResult(fmt.Sprintf("Failed to send email: %s", out))
}

func (t *EmailTool) reply(ctx context.Context, args map[string]interface{}) *ToolResult {
	uid, _ := args["uid"].(string)
	if uid == "" {
		return ErrorResult("uid is required for reply action")
	}
	body, _ := args["body"].(string)
	if body == "" {
		return ErrorResult("body is required for reply action")
	}

	out, err := t.run(ctx, "reply", uid, "--body", body)
	if err != nil {
		return t.handleError(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return SilentResult(out)
	}
	if sent, ok := result["sent"].(bool); ok && sent {
		replyTo, _ := result["to"].(string)
		return SilentResult(fmt.Sprintf("Reply sent to %s (re: UID %s)", replyTo, uid))
	}
	if errMsg, ok := result["error"].(string); ok {
		return ErrorResult(errMsg)
	}
	return ErrorResult(fmt.Sprintf("Failed to reply to UID %s: %s", uid, out))
}

// -- Helpers --

func (t *EmailTool) run(ctx context.Context, cmdArgs ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	args := []string{t.scriptPath, "--email", t.emailAddress, "--format", "json"}
	args = append(args, cmdArgs...)

	cmd := exec.CommandContext(ctx, "python3", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (t *EmailTool) handleError(err error) *ToolResult {
	msg := err.Error()
	if strings.Contains(msg, "Token expired") || strings.Contains(msg, "refresh failed") ||
		strings.Contains(msg, "AUTHENTICATE") || strings.Contains(msg, "AUTHENTICATIONFAILED") {
		return ErrorResult("Email authentication expired. The OAuth2 refresh token needs to be renewed. " +
			"Run the device code flow on the VPS: python3 /usr/local/lib/picoclaw/scripts/email_dashboard.py --email <address> recent")
	}
	return ErrorResult(fmt.Sprintf("email tool error: %v", err))
}

func (t *EmailTool) formatList(jsonOutput, label string) string {
	var emails []map[string]interface{}
	if err := json.Unmarshal([]byte(jsonOutput), &emails); err != nil {
		return jsonOutput
	}

	if len(emails) == 0 {
		return label + ": none found."
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("%s (%d):", label, len(emails)))
	for _, e := range emails {
		uid, _ := e["uid"].(string)
		from, _ := e["from"].(string)
		subject, _ := e["subject"].(string)
		date, _ := e["date"].(string)
		lines = append(lines, fmt.Sprintf("  [UID %s] %s | %s | %s", uid, date, from, subject))
	}
	return strings.Join(lines, "\n")
}
