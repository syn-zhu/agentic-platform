# Example 04: Skills

Demonstrates kagent's **skills system** — packaged domain expertise (instructions + executable scripts) distributed as OCI container images. The agent also discovers skills published to the AgentRegistry catalog at runtime.

## What It Shows

1. **Pre-loaded skills from OCI images** — The agent's `spec.skills.refs` points to a container image. At pod startup, an init container (`kagent-adk pull-skills`) extracts the image contents to `/skills/`. The ADK auto-registers `SkillsTool` and `BashTool` — no explicit tool configuration needed.

2. **Registry skill discovery** — The agent uses `list_skills` / `get_skill` MCP tools from AgentRegistry to browse a catalog of published skills at runtime.

3. **Skills + MCP tools** — The skill's `SKILL.md` instructs the agent to use Kubernetes MCP tools (from the kagent tool-server) for cluster operations like health checks and resource listing. This keeps cluster queries going through an infrastructure service that already has RBAC access, rather than trying to call the K8s API directly from within the sandboxed agent container.

## Architecture

```
                         Pod: skill-agent
┌──────────────────────────────────────────────────────┐
│                                                      │
│  Init Container (skills-init)                        │
│  ┌────────────────────────────────────────┐          │
│  │ kagent-adk pull-skills                 │          │
│  │   └─ krane export → /skills/           │          │
│  └────────────────────────────────────────┘          │
│              │ emptyDir volume                       │
│              ▼                                       │
│  Main Container (kagent)                             │
│  ┌────────────────────────────────────────┐          │
│  │                                        │          │
│  │  /skills/skill-platform-runbook/       │          │
│  │  ├── SKILL.md    (instructions)        │          │
│  │  └── scripts/    (executables)         │          │
│  │                                        │          │
│  │  Auto-registered tools:                │          │
│  │    • skills  — load skill content      │          │
│  │    • bash    — execute skill scripts   │          │
│  │                                        │          │
│  │  MCP tools (AgentRegistry):            │          │
│  │    • list_skills — browse catalog      │          │
│  │    • get_skill   — read skill details  │          │
│  │                                        │          │
│  │  MCP tools (kagent-tool-server):       │          │
│  │    • k8s_get_resources                 │          │
│  │    • k8s_get_pod_logs                  │          │
│  │    • k8s_describe_resource             │          │
│  │    • k8s_get_events                    │          │
│  └────────────────────────────────────────┘          │
└──────────────────────────────────────────────────────┘
          │ MCP calls                 │ MCP calls
          ▼                           ▼
   AgentRegistry             kagent-tool-server
   (skill catalog)           (shells out to kubectl
                              with cluster-admin RBAC)
```

## Why MCP Tools Instead of Direct K8s API Calls?

Skill scripts run inside the **Anthropic Sandbox Runtime** (`srt`), which creates a network-isolated environment using Linux namespaces (`bwrap --unshare-net`). Making direct K8s API calls from inside this sandbox requires configuring:
- srt network allowlists (`~/.srt-settings.json`)
- NetworkPolicy egress rules for the K8s API ClusterIP
- ztunnel bypass annotations (ambient mesh intercepts K8s API traffic)
- RBAC ClusterRoles for the agent's service account

Using the kagent tool-server's MCP tools avoids all of this — the tool-server already runs in `kagent-system` with full cluster access, and the agent communicates with it over the existing MCP channel (which the NetworkPolicy already allows).

## Prerequisites

1. Platform deployed (`platform/manifests/`)
2. AgentRegistry RemoteMCPServer applied (`platform/manifests/agentregistry-remotemcpserver.yaml`)
3. Skill OCI image pushed to ECR (see [Building the Skill Image](#building-the-skill-image))

## Building the Skill Image

The skill image is a minimal `FROM scratch` container that bundles a `SKILL.md` file and executable scripts:

```bash
cd examples/04-skills/skills/platform-runbook
docker buildx build --platform linux/amd64 \
  -t public.ecr.aws/w1e2w7d8/agentic-platform/skill-platform-runbook:v0.4.0 \
  --push .
```

## Deploy

```bash
kubectl apply -f examples/04-skills/manifests.yaml
```

Optionally, publish a skill to the AgentRegistry catalog for the `list_skills` demo:

```bash
kubectl port-forward -n agentregistry svc/agentregistry 18080:8080 &

curl -X POST http://localhost:18080/v0/skills \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "promql-guide",
    "title": "PromQL Query Guide",
    "description": "Reference guide for writing PromQL queries — covers selectors, aggregations, rate functions, histogram quantiles, and common patterns for monitoring Kubernetes workloads.",
    "version": "1.0.0",
    "category": "observability"
  }'
```

## Demo

1. **"What skills do you have available?"**
   → Agent lists `skill-platform-runbook` from the `skills` tool description

2. **"Run the diagnostics"**
   → Agent loads skill via `skills(command="skill-platform-runbook")`, executes `python3 scripts/diagnostics.py` via `bash`

3. **"Run a health check on the platform"**
   → Agent loads skill, then uses `k8s_get_resources` to list pods across platform namespaces and report their status

4. **"What skills are in the registry?"**
   → Agent calls `list_skills`, shows published skills from the catalog

5. **"Tell me about the PromQL guide"**
   → Agent calls `get_skill(name="promql-guide")`, reads description and metadata

## Cleanup

```bash
kubectl delete -f examples/04-skills/manifests.yaml
```
