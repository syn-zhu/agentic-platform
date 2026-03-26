# Phase 2: Provision Clusters + Per-Cluster Istio

## Prerequisites

- Phase 1 complete: management cluster running with CAPA
- `AWS_PROFILE` set, `kubectl`, `helm`, `helmfile` installed

## Run Order

```bash
./scripts/phase2/00-provision-clusters.sh     # ~20 min (4 clusters via CAPA)
./scripts/phase2/01-attach-vpcs-to-tgw.sh     # ~5 min
./scripts/phase2/02-install-istio.sh           # ~5 min
./scripts/phase2/03-verify.sh                  # ~30 sec
```

## What This Creates

- **agentic-cp** (10.1.0.0/16): Istio sidecar, ready for platform services
- **agentic-obs** (10.2.0.0/16): no mesh, ready for VictoriaMetrics/Grafana/Kiali
- **agentic-cell-1** (10.3.0.0/16): Istio ambient, 3 node groups (workload/waypoint/gateway)
- **agentic-cell-2** (10.4.0.0/16): Istio ambient, 3 node groups
- **Transit Gateway**: all 5 VPCs attached with cross-VPC routing + SG rules

## Next

- **Phase 3**: Deploy platform services to control-plane + observability stack
- **Phase 4**: Deploy cell services (kagent, EverMemOS, tenant onboarding)
