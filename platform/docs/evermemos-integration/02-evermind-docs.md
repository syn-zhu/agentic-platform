# EverMind Documentation Research Report

## 1. Platform Overview

EverMemOS is "The Memory OS for Agentic AI" -- a platform that gives stateless AI agents persistent, evolving memory. It comes in two flavors:

- **EverMemOS Cloud** -- managed infrastructure at `https://api.evermind.ai`, authenticated via Bearer token
- **EverMemOS Open Source** -- self-hosted version with the same core pipeline

The platform transforms raw conversations into structured, retrievable knowledge through a biologically-inspired memory lifecycle (encoding -> consolidation -> retrieval).

---

## 2. Core Concepts

### 2.1 Memory Types (4 types)

| Type | Alias | Description | Search Support |
|------|-------|-------------|----------------|
| **Profile** | "The Who" | Stable identity attributes (name, role, preferences). Only direct lookup by user_id, not semantic search. | Lookup only |
| **Episodic Memory** | "The Story" | Narrative summaries of past sessions/conversations. High-level context. | Full search |
| **EventLog** | "The Facts" | Discrete atomic facts (actions, files, figures). No narrative framing. Assistant scene only. | Full search |
| **Foresight** | "The Future" | Time-bound prospective memories (reminders, deadlines). Has validity windows (start_time/end_time). Assistant scene only. | Time-filtered search |

**Mapping to agent memory paradigms:**
- Profile = **Semantic/Long-term memory** (stable user knowledge)
- Episodic Memory = **Episodic memory** (narrative recall of past interactions)
- EventLog = **Working/Short-term memory** (discrete facts from recent interactions)
- Foresight = **Prospective memory** (future-oriented, time-scoped)

### 2.2 MemCell -- The Core Memory Unit

A MemCell is the atomic unit of memory, structured as M = (E, A, F, T):
- **Episode (E)**: Concise narrative summary of "what happened"
- **Atomic Facts (A)**: Discrete, verifiable statements derived from the episode
- **Foresight (F)**: Forward-looking inferences with validity intervals
- **Metadata (T)**: Timestamps, location, source confidence, emotional characteristics

### 2.3 MemScene -- Thematic Clustering

MemScenes organize MemCells into thematic clusters (e.g., "Health", "Work", "Project Alpha"). They:
- Use semantic vector analysis to categorize new MemCells
- Resolve redundancies and contradictions within a scene
- Elevate significant traits to the global User Profile

### 2.4 Memory Lifecycle (3 phases)

1. **Episodic Trace Formation (Encoding)**: Monitors interactions, segments into meaningful events (MemCells)
2. **Semantic Consolidation (Storage)**: Background analysis links new MemCells to existing knowledge, clusters into MemScenes, updates user profiles
3. **Reconstructive Recollection (Retrieval)**: Active reconstruction -- navigates the memory graph, filters irrelevant info, reconstructs precise context

### 2.5 Scenario Modes

Two modes, **immutable once data is stored**:

| Mode | Participants | Memory Focus | Foresight/EventLog |
|------|-------------|-------------|-------------------|
| **Assistant** | 1 human + AI(s) | Human user only | Supported |
| **Group Chat** | Multiple participants | All participants | Not supported |

---

## 3. Retrieval Methods

| Method | Latency | Mechanism | Best For |
|--------|---------|-----------|----------|
| **Keyword** | <100ms | BM25 lexical search | Real-time, exact term matching |
| **Vector** | 200-500ms | Semantic embedding similarity | Conceptual similarity |
| **Hybrid/RRF** | 200-600ms | BM25 + Vector via Reciprocal Rank Fusion | **Recommended default** |
| **Agentic** | 2-5s | LLM-guided multi-round query expansion | Complex/multi-faceted queries |

---

## 4. API Reference

**Base URL:** `https://api.evermind.ai`
**Auth:** `Authorization: Bearer <api_key>`

### 4.1 Add Memories

```
POST /api/v0/memories
```

