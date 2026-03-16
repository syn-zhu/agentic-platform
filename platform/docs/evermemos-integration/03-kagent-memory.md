# Kagent Memory System -- Deep Research Report

## 1. Memory Types

Kagent has **two completely separate memory systems**:

### A. Memory CRD (v1alpha1) -- "External Vector Memory Configuration"

A **Kubernetes CRD** (`Memory`) that stores *configuration* for connecting to external vector databases. It does NOT store actual memory content -- it is a pointer to where memory lives. Currently the only supported provider is **Pinecone**.

- **CRD file**: `go/api/v1alpha1/memory_types.go`
- **Provider enum**: Only `Pinecone` exists
- Used for agent-level configuration: `AgentResponse` has `MemoryRefs []string`

### B. CrewAI Agent Memory -- "Runtime Long-Term Memory"

A **database-backed** (SQLite/Postgres via GORM) system for storing actual memory content from CrewAI agent runs. Stores serialized JSON "memory data" scoped by (user_id, thread_id).

- **DB model**: `CrewAIAgentMemory` in `go/pkg/database/models.go:183-191`
- Table name: `crewai_agent_memory`

There is **no short-term memory, entity memory, or user memory** system built into kagent.

---

## 2. Data Model

### Memory CRD Spec (`v1alpha1.MemorySpec`)

```go
type MemorySpec struct {
    Provider        MemoryProvider `json:"provider"`          // Only "Pinecone"
    APIKeySecretRef string         `json:"apiKeySecretRef"`
    APIKeySecretKey string         `json:"apiKeySecretKey"`
    Pinecone        *PineconeConfig `json:"pinecone,omitempty"`
}

type PineconeConfig struct {
    IndexHost      string   `json:"indexHost"`
    TopK           int      `json:"topK,omitempty"`
    Namespace      string   `json:"namespace,omitempty"`
    RecordFields   []string `json:"recordFields,omitempty"`
    ScoreThreshold string   `json:"scoreThreshold,omitempty"`
}
```

### CrewAI Memory DB Schema

```go
type CrewAIAgentMemory struct {
    UserID     string    `gorm:"primaryKey;not null"`
    ThreadID   string    `gorm:"primaryKey;not null"`
    CreatedAt  time.Time `gorm:"autoCreateTime"`
    UpdatedAt  time.Time `gorm:"autoUpdateTime"`
    DeletedAt  gorm.DeletedAt
    MemoryData string    `gorm:"type:text;not null"`   // JSON blob
}
```

---

## 3. REST API

### Memory CRD Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/memories` | List all Memory CRDs |
| `POST` | `/api/memories` | Create a Memory CRD |
| `GET` | `/api/memories/{namespace}/{name}` | Get a specific Memory CRD |
| `PUT` | `/api/memories/{namespace}/{name}` | Update a Memory CRD |
| `DELETE` | `/api/memories/{namespace}/{name}` | Delete a Memory CRD |

### CrewAI Memory Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/crewai/memory` | Store a memory item |
| `GET` | `/api/crewai/memory?thread_id=X&q=Y&limit=N` | Search memory |
| `DELETE` | `/api/crewai/memory?thread_id=X` | Reset memory for a session |

---

## 4. Python Integration

`KagentMemoryStorage` implements three methods for CrewAI's `LongTermMemory`:

1. **`save(task_description, metadata, timestamp, score)`** -- POSTs to `/api/crewai/memory`
2. **`load(task_description, latest_n)`** -- GETs from `/api/crewai/memory?q=...&limit=...`
3. **`reset()`** -- DELETEs `/api/crewai/memory?thread_id=...`

Wired in `_executor.py:146-153` conditionally when `self._crew.memory` is True.

---

## 5. Storage Details

- **Backend**: GORM (SQLite default, PostgreSQL for production)
- **Search**: SQL `LIKE` query + `JSON_EXTRACT` -- **no vector search**
- **No embedding generation anywhere in the codebase**
- The Pinecone Memory CRD is configuration-only
- A `contrib/memory/qdrant.database.yaml` exists but is not integrated

---

## 6. Extension Points for a New Backend (like EverMemOS)

### A. New Memory CRD Provider
Add `EverMemOS` provider alongside `Pinecone` in `memory_types.go`. Limitation: Memory CRD not wired to Agent CRD.

### B. Replace CrewAI Memory Storage Backend
Implement new class replacing `KagentMemoryStorage`. Most practical for CrewAI agents.

### C. Database Client Interface Extension
Add new methods to `database.Client` for EverMemOS-backed operations.

### D. MCP Tool Server (Sidecar Approach)
Deploy EverMemOS as a tool server with `store_memory` and `recall_memory` tools. Least invasive, most Kubernetes-native.

---

## 7. Limitations of Current Memory System

1. **No vector/semantic search** -- SQL LIKE only
2. **Only Pinecone** for vector memory CRD
3. **Memory CRD not wired to Agent CRD** -- v1alpha2 has no `MemoryRef` field
4. **No memory reconciler/controller**
5. **CrewAI-only** -- LangGraph and ADK have no memory abstraction
6. **Flat scoping** -- (user_id, thread_id) only, no cross-session memory
7. **No TTL or cleanup**
8. **No memory types** -- only "long-term memory"
9. **No multi-agent memory sharing**
10. **JSON blob storage** -- opaque, no structured fields
