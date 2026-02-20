# Semantic Memory — Technical Documentation

## Overview

PicoClaw's semantic memory adds vector-based search over past conversations and extracted knowledge. It runs alongside the existing memory systems (MEMORY.md, daily notes, session history) without replacing any of them.

## Does It Work Out of the Box?

**It depends on your API key setup.** Here's exactly what happens at startup:

### Startup Flow (in `NewAgentLoop`)

1. Check `cfg.Tools.Memory.SemanticSearch` — defaults to `true` if the config field is absent
2. Call `resolveEmbeddingFunc(cfg)` which tries, in order:
   - `cfg.Providers.OpenAI.APIKey` → uses `chromem.NewEmbeddingFuncOpenAI()` with the configured embedding model
   - `cfg.Providers.OpenRouter.APIKey` → uses `chromem.NewEmbeddingFuncOpenAICompat()` with `openai/` prefix on the model name
   - Neither available → returns `nil`
3. If embedding function is `nil`: logs "No embedding API key available, semantic memory disabled" and **everything continues normally** — no crash, no `search_memory` tool registered
4. If embedding function exists: creates `VectorStore` at `workspace/memory/vectors/`, creates `KnowledgeExtractor`, registers `search_memory` tool

### What You Need

| Scenario | Memory Enabled? | Required Config |
|----------|----------------|-----------------|
| Have OpenAI API key | Yes | `providers.openai.api_key` set |
| Have OpenRouter API key | Yes | `providers.openrouter.api_key` set |
| Neither key | No (silent) | Nothing needed, graceful degradation |
| Explicitly disabled | No | `tools.memory.semantic_search: false` |

### Your VPS Config

Your VPS uses `/data/picoclaw/config.local.json` which currently has an OpenRouter key. **Semantic memory will activate automatically via the OpenRouter fallback path** on next deploy. No config changes needed.

The OpenRouter path prefixes `openai/` to the model name (so `text-embedding-3-small` becomes `openai/text-embedding-3-small`) because OpenRouter requires the provider prefix for embedding models.

## What About Previous Memories?

**Previous conversations are NOT retroactively indexed.** The vector store starts empty. Only new conversations (after deployment) get indexed.

Here's specifically what happens to each existing data source:

| Data Source | Retroactively Indexed? | Still Loaded? |
|-------------|----------------------|--------------|
| `MEMORY.md` | No | Yes, every turn (system prompt) |
| Daily notes (`YYYYMM/YYYYMMDD.md`) | No | Yes, last 3 days (system prompt) |
| Session JSON files (`workspace/sessions/*.json`) | No | Yes, per-session history |
| Session summaries (in session JSON) | No | Yes, when history is loaded |

### Why Not Backfill?

1. **Embedding API cost at scale**: If there are 1000 session turns, backfilling = 1000 embedding API calls at startup. For a personal assistant with ~50 turns/day, this adds up quickly on first deploy.
2. **Session files contain tool call messages**: Raw session JSON has `role: "tool"` messages with `ToolCallID` fields that aren't meaningful to embed.
3. **The existing systems already cover recent history**: MEMORY.md has explicit long-term knowledge, daily notes cover the last 3 days, and session history has the current conversation. Semantic memory primarily helps with "what did I say 2 weeks ago?" — which requires new data to accumulate.

### If You Want to Backfill (Future Enhancement)

A backfill script would need to:
1. Read all `workspace/sessions/*.json` files
2. Extract user+assistant message pairs (skip tool messages)
3. Call `vectorStore.IndexConversation()` for each pair
4. Optionally run `extractor.ExtractAndConsolidate()` on each pair

This could be a one-time CLI command (`picoclaw memory backfill`) or a startup flag. Not implemented yet.

## Data Flow: What Happens Each Turn

```
User sends message
        |
        v
runAgentLoop() processes message, gets finalContent
        |
        v
Trivial message check: if message < 15 chars or response < 50 chars → skip extraction
        |
        v
Step 6: Save to session JSON (synchronous, blocks)
        |
        v
Step 7: Fire two goroutines (async, non-blocking):
        |
        +---> goroutine 1: vectorStore.IndexConversation()
        |     - Formats "User: {msg}\nAssistant: {response}"
        |     - Truncates to 8000 runes
        |     - Calls OpenAI/OpenRouter embedding API (HTTP request)
        |     - Stores document in chromem-go (persisted to disk as gob files)
        |
        +---> goroutine 2: extractor.ExtractAndConsolidate()
              - Trivial message filter: skips "ok", "thanks", "hi", etc.
              - LLM call #1 (cheap model): Extract facts → JSON array
              - For each fact:
                  - Vector search existing knowledge (top 3, similarity > 0.8)
                  - If similar facts exist:
                      - LLM call #2 (cheap model): Decide ADD/UPDATE/DELETE/NOOP
                      - Execute the operation
                  - If no similar facts:
                      - Add as new knowledge
              - LLM call #3 (cheap model): Extract relations → (subject, predicate, object) triples
              - Store relations in workspace/memory/relations.jsonl
        |
        v
Step 8: Summarization (existing, uses cheap model)
```

### Timing

- **IndexConversation**: ~200-500ms (one embedding API call + disk write)
- **ExtractAndConsolidate**: ~1-5s (one LLM call for extraction + one embedding search per fact + optionally one LLM call per fact for consolidation)
- Both run in background goroutines — the user gets their response immediately

### Cost Per Turn

