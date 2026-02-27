# Example 06: Personal Assistant with Long-Term Memory

A BYO (Bring Your Own) agent that remembers users across sessions using EverMemOS for memory extraction, storage, and retrieval. Adapted from the [EverMemOS personal assistant cookbook](https://docs.evermind.ai/cookbook/personal-assistant).

## What It Demonstrates

- **BYO agent with ADK callbacks** -- memory integration via `before_model_callback` and `after_model_callback`, transparent to the LLM
- **Programmatic memory integration** -- application code controls when and how memories are stored and retrieved (not LLM-controlled)
- **Conversation metadata setup** -- configuring the "assistant" scene with user/assistant participants
- **Message-by-message ingestion** -- storing each message for automatic memory extraction
- **Hybrid retrieval** -- combining profile fetch (stable facts) with episodic/foresight search (relevant interactions)
- **Memory-augmented prompts** -- injecting retrieved context into the LLM system prompt
- **EverMemOS waypoint integration** -- all memory API calls are transparently traced via AgentGateway waypoint in Langfuse
- **Tenant network policy for EverMemOS** -- demonstrates the egress rule needed for agents to reach the memory service

## Architecture

![Architecture](architecture.drawio.svg)

**Memory flow per conversation turn (transparent via ADK callbacks):**

1. User sends a message via A2A
2. `before_model_callback` fires:
   - Stores the user message in EverMemOS (`POST /api/v1/memories`)
   - Retrieves relevant memories: profile fetch + episodic/foresight hybrid search
   - Injects memory context into the system instruction
3. ADK calls the LLM with the enriched prompt
4. `after_model_callback` fires:
   - Stores the assistant response in EverMemOS
5. EverMemOS asynchronously extracts and indexes memories in the background

## Agent Source

The BYO agent lives in `agent/` and follows the same pattern as Example 02 (Dynamic MCP):

```
agent/
  Dockerfile              # Based on kagent-adk base image
  pyproject.toml           # Dependencies: google-adk, requests
  .python-version
  personal_assistant/
    __init__.py
    agent.py               # ADK Agent with before/after model callbacks
    memory.py              # EverMemOS v1 API client
    agent-card.json         # A2A skill advertisement
```

Key files:
- **`agent.py`** -- Defines the ADK `Agent` with `before_model_callback` (retrieve + inject memories) and `after_model_callback` (store response)
- **`memory.py`** -- EverMemOS client with `store_message`, `search_memories`, `fetch_profile`, and `retrieve_context`

## Key Differences from Cloud Cookbook

| Aspect | Cloud (cookbook) | Platform (this example) |
|--------|----------------|------------------------|
| API version | `/api/v0/memories` | `/api/v1/memories` |
| Base URL | `https://api.evermind.ai` | `http://evermemos.evermemos.svc.cluster.local:1995` |
| Auth | Bearer token | None (in-cluster, mesh-secured) |
| Tenant mode | Single-tenant | Multi-tenant (`TENANT_NON_TENANT_MODE=false`) |
| `user_details` format | List of `{user_id, user_name, role}` | Dict keyed by user_id: `{full_name, role}` |
| Tracing | None | Automatic via AgentGateway waypoint (Langfuse) |
| Timeouts | Client-side | 60s enforced at waypoint (AgentgatewayPolicy) |
| Integration | Hardcoded in chat loop | ADK callbacks (transparent to agent logic) |

## Prerequisites

1. Platform deployed (`platform/manifests/`)
2. EverMemOS deployed and healthy:
   ```bash
   kubectl get pods -n evermemos
   ```
3. EverMemOS gateway applied:
   ```bash
   kubectl apply -f ../../platform/manifests/evermemos-gateway.yaml
   ```
4. Agent container image built and pushed (see [Build](#build) below)

## Build

```bash
cd agent/
docker build -t <your-registry>/personal-assistant:v0.1.0 .
docker push <your-registry>/personal-assistant:v0.1.0
```

Update the `image` field in `manifests.yaml` to point to your registry.

## Deploy

```bash
kubectl apply -f manifests.yaml
```

Wait for the agent pod to be ready:

```bash
kubectl get agents -n example-memory
kubectl get pods -n example-memory -w
```

## Try It

### Option 1: kagent UI (recommended)

```bash
kubectl port-forward -n kagent-system svc/kagent-ui 15000:8080
```

Open http://localhost:15000, select **personal-assistant**, and chat. The agent stores every message and retrieves relevant memories before each response -- all handled transparently by the ADK callbacks.

Example conversation:

```
You: Hi! I'm Alice, I'm a software engineer working on distributed systems.
Assistant: Nice to meet you, Alice! ...

You: I prefer Go for backend services and React for frontends.
Assistant: Great choices! ...

# (start a new session -- memories persist)

You: What do you remember about me?
Assistant: I remember you're a software engineer who works on distributed systems.
You prefer Go for backend services and React for frontend work...
```

### Option 2: A2A via curl

```bash
kubectl port-forward -n agentgateway-system svc/agentgateway-proxy 15003:80

curl -s -X POST http://localhost:15003/a2a/example-memory/personal-assistant \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "method": "message/send",
    "id": "1",
    "params": {
      "message": {
        "role": "user",
        "messageId": "msg-001",
        "parts": [{"kind": "text", "text": "Hi, I am Alice and I like distributed systems"}]
      }
    }
  }'
```

## Memory Types

EverMemOS extracts four types of memories from conversations:

| Type | Description | Example |
|------|-------------|---------|
| `profile` | Stable user facts | "Alice is a software engineer" |
| `episodic_memory` | Interaction summaries | "Discussed Go vs Rust for their new microservice" |
| `foresight` | Anticipated future needs | "Alice may need help with gRPC service design" |
| `event_log` | Atomic facts | "Alice mentioned she uses VS Code" |

## Retrieval Methods

All retrieval is server-side in EverMemOS:

| Method | Description | Latency | Best For |
|--------|-------------|---------|----------|
| `keyword` | BM25 text search | <100ms | Exact term matching |
| `vector` | Cosine similarity on embeddings | ~200ms | Semantic similarity |
| `hybrid` | Keyword + vector + LLM reranker | ~500ms | General use (recommended) |
| `rrf` | Keyword + vector, reciprocal rank fusion | ~300ms | Fast hybrid without LLM cost |
| `agentic` | Multi-round LLM-guided search | 2-5s | Complex queries requiring reasoning |

## Verify Waypoint Tracing

After running some conversations, check Langfuse for memory API traces:

```bash
# Port-forward Langfuse
kubectl port-forward -n langfuse svc/langfuse-web 13000:3000
```

Open http://localhost:13000 and look for traces from the `evermemos` service -- every memory API call (store, search, fetch) shows up with latency and status.

## Cleanup

```bash
kubectl delete -f manifests.yaml
```
