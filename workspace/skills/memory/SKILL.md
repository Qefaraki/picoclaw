---
name: memory
description: Semantic memory — search past conversations and knowledge about the user via vector embeddings (chromem-go + OpenAI).
---

# Semantic Memory

You have a `search_memory` tool that searches past conversations and extracted knowledge using vector similarity (embeddings).

## Available Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `query` | string | yes | — | Natural language search query |
| `limit` | int | no | 5 | Max results to return |
| `filter` | string | no | `"all"` | `"all"`, `"conversations"`, or `"knowledge"` |

## When to Use — BE PROACTIVE

You should search memory **eagerly and often**. Do NOT wait for the user to ask. Specifically:

- **Start of every conversation**: Search for recent context about the user to ground your responses
- **User mentions anything from the past**: "remember when...", "like I said", "that thing about...", "as we discussed"
- **User asks about their own info**: preferences, plans, deadlines, people they know, where they live, what they're working on
- **You're unsure about context**: Search first, then respond. It's always better to search and find nothing than to miss relevant context.
- **User references a topic you discussed before**: Even vague references like "the project" or "my application" — search to understand what they mean
- **User seems to expect you to know something**: If they talk as if you should already have context, you probably indexed it before. Search for it.

## Example Calls

```json
{"query": "flat application", "limit": 5}
{"query": "user's preferred programming language", "filter": "knowledge"}
{"query": "discussion about moving to London", "filter": "conversations", "limit": 3}
```

## What Gets Returned

Results are formatted with timestamps, categories, and source labels:

```
## Knowledge
- [2026-02-10] User applied for a flat at Aparto (biographical)
- [2026-02-14] Application was approved, moving in March (biographical)

## Conversations
- [2026-02-10, telegram] User: Can you help me with the Aparto flat application?...
```

## Architecture

Two tiers of memory, both searched simultaneously:

### Tier 1: Conversations (Episodic)
Every conversation turn (user message + assistant response) is embedded and stored. This is the raw history — "when did we talk about X?"

- Indexed automatically after every response
- Metadata: session key, channel, chat ID, timestamp
- Truncated at 8000 characters per turn (longer messages lose their tail)

### Tier 2: Knowledge (Semantic)
After each conversation, an LLM extracts key facts about the user and stores them as individual knowledge entries. This is distilled understanding — "what do I know about the user?"

- Categories: `biographical`, `preference`, `task`, `relationship`, `contextual`
- Consolidated Mem0-style: duplicates are detected (cosine similarity > 0.8) and the LLM decides to ADD, UPDATE, DELETE, or NOOP
- Each fact has an `updated_at` timestamp

### What This Does NOT Replace
- `MEMORY.md` — still loaded every turn, still the place for explicit "remember this" notes
- Daily notes — still loaded (last 3 days), still work as before
- Session history — still the primary short-term context per session
- Summarization — still runs when session history gets long

Semantic memory is an *addition* to all of the above.