Request body:
```json
{
  "message_id": "msg_001",
  "create_time": "2024-01-15T10:30:00+00:00",
  "sender": "user_123",
  "content": "I prefer dark roast coffee",
  "group_id": "conv_abc",
  "group_name": "Morning Chat",
  "sender_name": "Alice",
  "role": "user",
  "refer_list": ["msg_000"],
  "flush": true
}
```

Response: `{"status": "queued", "message": "Message accepted and queued for processing"}`

Python SDK:
```python
from evermemos import EverMemOS
memory = EverMemOS(api_key="<api_key>").v0.memories
response = memory.add(
    message_id="<message_id>",
    create_time="<iso8601_time>",
    sender="<user_id>",
    content="<message_content>"
)
```

### 4.2 Search Memories

```
GET /api/v0/memories/search
```

Request body:
```json
{
  "user_id": "user_123",
  "group_ids": ["conv_abc"],
  "query": "coffee preference",
  "memory_types": ["profile", "episodic_memory", "foresight", "event_log"],
  "retrieve_method": "hybrid",
  "include_metadata": true,
  "start_time": "2024-01-01T00:00:00+00:00",
  "end_time": "2024-12-31T23:59:59+00:00",
  "current_time": "2024-06-15T12:00:00+00:00",
  "radius": 0.6,
  "top_k": 5
}
```

### 4.3 Get Memories (Paginated)

```
GET /api/v0/memories
```

### 4.4 Set Conversation Metadata

```
POST /api/v0/memories/conversation-meta
```

Request body:
```json
{
  "created_at": "2024-01-15T10:00:00+00:00",
  "scene": "assistant",
  "scene_desc": {"description": "...", "type": "..."},
  "description": "Personal assistant for Alice",
  "default_timezone": "UTC",
  "user_details": {},
  "tags": ["personal", "assistant"],
  "llm_custom_setting": {}
}
```

### 4.5 Delete Memories

```
DELETE /api/v0/memories
```

---

## 5. Cookbook Patterns

### 5.1 Personal AI Assistant

Store conversation turns -> Retrieve relevant context -> Generate personalized response -> Store assistant reply. Use UUIDs for message IDs, limit top_k: 5, implement async storage.

### 5.2 Customer Support Bot

Ticket isolation via unique group_id + customer profiles across tickets + cross-ticket search. Escalation generates handoff summaries.

### 5.3 Team Collaboration / Group Chat

Group-level + individual-level memory extraction. Hierarchical group IDs, participant roles, topic transitions, `refer_list` for threads.

### 5.4 Python Integration Patterns

- Synchronous client (requests): 30s timeout
- Async client (aiohttp): Connection pooling, 30s keepalive
- Error handling: RateLimitError (4x backoff), ServerError (2x backoff), max 3 retries
- Fire-and-forget: Background asyncio queue (max 1000 items)
- Timeouts: 30s search, 10s storage, 60s+ agentic retrieval

---

## 6. EverMemOS vs Standard RAG

| Aspect | Standard RAG | EverMemOS |
|--------|-------------|-----------|
| Storage | Document chunks, manual updates | Temporal facts and relationships, auto-updated |
| Query | Vector similarity only | BM25 + Vector + RRF + Agentic |
| State | Append-only | Dynamic profile, contradictions resolved at write-time |
| Multi-user | Profile contamination risk | Subject disentanglement per participant |

---

## 7. Key Integration Considerations

1. REST API with Bearer token auth -- straightforward to wrap as MCP tools
2. Async processing -- memories extracted asynchronously; eventual consistency
3. Scene modes are **immutable after first data** -- must choose upfront
4. Search endpoint accepts body on GET -- unusual pattern
5. Python SDK: `pip install evermemos`
6. `flush` parameter forces immediate extraction
7. Group IDs enable session isolation -- maps to agent session tracking
8. Agentic retrieval needs 60s timeout, use selectively

## 8. Cloud vs Open Source API Versions

- Cloud API: `/api/v0/memories` (v0)
- Open Source API: `/api/v1/memories` (v1)
- Same concepts, same fields, different version prefix
