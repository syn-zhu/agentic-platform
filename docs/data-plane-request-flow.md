# Data-Plane Request Flow

There's a "Magenta API" service which lives in some control-plane cluster
(currently a Go gRPC in classic Helix). When a tenant deploys a
new agent, the request gets forwarded to this service and we spin up a pool
of executor pods in the tenant's cell cluster and namespace, behind a
**ClusterIP Service** with a shared label (e.g., `tenant: tenant-a`). For
the purpose of this discussion, I don't think it's super important how we
get from "request received" to "pods deployed" — the interesting part is
what happens next.

> **Why ClusterIP and not headless?** AgentGateway's waypoint mode identifies
> the target service by VIP lookup (`get_by_vip`). Headless services have no
> VIP, so that lookup fails. A regular ClusterIP service gives us a VIP the
> waypoint can match on, while AgentGateway still does its own endpoint
> selection internally.

## The invoke path

Later, a user comes along and invokes this agent. The exact URL shape
depends on the edge routing strategy (covered below), but the path portion
will look something like `/agents/{agentName}/invoke/{sessionId}`. This
request needs to get from the public internet to a specific executor pod in
the right cell cluster, passing through the tenant's AgentGateway waypoint
for L7 policy enforcement.

There are really two problems here:

1. **Edge → Cell:** How does traffic get from the internet to the right cell
   cluster?
2. **Cell → Pod:** Once in the cell, how does traffic get through the waypoint
   and land on the right pod?

Problem 2 is the same regardless of how we solve problem 1, so I'll cover
that first and then lay out the options for problem 1.

---

## Inside the cell: waypoint to pod

Each cell cluster is fully enrolled in ambient mesh. Every tenant namespace
is labeled to route traffic through its AgentGateway waypoint:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: tenant-a
  labels:
    istio.io/dataplane-mode: ambient
    istio.io/use-waypoint: tenant-a-waypoint
    istio.io/ingress-use-waypoint: "true"
```

The waypoint terminates the HBONE tunnel, looks up the service by VIP, and
runs the request through
its HTTP filter chain. An ExtProc plugin (the
[sandbox-scheduler](link-to-sandbox-scheduler-doc)) handles pod selection:

```yaml
# AgentGateway waypoint for this tenant
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: tenant-a-waypoint
  namespace: tenant-a
  labels:
    istio.io/waypoint-for: all
spec:
  gatewayClassName: agentgateway-waypoint
  listeners:
  - name: mesh
    port: 15008
    protocol: HBONE
---
# Single ClusterIP service selecting ALL agent pods in this tenant
apiVersion: v1
kind: Service
metadata:
  name: tenant-agents
  namespace: tenant-a
spec:
  selector:
    tenant: tenant-a
  ports:
  - port: 8080
---
# ExtProc policy — attaches to the tenant-agents service so every
# request through the waypoint goes through the sandbox-scheduler first
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata:
  name: sandbox-routing
  namespace: tenant-a
spec:
  targetRefs:
  - group: ""
    kind: Service
    name: tenant-agents
  backend:
    extProc:
      backendRef:
        name: sandbox-scheduler
        port: 9002
```

The sandbox-scheduler parses `/{agentName}/invoke/{sessionId}` from the path,
looks up or claims a pod for the session, and sets
[`x-gateway-destination-endpoint: <pod-ip>:<port>`](https://github.com/kubernetes-sigs/gateway-api-inference-extension/tree/main/docs/proposals/004-endpoint-picker-protocol).
AgentGateway reads this header and uses its `override_dest` mechanism to
route to that specific pod, bypassing its normal P2C load balancing.

The details of the sandbox-scheduler itself (where it stores state, how it
detects dead pods, how it picks idle ones) are worth a deeper discussion,
but that's outside the scope here.

---

## Getting to the cell: edge routing options

This is the part where there are real choices to make. The core constraint
is that Magenta cell clusters are **IPv6** and the waypoint needs to be
**in the mesh** to receive HBONE traffic. The existing Helix front Envoy
fleet (lb-h etc.) is IPv4 and not mesh-enrolled, so it can't reach IPv6
pods or speak HBONE directly.

The approach I think makes the most sense is to put a **public-facing
gateway inside each cell cluster**, exposed via a dualstack NLB. There's
already precedent for this in the SLS cell clusters — the CoreDNS and
scheduler NLBs are dualstack, internet-facing, and register themselves in
Route53 under `*.mdbsls.net`. A Magenta cell gateway would follow the same
pattern.

```yaml
# Public-facing gateway inside the cell cluster
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: magenta-cell-gateway
  namespace: magenta-system
