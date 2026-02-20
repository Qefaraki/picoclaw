package tools

import (
	"context"
	"errors"
	"testing"
)

func TestMessageTool_Execute_Success(t *testing.T) {
	tool := NewMessageTool()
	tool.SetContext("test-channel", "test-chat-id")

	var sentChannel, sentChatID, sentContent string
	tool.SetSendCallback(func(channel, chatID, content string, metadata map[string]string) error {
		sentChannel = channel
		sentChatID = chatID
		sentContent = content
		return nil
	})

	ctx := context.Background()
	args := map[string]interface{}{
		"content": "Hello, world!",
	}

	result := tool.Execute(ctx, args)

	// Verify message was sent with correct parameters
	if sentChannel != "test-channel" {
		t.Errorf("Expected channel 'test-channel', got '%s'", sentChannel)
	}
	if sentChatID != "test-chat-id" {
		t.Errorf("Expected chatID 'test-chat-id', got '%s'", sentChatID)
	}
	if sentContent != "Hello, world!" {
		t.Errorf("Expected content 'Hello, world!', got '%s'", sentContent)
	}

	// Verify ToolResult meets US-011 criteria:
	// - Send success returns SilentResult (Silent=true)
	if !result.Silent {
		t.Error("Expected Silent=true for successful send")
	}

	// - ForLLM contains send status description
	if result.ForLLM != "Message sent to test-channel:test-chat-id" {
		t.Errorf("Expected ForLLM 'Message sent to test-channel:test-chat-id', got '%s'", result.ForLLM)
	}

	// - ForUser is empty (user already received message directly)
	if result.ForUser != "" {
		t.Errorf("Expected ForUser to be empty, got '%s'", result.ForUser)
	}

	// - IsError should be false
	if result.IsError {
		t.Error("Expected IsError=false for successful send")
	}
}

func TestMessageTool_Execute_WithCustomChannel(t *testing.T) {
	tool := NewMessageTool()
	tool.SetContext("default-channel", "default-chat-id")

	var sentChannel, sentChatID string
	tool.SetSendCallback(func(channel, chatID, content string, metadata map[string]string) error {
		sentChannel = channel
		sentChatID = chatID
		return nil
	})

	ctx := context.Background()
	args := map[string]interface{}{
		"content": "Test message",
		"channel": "custom-channel",
		"chat_id": "custom-chat-id",
	}

	result := tool.Execute(ctx, args)

	// Verify custom channel/chatID were used instead of defaults
	if sentChannel != "custom-channel" {
		t.Errorf("Expected channel 'custom-channel', got '%s'", sentChannel)
	}
	if sentChatID != "custom-chat-id" {
		t.Errorf("Expected chatID 'custom-chat-id', got '%s'", sentChatID)
	}

	if !result.Silent {
		t.Error("Expected Silent=true")
	}
	if result.ForLLM != "Message sent to custom-channel:custom-chat-id" {
		t.Errorf("Expected ForLLM 'Message sent to custom-channel:custom-chat-id', got '%s'", result.ForLLM)
	}
}

func TestMessageTool_Execute_WithThreadID(t *testing.T) {
	tool := NewMessageTool()
	tool.SetContext("telegram", "-1003732393703")

	var sentMetadata map[string]string
	tool.SetSendCallback(func(channel, chatID, content string, metadata map[string]string) error {
		sentMetadata = metadata
		return nil
	})

	ctx := context.Background()
	args := map[string]interface{}{
		"content":   "Test message",
		"thread_id": "35",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected no error, got: %s", result.ForLLM)
	}
	if sentMetadata == nil {
		t.Fatal("Expected metadata to be set")
	}
	if sentMetadata["thread_id"] != "35" {
		t.Errorf("Expected thread_id '35', got '%s'", sentMetadata["thread_id"])
	}
}

func TestMessageTool_Execute_InheritThreadIDFromMetadata(t *testing.T) {
	tool := NewMessageTool()
	tool.SetContext("telegram", "-1003732393703")
	// Simulate inbound metadata from a cron job with thread_id
	tool.SetMetadata(map[string]string{"thread_id": "35"})

	var sentMetadata map[string]string
	tool.SetSendCallback(func(channel, chatID, content string, metadata map[string]string) error {
		sentMetadata = metadata
		return nil
	})

	ctx := context.Background()
	// No explicit thread_id in args — should inherit from inbound metadata
	args := map[string]interface{}{
		"content": "Cron message",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected no error, got: %s", result.ForLLM)
	}
	if sentMetadata == nil {
		t.Fatal("Expected metadata to be set from inbound metadata")
	}
	if sentMetadata["thread_id"] != "35" {
		t.Errorf("Expected inherited thread_id '35', got '%s'", sentMetadata["thread_id"])
	}
}

func TestMessageTool_Execute_ExplicitThreadIDOverridesInbound(t *testing.T) {
	tool := NewMessageTool()
	tool.SetContext("telegram", "-1003732393703")
	tool.SetMetadata(map[string]string{"thread_id": "35"})

	var sentMetadata map[string]string
	tool.SetSendCallback(func(channel, chatID, content string, metadata map[string]string) error {
		sentMetadata = metadata
		return nil
	})

	ctx := context.Background()
	// Explicit thread_id should override inbound metadata
	args := map[string]interface{}{
		"content":   "Test message",
		"thread_id": "99",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected no error, got: %s", result.ForLLM)
	}
	if sentMetadata == nil {
		t.Fatal("Expected metadata to be set")
	}
	if sentMetadata["thread_id"] != "99" {
		t.Errorf("Expected explicit thread_id '99', got '%s'", sentMetadata["thread_id"])
	}
}

