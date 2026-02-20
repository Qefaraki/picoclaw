package tools

import (
	"context"
	"fmt"
)

type SendCallback func(channel, chatID, content string, metadata map[string]string) error

type MessageTool struct {
	sendCallback    SendCallback
	defaultChannel  string
	defaultChatID   string
	sentInRound     bool              // Tracks whether a message was sent in the current processing round
	inboundMetadata map[string]string // Metadata from the inbound message (thread_id, etc.)
}

func NewMessageTool() *MessageTool {
	return &MessageTool{}
}

func (t *MessageTool) Name() string {
	return "message"
}

func (t *MessageTool) Description() string {
	return "Send a message to user on a chat channel. Use this when you want to communicate something. For Telegram forum topics, include thread_id to target a specific topic."
}

func (t *MessageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The message content to send",
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target channel (telegram, whatsapp, etc.)",
			},
			"chat_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target chat/user ID",
			},
			"thread_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional: Telegram forum topic thread ID for routing messages to specific topics",
			},
		},
		"required": []string{"content"},
	}
}

func (t *MessageTool) SetContext(channel, chatID string) {
	t.defaultChannel = channel
	t.defaultChatID = chatID
	t.sentInRound = false // Reset send tracking for new processing round
}

// HasSentInRound returns true if the message tool sent a message during the current round.
func (t *MessageTool) HasSentInRound() bool {
	return t.sentInRound
}

func (t *MessageTool) SetSendCallback(callback SendCallback) {
	t.sendCallback = callback
}

// SetMetadata implements MetadataAwareTool — receives inbound message metadata.
func (t *MessageTool) SetMetadata(metadata map[string]string) {
	t.inboundMetadata = metadata
}

func (t *MessageTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	content, ok := args["content"].(string)
	if !ok {
		return &ToolResult{ForLLM: "content is required", IsError: true}
	}

	channel, _ := args["channel"].(string)
	chatID, _ := args["chat_id"].(string)

	if channel == "" {
		channel = t.defaultChannel
	}
	if chatID == "" {
		chatID = t.defaultChatID
	}

	if channel == "" || chatID == "" {
		return &ToolResult{ForLLM: "No target channel/chat specified", IsError: true}
	}

	if t.sendCallback == nil {
		return &ToolResult{ForLLM: "Message sending not configured", IsError: true}
	}

	// Build metadata with thread_id if provided, or inherit from inbound metadata
	var metadata map[string]string
	if threadID, ok := args["thread_id"].(string); ok && threadID != "" {
		metadata = map[string]string{"thread_id": threadID}
	} else if t.inboundMetadata != nil {
		if threadID, ok := t.inboundMetadata["thread_id"]; ok && threadID != "" {
			metadata = map[string]string{"thread_id": threadID}
		}
	}

	if err := t.sendCallback(channel, chatID, content, metadata); err != nil {
		return &ToolResult{
			ForLLM:  fmt.Sprintf("sending message: %v", err),
			IsError: true,
			Err:     err,
		}
	}

	t.sentInRound = true
	// Silent: user already received the message directly
	return &ToolResult{
		ForLLM: fmt.Sprintf("Message sent to %s:%s", channel, chatID),
		Silent: true,
	}
}
