package tools

import (
	"context"
	"fmt"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// ManageTelegramTool gives the agent access to Telegram bot API operations
// such as creating forum topics, pinning messages, and querying chat info.
type ManageTelegramTool struct {
	bot      *telego.Bot
	chatID   string
	metadata map[string]string
}

func NewManageTelegramTool(bot *telego.Bot) *ManageTelegramTool {
	return &ManageTelegramTool{bot: bot}
}

func (t *ManageTelegramTool) Name() string { return "manage_telegram" }

func (t *ManageTelegramTool) Description() string {
	return "Manage Telegram forum topics and messages. Actions: create_topic (create a new forum topic), close_topic / reopen_topic (manage topic state), pin_message / unpin_message (pin or unpin a message), get_chat_info (get chat details). Requires being in a Telegram chat context."
}

func (t *ManageTelegramTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform",
				"enum":        []string{"create_topic", "close_topic", "reopen_topic", "pin_message", "unpin_message", "get_chat_info"},
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Topic name (for create_topic)",
			},
			"icon_color": map[string]interface{}{
				"type":        "integer",
				"description": "Topic icon color (for create_topic). Allowed values: 7322096 (blue), 16766590 (yellow), 13338331 (violet), 9367192 (green), 16749490 (rose), 16478047 (red)",
			},
			"message_id": map[string]interface{}{
				"type":        "integer",
				"description": "Message ID (for pin_message / unpin_message)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *ManageTelegramTool) SetContext(channel, chatID string) {
	t.chatID = chatID
}

func (t *ManageTelegramTool) SetMetadata(metadata map[string]string) {
	t.metadata = metadata
}

func (t *ManageTelegramTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.bot == nil {
		return ErrorResult("Telegram bot is not available.")
	}

	action, _ := args["action"].(string)
	if action == "" {
		return ErrorResult("action is required")
	}

	// Parse chat ID from context
	chatID, err := parseTelegramChatID(t.chatID)
	if err != nil {
		return ErrorResult("No valid Telegram chat context. This tool can only be used from a Telegram chat.")
	}

	// Parse thread_id from metadata
	threadID := 0
	if t.metadata != nil {
		if tid := t.metadata["thread_id"]; tid != "" {
			fmt.Sscanf(tid, "%d", &threadID)
		}
	}

	switch action {
	case "create_topic":
		return t.createTopic(ctx, chatID, args)
	case "close_topic":
		return t.closeTopic(ctx, chatID, threadID)
	case "reopen_topic":
		return t.reopenTopic(ctx, chatID, threadID)
	case "pin_message":
		return t.pinMessage(ctx, chatID, args)
	case "unpin_message":
		return t.unpinMessage(ctx, chatID, args)
	case "get_chat_info":
		return t.getChatInfo(ctx, chatID)
	default:
		return ErrorResult(fmt.Sprintf("Unknown action %q. Supported: create_topic, close_topic, reopen_topic, pin_message, unpin_message, get_chat_info", action))
	}
}

func (t *ManageTelegramTool) createTopic(ctx context.Context, chatID int64, args map[string]interface{}) *ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return ErrorResult("name is required for create_topic")
	}

	params := &telego.CreateForumTopicParams{
		ChatID: tu.ID(chatID),
		Name:   name,
	}

	// Icon color is optional
	if iconColor, ok := args["icon_color"].(float64); ok {
		params.IconColor = int(iconColor)
	}

	topic, err := t.bot.CreateForumTopic(ctx, params)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to create topic: %v", err))
	}

	return SilentResult(fmt.Sprintf("Forum topic '%s' created successfully. Thread ID: %d", topic.Name, topic.MessageThreadID))
}

func (t *ManageTelegramTool) closeTopic(ctx context.Context, chatID int64, threadID int) *ToolResult {
	if threadID == 0 {
		return ErrorResult("Must be used from within a forum topic (no thread_id available).")
	}

	err := t.bot.CloseForumTopic(ctx, &telego.CloseForumTopicParams{
		ChatID:          tu.ID(chatID),
		MessageThreadID: threadID,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to close topic: %v", err))
	}

	return SilentResult(fmt.Sprintf("Forum topic (thread %d) closed.", threadID))
}

func (t *ManageTelegramTool) reopenTopic(ctx context.Context, chatID int64, threadID int) *ToolResult {
	if threadID == 0 {
		return ErrorResult("Must be used from within a forum topic (no thread_id available).")
	}

	err := t.bot.ReopenForumTopic(ctx, &telego.ReopenForumTopicParams{
		ChatID:          tu.ID(chatID),
		MessageThreadID: threadID,
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to reopen topic: %v", err))
	}

	return SilentResult(fmt.Sprintf("Forum topic (thread %d) reopened.", threadID))
}

func (t *ManageTelegramTool) pinMessage(ctx context.Context, chatID int64, args map[string]interface{}) *ToolResult {
	msgID, ok := args["message_id"].(float64)
	if !ok || msgID == 0 {
		return ErrorResult("message_id is required for pin_message")
	}

	err := t.bot.PinChatMessage(ctx, &telego.PinChatMessageParams{
		ChatID:    tu.ID(chatID),
		MessageID: int(msgID),
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to pin message: %v", err))
	}

	return SilentResult(fmt.Sprintf("Message %d pinned.", int(msgID)))
}

func (t *ManageTelegramTool) unpinMessage(ctx context.Context, chatID int64, args map[string]interface{}) *ToolResult {
	msgID, ok := args["message_id"].(float64)
	if !ok || msgID == 0 {
		return ErrorResult("message_id is required for unpin_message")
	}

	err := t.bot.UnpinChatMessage(ctx, &telego.UnpinChatMessageParams{
		ChatID:    tu.ID(chatID),
		MessageID: int(msgID),
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to unpin message: %v", err))
	}

	return SilentResult(fmt.Sprintf("Message %d unpinned.", int(msgID)))
}

func (t *ManageTelegramTool) getChatInfo(ctx context.Context, chatID int64) *ToolResult {
	chat, err := t.bot.GetChat(ctx, &telego.GetChatParams{
		ChatID: tu.ID(chatID),
	})
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to get chat info: %v", err))
	}

	info := fmt.Sprintf("Chat ID: %d\nType: %s", chat.ID, chat.Type)
	if chat.Title != "" {
		info += fmt.Sprintf("\nTitle: %s", chat.Title)
	}
	if chat.Username != "" {
		info += fmt.Sprintf("\nUsername: @%s", chat.Username)
	}
	if chat.Description != "" {
		info += fmt.Sprintf("\nDescription: %s", chat.Description)
	}
	info += fmt.Sprintf("\nIs Forum: %t", chat.IsForum)

	return SilentResult(info)
}

func parseTelegramChatID(chatIDStr string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(chatIDStr, "%d", &id)
	return id, err
}