spec:
  gatewayClassName: istio
  listeners:
  - name: https
    port: 443
    protocol: HTTPS
    tls:
      mode: Terminate
      certificateRefs:
      - name: magenta-wildcard-tls
    allowedRoutes:
      namespaces:
        from: All
---
# Exposed via dualstack NLB (same pattern as SLS cell NLBs)
apiVersion: v1
kind: Service
metadata:
  name: magenta-cell-gateway
  namespace: magenta-system
  annotations:
    service.beta.kubernetes.io/aws-load-balancer-type: 'external'
    service.beta.kubernetes.io/aws-load-balancer-nlb-target-type: 'ip'
    service.beta.kubernetes.io/aws-load-balancer-scheme: 'internet-facing'
    service.beta.kubernetes.io/aws-load-balancer-ip-address-type: 'dualstack'
spec:
  type: LoadBalancer
  selector:
    istio.io/gateway-name: magenta-cell-gateway
  ports:
  - port: 443
    targetPort: 8443
```

Because this gateway runs inside the mesh, `istio.io/ingress-use-waypoint`
on the tenant namespace makes istiod rewrite its EDS to tunnel through the
waypoint via HBONE automatically. The flow is two hops:

```
NLB (dualstack) → Cell Gateway (in mesh, IPv6) → HBONE → Waypoint → ExtProc → Pod
```

The NLB handles the IPv4/IPv6 boundary — it accepts IPv4 from the internet
and targets IPv6 pods internally. No changes to the existing Helix infra
needed, and no IPv6 support required on the classic front Envoy fleet.

The cell gateway has one HTTPRoute per tenant, auto-generated by the deploy
pipeline (or Kyverno) when a tenant is provisioned. Each route matches the
tenant's hostname (Option A) or path prefix (Options B–D) and forwards to
the tenant's `tenant-agents` ClusterIP service:

```yaml
# One route per tenant on the cell gateway (auto-generated on deploy)
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: tenant-a
  namespace: tenant-a
spec:
  parentRefs:
  - name: magenta-cell-gateway
    namespace: magenta-system
  hostnames:
  - "tenant-a.magenta.mongodb.com"  # Option A; or use path match for B–D
  rules:
  - backendRefs:
    - name: tenant-agents
      port: 8080
```

### The open question: how does the user's request reach the right cell's NLB?

This is where DNS strategy comes in. There are a few options, and I think
we should discuss these as a group:

**Option A: Per-tenant subdomains**

Each tenant gets a subdomain that CNAMEs to their cell's NLB:

```
tenant-a.magenta.mongodb.com → CNAME → magenta-gw.cell-1-us-east-1-aws.magenta-internal.mongodb.net
tenant-b.magenta.mongodb.com → CNAME → magenta-gw.cell-2-eu-west-1-aws.magenta-internal.mongodb.net
```

The invoke URL becomes `https://tenant-a.magenta.mongodb.com/agents/{agentName}/invoke/{sessionId}`.

- DNS does all the tenant→cell routing. No L7 edge proxy needed.
- The cell gateway only needs to handle its own tenants (no per-tenant
  routing rules — it can match on hostname from the TLS SNI / Host header).
- NLB is pure L4 — cheap, simple, no HTTP inspection.
- On deploy, the control plane creates a Route53 record pointing the
  tenant's subdomain to the cell's NLB hostname.
- Moving a tenant between cells = updating the DNS record.
- Latency-based DNS could route to the nearest cell if a tenant is
  multi-region.

Open questions:
- Do we want to expose per-tenant subdomains to customers, or is that a
  leaky abstraction?
- Wildcard TLS cert (`*.magenta.mongodb.com`) is straightforward, but does
  it play nicely with our cert management?
