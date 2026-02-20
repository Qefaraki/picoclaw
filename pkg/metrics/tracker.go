package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TokenEvent records usage for a single LLM call.
type TokenEvent struct {
	Timestamp    string   `json:"ts"`
	SessionKey   string   `json:"session"`
	Model        string   `json:"model"`
	InputTokens  int      `json:"in"`
	OutputTokens int      `json:"out"`
	CacheRead    int      `json:"cache_read,omitempty"`
	CacheCreate  int      `json:"cache_create,omitempty"`
	CostUSD      float64  `json:"cost"`
	Specialist   string   `json:"specialist,omitempty"`
	ToolsUsed    []string `json:"tools,omitempty"`
	Iteration    int      `json:"iter"`
}

// Tracker appends token usage events to a JSONL file.
type Tracker struct {
	filePath string
	mu       sync.Mutex
}

// NewTracker creates a tracker that writes to workspace/metrics/tokens.jsonl.
func NewTracker(workspace string) *Tracker {
	dir := filepath.Join(workspace, "metrics")
	os.MkdirAll(dir, 0755)
	return &Tracker{
		filePath: filepath.Join(dir, "tokens.jsonl"),
	}
}

// Record appends a token event to the JSONL file.
func (t *Tracker) Record(event TokenEvent) {
	if event.Timestamp == "" {
		event.Timestamp = time.Now().Format(time.RFC3339)
	}
	event.CostUSD = calculateCost(event.Model, event.InputTokens, event.OutputTokens, event.CacheRead, event.CacheCreate)

	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	f, err := os.OpenFile(t.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	f.Write(data)
	f.Write([]byte("\n"))
}

// Model pricing per million tokens (input, output, cache_read, cache_create).
type modelPricing struct {
	inputPerM       float64
	outputPerM      float64
	cacheReadPerM   float64
	cacheCreatePerM float64
}

var pricing = map[string]modelPricing{
	"claude-sonnet-4-5-20250929":  {3.0, 15.0, 0.3, 3.75},
	"claude-sonnet-4-20250514":    {3.0, 15.0, 0.3, 3.75},
	"claude-haiku-3-5-20241022":   {0.8, 4.0, 0.08, 1.0},
	"claude-opus-4-20250514":      {15.0, 75.0, 1.5, 18.75},
}

func calculateCost(model string, input, output, cacheRead, cacheCreate int) float64 {
	p, ok := pricing[model]
	if !ok {
		// Default to Sonnet pricing
		p = modelPricing{3.0, 15.0, 0.3, 3.75}
	}

	return float64(input)*p.inputPerM/1e6 +
		float64(output)*p.outputPerM/1e6 +
		float64(cacheRead)*p.cacheReadPerM/1e6 +
		float64(cacheCreate)*p.cacheCreatePerM/1e6
}
