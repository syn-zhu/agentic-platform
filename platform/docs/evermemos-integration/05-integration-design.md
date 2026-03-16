# EverMemOS + kagent Integration Design Document

## 1. Architecture Overview

```
                          DATA FLOW DIAGRAM

  ┌─────────────────────────────────────────────────────────────────┐
  │                     Kubernetes Cluster                          │
  │                                                                 │
  │  ┌──────────────┐     ┌──────────────────┐    ┌─────────────┐  │
  │  │  Agent Pod    │     │  kagent-controller│    │  EverMemOS  │  │
  │  │  (Python)     │     │  (Go HTTP Server) │    │  (FastAPI)  │  │
  │  │              │     │                  │    │  port 1995  │  │
  │  │  ┌──────────┐│     │  ┌──────────────┐│    │             │  │
  │  │  │ CrewAI   ││     │  │ Memory CRD   ││    │  ┌────────┐│  │
  │  │  │ LangGraph││     │  │ Handler      ││    │  │MongoDB ││  │
  │  │  │ ADK      ││     │  │ /api/memories││    │  │ES      ││  │
  │  │  └─────┬────┘│     │  └──────┬───────┘│    │  │Milvus  ││  │
  │  │        │     │     │         │        │    │  └────────┘│  │
  │  │  ┌─────▼────┐│     │  ┌──────▼───────┐│    │             │  │
  │  │  │EverMemOS ││     │  │EverMemOS     ││    │             │  │
  │  │  │Storage   │├─────┤──│Proxy Handler │├────┤             │  │
  │  │  │(Layer 3) ││     │  │(Layer 2)     ││    │             │  │
  │  │  └──────────┘│     │  └──────────────┘│    │             │  │
  │  └──────────────┘     └──────────────────┘    └─────────────┘  │
  │                                                                 │
  │  ┌──────────────┐     ┌──────────────────┐                     │
  │  │  MCP Tool    │     │  Memory CRD      │                     │
  │  │  Server      │     │  (v1alpha1)       │                     │
  │  │  (Layer 4)   │     │  (Layer 1)        │                     │
  │  │              │     │                  │                     │
  │  │ store_memory │     │ provider:        │                     │
  │  │ search_memory├─────│  EverMemOS       │                     │
  │  │ get_profile  │     │ endpoint: svc:// │                     │
  │  └──────────────┘     └──────────────────┘                     │
  └─────────────────────────────────────────────────────────────────┘
```

---

## 2. Concept Mapping: kagent to EverMemOS

| kagent Concept | EverMemOS Concept | Mapping Strategy |
|---|---|---|
| `user_id` (X-User-ID header) | `sender` / `user_id` | Direct 1:1 mapping |
| `thread_id` (session/context_id) | `group_id` | `{namespace}.{agent-name}.{session-id}` |
| `CrewAIAgentMemory.MemoryData` | Structured memory types | EverMemOS auto-extracts |
| SQL LIKE search | `retrieve_method` | Upgrade to semantic search |
| No cross-session memory | `user_id`-level search | EverMemOS natively supports |

---

## 3. Layer 1: Memory CRD Enhancement

### Changes to `memory_types.go`

```go
// +kubebuilder:validation:Enum=Pinecone;EverMemOS
type MemoryProvider string

const (
    Pinecone  MemoryProvider = "Pinecone"
    EverMemOS MemoryProvider = "EverMemOS"
)

type EverMemOSConfig struct {
    Endpoint       string   `json:"endpoint"`
    SceneMode      string   `json:"sceneMode,omitempty"`      // assistant|group_chat
    RetrieveMethod string   `json:"retrieveMethod,omitempty"` // keyword|vector|hybrid|rrf|agentic
    TopK           int      `json:"topK,omitempty"`
    Radius         *float64 `json:"radius,omitempty"`
}

type MemorySpec struct {
    Provider        MemoryProvider   `json:"provider"`
    APIKeySecretRef string           `json:"apiKeySecretRef"`
    APIKeySecretKey string           `json:"apiKeySecretKey"`
    Pinecone        *PineconeConfig  `json:"pinecone,omitempty"`
    EverMemOS       *EverMemOSConfig `json:"evermemos,omitempty"`  // NEW
}
```

### Memory CRD Example

```yaml
apiVersion: kagent.dev/v1alpha1
kind: Memory
metadata:
  name: agent-memory
  namespace: kagent-system
spec:
  provider: EverMemOS
  apiKeySecretRef: evermemos-api-key
  evermemos:
    endpoint: http://evermemos.evermemos.svc.cluster.local:1995
    sceneMode: assistant
    retrieveMethod: hybrid
    topK: 10
```

---

## 4. Layer 2: Go HTTP Backend

### New `/api/memory/v2` Endpoints

