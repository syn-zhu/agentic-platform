# Phase 1: Foundation

Sets up the management cluster, Transit Gateway, and certificate authority.

## Prerequisites

- AWS CLI configured (`aws sts get-caller-identity` succeeds)
- `eksctl`, `kubectl`, `helm`, `helmfile` installed
- `envsubst` available (part of `gettext`)

## Run Order

```bash
./scripts/phase1/00-create-management-cluster.sh  # ~15-20 min (EKS creation)
./scripts/phase1/01-create-transit-gateway.sh      # ~2-3 min
./scripts/phase1/02-deploy-cert-manager.sh         # ~2-3 min
./scripts/phase1/03-deploy-capa.sh                 # ~3-5 min
./scripts/phase1/04-verify-foundation.sh           # ~30 sec
```

## What This Creates

- **Management EKS cluster** (`agentic-mgmt`) in VPC 10.0.0.0/16
- **Transit Gateway** with management VPC attached and routes to future cluster CIDRs
- **cert-manager** with root CA ClusterIssuer
- **Cluster API + CAPA** ready to provision additional EKS clusters

## Cleanup

```bash
# Delete CAPA resources first (if any managed clusters exist)
kubectl --context agentic-mgmt delete clusters --all -A

# Delete management cluster
eksctl delete cluster --name agentic-mgmt --region us-east-1

# Delete Transit Gateway
./cluster/transit-gateway/teardown-tgw.sh

# Delete IAM resources
aws iam detach-role-policy --role-name agentic-mgmt-capa-controller --policy-arn arn:aws:iam::$(aws sts get-caller-identity --query Account --output text):policy/capa-controller
aws iam delete-role --role-name agentic-mgmt-capa-controller
aws iam delete-policy --policy-arn arn:aws:iam::$(aws sts get-caller-identity --query Account --output text):policy/capa-controller
```

## Phase 2 Prerequisites

Before CAPA can manage remote clusters, cross-VPC security group rules must be configured:
- TCP 443 (kube API) from management VPC to all managed cluster VPCs
- TCP 15008 (HBONE) and TCP 15012 (istiod xDS) between all in-mesh cluster VPCs

These are set up as part of Phase 2 when each managed cluster is created.

## Next

Proceed to **Phase 2: Mesh Core** — creates control-plane, gateway, and observability clusters with Istio 1.29.
