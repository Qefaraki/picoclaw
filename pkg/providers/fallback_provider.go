package providers

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// FallbackProvider wraps a primary and fallback LLMProvider.
// If the primary fails, it transparently retries with the fallback.
type FallbackProvider struct {
	primary       LLMProvider
	fallback      LLMProvider
	primaryModel  string
	fallbackModel string
}

func NewFallbackProvider(primary LLMProvider, fallback LLMProvider, primaryModel, fallbackModel string) *FallbackProvider {
	return &FallbackProvider{
		primary:       primary,
		fallback:      fallback,
		primaryModel:  primaryModel,
		fallbackModel: fallbackModel,
	}
}

func (p *FallbackProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	resp, err := p.primary.Chat(ctx, messages, tools, model, options)
	if err == nil {
		return resp, nil
	}

	logger.WarnCF("fallback", fmt.Sprintf("Primary provider failed (%s), falling back to %s: %v", model, p.fallbackModel, err), nil)

	fbResp, fbErr := p.fallback.Chat(ctx, messages, tools, p.fallbackModel, options)
	if fbErr != nil {
		return nil, fmt.Errorf("primary failed: %w; fallback also failed: %v", err, fbErr)
	}
	return fbResp, nil
}

func (p *FallbackProvider) GetDefaultModel() string {
	return p.primaryModel
}

// Primary returns the underlying primary provider.
func (p *FallbackProvider) Primary() LLMProvider {
	return p.primary
}

// Fallback returns the underlying fallback provider.
func (p *FallbackProvider) Fallback() LLMProvider {
	return p.fallback
}

// FallbackModel returns the fallback model name.
func (p *FallbackProvider) FallbackModel() string {
	return p.fallbackModel
}