| kagent Endpoint | EverMemOS Endpoint | Description |
|---|---|---|
| `POST /api/memory/v2/store` | `POST /api/v1/memories` | Store a message |
| `GET /api/memory/v2/search` | `GET /api/v1/memories/search` | Search memories |
| `GET /api/memory/v2/fetch` | `GET /api/v1/memories` | Paginated fetch |
| `DELETE /api/memory/v2/delete` | `DELETE /api/v1/memories` | Delete memories |
| `POST /api/memory/v2/meta` | `POST /api/v1/memories/conversation-meta` | Set metadata |
| `GET /api/memory/v2/meta` | `GET /api/v1/memories/conversation-meta` | Get metadata |

### Key Mapping Logic

- `group_id` = `{agent_namespace}.{agent_name}.{thread_id}`
- `sender` = `user_id` from `X-User-ID` header
- `message_id` = auto-generated UUID
- Cross-session search: omit `thread_id` to search across all groups for user

### Async Handling

- Return EverMemOS status transparently (`accumulated` vs `extracted`)
- Support `flush=true` for end-of-session force-extraction

---

## 5. Layer 3: Python Agent Integration

### New `EverMemOSStorage` Class

Replaces `KagentMemoryStorage` for CrewAI's `LongTermMemory`:
- `save()` -> `POST /api/v1/memories` (with auto conversation-meta init)
- `load()` -> `GET /api/v1/memories/search` (hybrid retrieval)
- `reset()` -> `DELETE /api/v1/memories`

Selection logic in `_executor.py`:
```python
if os.environ.get("EVERMEMOS_ENDPOINT"):
    # Use EverMemOS
    self._crew.long_term_memory = LongTermMemory(EverMemOSStorage(...))
else:
    # Fallback to existing kagent DB
    self._crew.long_term_memory = LongTermMemory(KagentMemoryStorage(...))
```

### Framework-Agnostic Client

```python
class EverMemOSClient:
    def store(self, content, role="user", **kwargs) -> dict: ...
    def search(self, query, top_k=10, **kwargs) -> list[dict]: ...
    def get_profile(self, user_id) -> dict: ...
    def delete(self, **filters) -> dict: ...
```

---

## 6. Layer 4: MCP Tool Server

### Tools

| Tool | Description |
|------|-------------|
| `store_memory` | Store a message/observation into long-term memory |
| `search_memory` | Search memories by semantic query |
| `get_profile` | Retrieve user profile extracted from conversations |
| `manage_conversation_meta` | Get/set conversation metadata |

### Deployment

Deploy as MCPServer CR or RemoteMCPServer in kagent. Any agent framework can use memory via MCP tools.

---

## 7. Key Design Decisions

1. **Scene mode**: Default `assistant` for all agent sessions (supports all 4 memory types)
2. **Group ID**: `{namespace}.{agent}.{session}` format (deterministic, reversible)
3. **Backward compatible**: Existing `/api/crewai/memory` untouched; EverMemOS is opt-in via env var
4. **Cross-session memory**: Free via user_id-only search (no group_id filter)

---

## 8. Files to Modify / Create

### Layer 1: Memory CRD
- MODIFY: `go/api/v1alpha1/memory_types.go`
- MODIFY: `go/pkg/client/api/types.go`
- MODIFY: `go/internal/httpserver/handlers/memory.go`

### Layer 2: Go HTTP Backend
- CREATE: `go/internal/httpserver/handlers/evermemos.go`
- MODIFY: `go/internal/httpserver/server.go`

### Layer 3: Python Agent Integration
- CREATE: `python/packages/kagent-crewai/src/kagent/crewai/_evermemos_memory.py`
- MODIFY: `python/packages/kagent-crewai/src/kagent/crewai/_executor.py`
- CREATE: `python/packages/kagent-core/src/kagent/core/memory/evermemos.py`

### Layer 4: MCP Tool Server
- CREATE: New repo or `tools/evermemos-mcp/`

---

## 9. Phased Rollout Plan

| Phase | Layer | Effort | Description |
|-------|-------|--------|-------------|
| **1** | MCP Tool Server | ~2-3 days | Zero kagent changes, any framework |
| **2** | Memory CRD Enhancement | ~1-2 days | K8s-native config model |
| **3** | Python Storage Integration | ~2-3 days | Native CrewAI support |
| **4** | Go HTTP Backend Proxy | ~2-3 days | UI integration path |

---

## 10. Open Questions

1. MCP tool server: separate repo or in agentic-platform?
2. Wire Memory CRD to Agent CRD (v1alpha2 schema change)?
3. Multi-tenant: shared instance or per-tenant?
4. LLM budget/quota settings in Memory CRD?
5. Migration script for existing CrewAI memory data?
