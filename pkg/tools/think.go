package tools

import "context"

// ThinkTool allows the agent to reason through complex problems step by step
// without taking any action. The thought is returned silently to the LLM.
type ThinkTool struct{}

func NewThinkTool() *ThinkTool {
	return &ThinkTool{}
}

func (t *ThinkTool) Name() string {
	return "think"
}

func (t *ThinkTool) Description() string {
	return "Use this tool to think through a problem step-by-step before acting. Your thought is private and not shown to the user. Use it when you need to reason about complex decisions, plan multi-step actions, or analyze information before responding."
}

func (t *ThinkTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"thought": map[string]interface{}{
				"type":        "string",
				"description": "Your step-by-step reasoning or analysis",
			},
		},
		"required": []string{"thought"},
	}
}

func (t *ThinkTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	thought, _ := args["thought"].(string)
	if thought == "" {
		return ErrorResult("thought is required")
	}
	return SilentResult("Thought recorded.")
}