- **Embedding**: ~500 tokens × $0.02/M = $0.00001 per turn
- **Extraction LLM call**: Uses cheap model (Haiku by default), ~500 input + 200 output tokens
- **Consolidation LLM calls**: 0-N calls depending on extracted facts (0 if no similar existing facts), also uses cheap model
- **Relation extraction**: One additional cheap model call per turn for graph triples
- **Trivial messages**: Zero extraction cost — "ok", "thanks", "hi", etc. are filtered out before any LLM call
- **Practical estimate**: Much cheaper than before since extraction/consolidation/summarization all use the cheap model. For a personal assistant doing 50 turns/day, this is negligible.

## File Layout

```
pkg/memory/
  vectorstore.go   — VectorStore: chromem-go wrapper, two collections, CRUD + search + shared blackboard
  extractor.go     — KnowledgeExtractor: LLM fact extraction + Mem0-style consolidation + relation extraction
  relations.go     — RelationStore: graph-augmented memory (subject-predicate-object triples)

pkg/tools/
  memory_search.go — search_memory tool: thin wrapper over VectorStore.Search()
  think.go         — think tool: internal reasoning without output tokens

pkg/metrics/
  tracker.go       — TokenTracker: per-turn token/cost logging to JSONL

pkg/agent/
  loop.go          — Initialization in NewAgentLoop(), async indexing in runAgentLoop()

pkg/config/
  config.go        — MemoryConfig, AgentDefaults.CheapModel, EmailMonitorConfig, MCPConfig

workspace/memory/vectors/
  (runtime)        — chromem-go persistent storage (gob files, auto-created)

workspace/memory/relations.jsonl
  (runtime)        — graph relation triples (auto-created)

workspace/metrics/tokens.jsonl
  (runtime)        — per-turn token usage and cost tracking (auto-created)
```

## Configuration Reference

```json
{
  "tools": {
    "memory": {
      "semantic_search": true,
      "knowledge_extract": true,
      "embedding_model": "text-embedding-3-small"
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `semantic_search` | bool | `true` | Enable vector store + `search_memory` tool |
| `knowledge_extract` | bool | `true` | Enable LLM fact extraction after each turn |
| `embedding_model` | string | `"text-embedding-3-small"` | OpenAI embedding model name |

Environment variables: `PICOCLAW_MEMORY_SEMANTIC_SEARCH`, `PICOCLAW_MEMORY_KNOWLEDGE_EXTRACT`, `PICOCLAW_MEMORY_EMBEDDING_MODEL`

### Disabling

- Set `semantic_search: false` → no vector store created, no `search_memory` tool, no indexing
- Set `knowledge_extract: false` → conversations still indexed, but no LLM fact extraction (saves LLM calls)
- Remove OpenAI/OpenRouter API key → silently disabled (logged at startup)

## Concurrency & Thread Safety

- **chromem-go collections**: Thread-safe internally (use sync.RWMutex)
- **IndexConversation goroutine**: Writes to `conversations` collection, never reads it
- **ExtractAndConsolidate goroutine**: Reads+writes `knowledge` collection (search then add/delete)
- **search_memory tool**: Reads both collections (called from agent loop, which is single-threaded per message)
- **Multiple goroutines from consecutive turns**: Can overlap. chromem-go handles concurrent AddDocument/Query safely

### Context Handling

- Async goroutines use `context.Background()` — intentional, so they're not canceled when the message handler finishes
- If the server shuts down during an async operation, the goroutine runs until the embedding API call completes or times out
- `decideAction` in the extractor has its own 30-second context timeout

## Shared Blackboard Memory

Specialists can access knowledge beyond their own scope. When `SearchKnowledgeScoped()` is called:

1. **Specialist-scoped search first**: Search for knowledge tagged with the specialist's name
2. **Global backfill**: If fewer results than requested, backfill with unscoped (global) knowledge
3. **Deduplication**: Results are deduplicated by ID to avoid showing the same fact twice

This means a finance specialist can access general knowledge about the user, while still prioritizing its own domain knowledge.

## Graph-Augmented Memory

In addition to vector-based semantic search, PicoClaw extracts structured relations from conversations:

- **Format**: `(Subject, Predicate, Object)` triples (e.g., "Muhammad → studies_at → QMUL")
- **Storage**: `workspace/memory/relations.jsonl` — one JSON object per relation
- **Extraction**: After fact extraction, a second LLM call (cheap model) extracts entity relationships
- **Deduplication**: Identical triples are not stored twice
- **Query**: 1-hop traversal — given an entity, find all related entities

The `search_memory` tool includes 1-hop relation results alongside vector search results, giving the agent a richer understanding of entity connections.

## Known Limitations

1. **No retroactive indexing**: Previous conversations before this feature was deployed are not searchable. Only new turns get indexed.

2. **Embedding model change = broken search**: If you change `embedding_model`, new embeddings will be in a different vector space than old ones. Cosine similarity between them is meaningless. You'd need to delete `workspace/memory/vectors/` and start fresh.

3. **Knowledge extraction quality depends on the LLM**: The extraction prompt asks for structured JSON. Weaker models may return malformed JSON (handled gracefully — logged as warning, turn still indexed in conversations). The consolidation prompt is even harder — it asks the LLM to compare facts and decide actions.

4. **No TTL or garbage collection**: Facts in the knowledge collection live forever. If the user says "I'm moving to London" and later "I moved to Manchester", the consolidation *should* update the fact, but only if the cosine similarity between the two is > 0.8.

5. **8000-rune truncation**: Very long conversations (like code review sessions) get truncated. The tail of the conversation is lost for embedding purposes.

6. **OpenRouter embedding cost**: OpenRouter may charge differently for embedding models than direct OpenAI. Check your OpenRouter dashboard.

## Dependency

- `github.com/philippgille/chromem-go v0.7.0` (MPL-2.0, pure Go, zero CGO)
- Persistent storage format: gob files in `workspace/memory/vectors/`
- No additional runtime dependencies in the Docker image
