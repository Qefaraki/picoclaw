package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/sipeed/picoclaw/pkg/auth"
)

type ClaudeProvider struct {
	client      *anthropic.Client
	tokenSource func() (string, error)
}

func NewClaudeProvider(token string) *ClaudeProvider {
	client := anthropic.NewClient(
		option.WithAuthToken(token),
		option.WithBaseURL("https://api.anthropic.com"),
	)
	return &ClaudeProvider{client: &client}
}

func NewClaudeProviderWithTokenSource(token string, tokenSource func() (string, error)) *ClaudeProvider {
	p := NewClaudeProvider(token)
	p.tokenSource = tokenSource
	return p
}

// NewClaudeProviderOAuth creates a provider that authenticates via OAuth Bearer
// token instead of x-api-key. Claude Max/Pro subscriptions use OAuth tokens
// which must be sent as Authorization: Bearer (not x-api-key).
func NewClaudeProviderOAuth(tokenSource func() (string, error)) *ClaudeProvider {
	client := anthropic.NewClient(
		option.WithBaseURL("https://api.anthropic.com"),
		option.WithMiddleware(oauthBearerMiddleware(tokenSource)),
	)
	return &ClaudeProvider{client: &client}
}

// oauthBearerMiddleware returns SDK middleware that replaces the default
// x-api-key auth with Authorization: Bearer for OAuth tokens.
// Mirrors the auth approach used by Claude CLI / OpenCode:
// - Remove x-api-key header
// - Set Authorization: Bearer <token>
// - Set CLI user-agent (required for OAuth endpoint recognition)
// - Add ?beta=true query param (enables OAuth on the API)
func oauthBearerMiddleware(tokenSource func() (string, error)) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		token, err := tokenSource()
		if err != nil {
			return nil, fmt.Errorf("refreshing OAuth token: %w", err)
		}
		// Strip API key auth — OAuth uses Bearer header instead
		req.Header.Del("X-Api-Key")
		req.Header.Del("x-api-key")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", "claude-cli/2.1.2 (external, cli)")
		// Beta flags required for OAuth-authenticated requests
		req.Header.Set("anthropic-beta", "oauth-2025-04-20,interleaved-thinking-2025-05-14")
		q := req.URL.Query()
		q.Set("beta", "true")
		req.URL.RawQuery = q.Encode()
		return next(req)
	}
}

func (p *ClaudeProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	var opts []option.RequestOption
	if p.tokenSource != nil {
		tok, err := p.tokenSource()
		if err != nil {
			return nil, fmt.Errorf("refreshing token: %w", err)
		}
		opts = append(opts, option.WithAuthToken(tok))
	}

	params, err := buildClaudeParams(messages, tools, model, options)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Messages.New(ctx, params, opts...)
	if err != nil {
		return nil, fmt.Errorf("claude API call: %w", err)
	}

	return parseClaudeResponse(resp), nil
}

func (p *ClaudeProvider) GetDefaultModel() string {
	return "claude-sonnet-4-5-20250929"
}