func TestMessageTool_Execute_SendFailure(t *testing.T) {
	tool := NewMessageTool()
	tool.SetContext("test-channel", "test-chat-id")

	sendErr := errors.New("network error")
	tool.SetSendCallback(func(channel, chatID, content string, metadata map[string]string) error {
		return sendErr
	})

	ctx := context.Background()
	args := map[string]interface{}{
		"content": "Test message",
	}

	result := tool.Execute(ctx, args)

	// Verify ToolResult for send failure:
	// - Send failure returns ErrorResult (IsError=true)
	if !result.IsError {
		t.Error("Expected IsError=true for failed send")
	}

	// - ForLLM contains error description
	expectedErrMsg := "sending message: network error"
	if result.ForLLM != expectedErrMsg {
		t.Errorf("Expected ForLLM '%s', got '%s'", expectedErrMsg, result.ForLLM)
	}

	// - Err field should contain original error
	if result.Err == nil {
		t.Error("Expected Err to be set")
	}
	if result.Err != sendErr {
		t.Errorf("Expected Err to be sendErr, got %v", result.Err)
	}
}

func TestMessageTool_Execute_MissingContent(t *testing.T) {
	tool := NewMessageTool()
	tool.SetContext("test-channel", "test-chat-id")

	ctx := context.Background()
	args := map[string]interface{}{} // content missing

	result := tool.Execute(ctx, args)

	// Verify error result for missing content
	if !result.IsError {
		t.Error("Expected IsError=true for missing content")
	}
	if result.ForLLM != "content is required" {
		t.Errorf("Expected ForLLM 'content is required', got '%s'", result.ForLLM)
	}
}

func TestMessageTool_Execute_NoTargetChannel(t *testing.T) {
	tool := NewMessageTool()
	// No SetContext called, so defaultChannel and defaultChatID are empty

	tool.SetSendCallback(func(channel, chatID, content string, metadata map[string]string) error {
		return nil
	})

	ctx := context.Background()
	args := map[string]interface{}{
		"content": "Test message",
	}

	result := tool.Execute(ctx, args)

	// Verify error when no target channel specified
	if !result.IsError {
		t.Error("Expected IsError=true when no target channel")
	}
	if result.ForLLM != "No target channel/chat specified" {
		t.Errorf("Expected ForLLM 'No target channel/chat specified', got '%s'", result.ForLLM)
	}
}

func TestMessageTool_Execute_NotConfigured(t *testing.T) {
	tool := NewMessageTool()
	tool.SetContext("test-channel", "test-chat-id")
	// No SetSendCallback called

	ctx := context.Background()
	args := map[string]interface{}{
		"content": "Test message",
	}

	result := tool.Execute(ctx, args)

	// Verify error when send callback not configured
	if !result.IsError {
		t.Error("Expected IsError=true when send callback not configured")
	}
	if result.ForLLM != "Message sending not configured" {
		t.Errorf("Expected ForLLM 'Message sending not configured', got '%s'", result.ForLLM)
	}
}

func TestMessageTool_Name(t *testing.T) {
	tool := NewMessageTool()
	if tool.Name() != "message" {
		t.Errorf("Expected name 'message', got '%s'", tool.Name())
	}
}

func TestMessageTool_Description(t *testing.T) {
	tool := NewMessageTool()
	desc := tool.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
}

func TestMessageTool_Parameters(t *testing.T) {
	tool := NewMessageTool()
	params := tool.Parameters()

	// Verify parameters structure
	typ, ok := params["type"].(string)
	if !ok || typ != "object" {
		t.Error("Expected type 'object'")
	}

	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected properties to be a map")
	}

	// Check required properties
	required, ok := params["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "content" {
		t.Error("Expected 'content' to be required")
	}

	// Check content property
	contentProp, ok := props["content"].(map[string]interface{})
	if !ok {
		t.Error("Expected 'content' property")
	}
	if contentProp["type"] != "string" {
		t.Error("Expected content type to be 'string'")
	}

	// Check channel property (optional)
	channelProp, ok := props["channel"].(map[string]interface{})
	if !ok {
		t.Error("Expected 'channel' property")
	}
	if channelProp["type"] != "string" {
		t.Error("Expected channel type to be 'string'")
	}

	// Check chat_id property (optional)
	chatIDProp, ok := props["chat_id"].(map[string]interface{})
	if !ok {
		t.Error("Expected 'chat_id' property")
	}
	if chatIDProp["type"] != "string" {
		t.Error("Expected chat_id type to be 'string'")
	}

	// Check thread_id property (optional)
	threadIDProp, ok := props["thread_id"].(map[string]interface{})
	if !ok {
		t.Error("Expected 'thread_id' property")
	}
	if threadIDProp["type"] != "string" {
		t.Error("Expected thread_id type to be 'string'")
	}
}