- Is there a max number of Route53 records we'd hit at scale?

**Option B: Per-cell subdomains + path-based tenant routing**

Each cell cluster gets a public subdomain (e.g., `us-east-1.magenta.mongodb.com`),
and the cell gateway does path-based tenant routing internally.

The invoke URL is `https://us-east-1.magenta.mongodb.com/{tenant}/agents/{agentName}/invoke/{sessionId}`.

- The control plane returns the cell-specific URL to the tenant on deploy.
- DNS is per-cell (small number of records), not per-tenant.
- The cell gateway handles per-tenant routing via HTTPRoute hostname or path
  matching — one route per tenant, auto-generated by Kyverno or the deploy
  pipeline.
- No L7 edge needed — DNS + NLB is enough.
- But the URL leaks the cell's region, which may not be desirable.

**Option C: Global Accelerator or Route53 latency routing**

Use a single domain (`magenta.mongodb.com`) pointed at an AWS Global
Accelerator or Route53 latency record that resolves to the nearest cell's
NLB.

- Works for geographic routing (tenant → nearest cell), but doesn't help
  when the tenant→cell mapping is arbitrary (e.g., tenant-a is in us-east-1
  because that's where they were provisioned, not because they're nearby).
- Could be combined with Option A (tenant subdomain + latency routing for
  multi-region tenants).

**Option D: Path-based routing with a thin L7 edge**

Keep a single domain (`magenta.mongodb.com`) and use path-based routing
(`/{tenant}/*`) at an L7 gateway that knows which tenant maps to which cell.

The invoke URL is `https://magenta.mongodb.com/{tenant}/agents/{agentName}/invoke/{sessionId}`.

- Requires an L7 proxy somewhere that can inspect the path and forward to
  the right cell NLB. This could be:
  - A new lb-X in the Helix front fleet (but that's IPv4-only, can't reach
    cells directly)
  - A shared Magenta-specific gateway in a management cluster
  - An existing lb-h/Heimdall route (same IPv4 problem)
- Adds a hop: `Edge L7 proxy → Cell NLB → Cell Gateway → Waypoint → Pod`
  (three hops instead of two).
- The L7 proxy needs a tenant→cell routing table, updated on deploy. This
  is what Heimdall does today for lb-h via xDS — so the pattern exists, but
  the IPv4/IPv6 gap means we'd need a proxy that can reach the cell NLBs.
- AWS Bedrock AgentCore uses this pattern (single regional endpoint,
  agent ARN in the path), but they own the L7 edge as a managed service.

Open questions:
- Where does this L7 proxy live? If it's in a Helix VPC, it needs to be
  able to reach cell NLBs (dualstack NLBs have IPv4 addresses too, so this
  might actually work even from an IPv4 VPC — needs verification).
- Is the extra hop acceptable?
- Could we use Heimdall to push routes dynamically, reusing the lb-h
  pattern?

---

## Summary

| Hop | From | To | Mechanism |
|-----|------|----|-----------|
| 1 | Internet | Cell Gateway | DNS (subdomain or path-based, TBD) → dualstack NLB |
| 2 | Cell Gateway | AgentGW Waypoint | `istio.io/ingress-use-waypoint` + HBONE tunnel |
| 3 | Waypoint | Specific Pod | ExtProc sandbox-scheduler + `x-gateway-destination-endpoint` |

Key design choices already made:
- **ClusterIP service, not headless.** AgentGateway's waypoint mode needs a VIP.
- **Public-facing cell gateway, not Helix front Envoy.** IPv6 cell clusters
  can't be reached from IPv4 Helix VPCs. Cell-local gateways with dualstack
  NLBs sidestep this entirely.
- **Namespace-level waypoint labels.** All services in a tenant namespace route
  through the same waypoint.
- **ExtProc for pod selection.** The sandbox-scheduler handles session→pod
  mapping, decoupled from the proxy.
- **Tenant isolation via ztunnel.** mTLS identity is cryptographic (SPIFFE),
  not IP-based. No per-tenant IPAM or CIDR segregation required.

Decision needed: **how to route from the internet to the right cell** (Options A–D above).