func buildClaudeParams(messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (anthropic.MessageNewParams, error) {
	var system []anthropic.TextBlockParam
	var anthropicMessages []anthropic.MessageParam

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			system = append(system, anthropic.TextBlockParam{Text: msg.Content})
		case "user":
			if msg.ToolCallID != "" {
				anthropicMessages = append(anthropicMessages,
					anthropic.NewUserMessage(anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false)),
				)
			} else {
				anthropicMessages = append(anthropicMessages,
					anthropic.NewUserMessage(anthropic.NewTextBlock(msg.Content)),
				)
			}
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				var blocks []anthropic.ContentBlockParamUnion
				if msg.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
				}
				for _, tc := range msg.ToolCalls {
					name := tc.Name
					if name == "" && tc.Function != nil {
						name = tc.Function.Name
					}
					if name == "" {
						continue
					}
					// Resolve arguments: prefer map, fall back to parsing Function.Arguments string
					args := tc.Arguments
					if len(args) == 0 && tc.Function != nil && tc.Function.Arguments != "" {
						var parsed map[string]interface{}
						if json.Unmarshal([]byte(tc.Function.Arguments), &parsed) == nil {
							args = parsed
						}
					}
					if args == nil {
						args = map[string]interface{}{}
					}
					blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, args, name))
				}
				anthropicMessages = append(anthropicMessages, anthropic.NewAssistantMessage(blocks...))
			} else {
				anthropicMessages = append(anthropicMessages,
					anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content)),
				)
			}
		case "tool":
			anthropicMessages = append(anthropicMessages,
				anthropic.NewUserMessage(anthropic.NewToolResultBlock(msg.ToolCallID, msg.Content, false)),
			)
		}
	}

	maxTokens := int64(4096)
	if mt, ok := options["max_tokens"].(int); ok {
		maxTokens = int64(mt)
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		Messages:  anthropicMessages,
		MaxTokens: maxTokens,
	}

	if len(system) > 0 {
		params.System = system
	}

	if temp, ok := options["temperature"].(float64); ok {
		params.Temperature = anthropic.Float(temp)
	}

	if len(tools) > 0 {
		params.Tools = translateToolsForClaude(tools)
	}

	return params, nil
}

func translateToolsForClaude(tools []ToolDefinition) []anthropic.ToolUnionParam {
	result := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, t := range tools {
		tool := anthropic.ToolParam{
			Name: t.Function.Name,
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: t.Function.Parameters["properties"],
			},
		}
		if desc := t.Function.Description; desc != "" {
			tool.Description = anthropic.String(desc)
		}
		if req, ok := t.Function.Parameters["required"].([]interface{}); ok {
			required := make([]string, 0, len(req))
			for _, r := range req {
				if s, ok := r.(string); ok {
					required = append(required, s)
				}
			}
			tool.InputSchema.Required = required
		}
		result = append(result, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return result
}

func parseClaudeResponse(resp *anthropic.Message) *LLMResponse {
	var content string
	var toolCalls []ToolCall

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			tb := block.AsText()
			content += tb.Text
		case "tool_use":
			tu := block.AsToolUse()
			var args map[string]interface{}
			if err := json.Unmarshal(tu.Input, &args); err != nil {
				args = map[string]interface{}{"raw": string(tu.Input)}
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:        tu.ID,
				Name:      tu.Name,
				Arguments: args,
			})
		}
	}

	finishReason := "stop"
	switch resp.StopReason {
	case anthropic.StopReasonToolUse:
		finishReason = "tool_calls"
	case anthropic.StopReasonMaxTokens:
		finishReason = "length"
	case anthropic.StopReasonEndTurn:
		finishReason = "stop"
	}

	return &LLMResponse{
		Content:      content,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage: &UsageInfo{
			PromptTokens:     int(resp.Usage.InputTokens),
			CompletionTokens: int(resp.Usage.OutputTokens),
			TotalTokens:      int(resp.Usage.InputTokens + resp.Usage.OutputTokens),
		},
	}
}

func createClaudeTokenSource() func() (string, error) {
	return func() (string, error) {
		cred, err := auth.GetCredential("anthropic")
		if err != nil {
			return "", fmt.Errorf("loading auth credentials: %w", err)
		}
		if cred == nil {
			return "", fmt.Errorf("no credentials for anthropic. Run: picoclaw auth login --provider anthropic")
		}

		if cred.AuthMethod == "oauth" && cred.NeedsRefresh() && cred.RefreshToken != "" {
			oauthCfg := auth.AnthropicOAuthConfig()
			refreshed, err := auth.RefreshAccessToken(cred, oauthCfg)
			if err != nil {
				return "", fmt.Errorf("refreshing token: %w", err)
			}
			if err := auth.SetCredential("anthropic", refreshed); err != nil {
				return "", fmt.Errorf("saving refreshed token: %w", err)
			}
			return refreshed.AccessToken, nil
		}

		return cred.AccessToken, nil
	}
}
