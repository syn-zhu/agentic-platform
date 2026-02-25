#!/usr/bin/env bash
# port-forward.sh — Forward all platform UIs to localhost.
#
# Usage:
#   ./scripts/port-forward.sh          # start all
#   ./scripts/port-forward.sh stop     # kill all
#
# UIs:
#   kagent        http://localhost:15000
#   langfuse      http://localhost:15001
#   grafana       http://localhost:15002
#   agentgateway  http://localhost:15003/ui/
#   keycloak      http://localhost:15004
#   kiali         http://localhost:15005
#   agentregistry http://localhost:15006
set -euo pipefail

export AWS_PROFILE="${AWS_PROFILE:-agentic-platform}"

PID_DIR="/tmp/agentic-platform-pf"
mkdir -p "$PID_DIR"

# Kill tracked PIDs, then sweep for any orphaned kubectl port-forwards
# that match our port range (15000-15005) in case PID files were lost.
stop_all() {
  local found=false

  # 1. Kill tracked PIDs
  for pid_file in "$PID_DIR"/*.pid; do
    [[ -f "$pid_file" ]] || continue
    found=true
    pid=$(cat "$pid_file")
    if kill "$pid" 2>/dev/null; then
      echo "  Stopped $(basename "$pid_file" .pid) (pid $pid)"
    fi
    rm -f "$pid_file"
  done

  # 2. Sweep for orphaned kubectl port-forwards on our ports
  local orphans
  orphans=$(pgrep -f 'kubectl port-forward.*1500[0-6]:' 2>/dev/null || true)
  for pid in $orphans; do
    # Skip if we already killed it above
    [[ -f "$PID_DIR"/*.pid ]] 2>/dev/null && grep -qr "^${pid}$" "$PID_DIR" 2>/dev/null && continue
    kill "$pid" 2>/dev/null && echo "  Stopped orphaned port-forward (pid $pid)"
    found=true
  done

  if $found; then
    echo "Done."
  else
    echo "  No active port-forwards found."
  fi
}

if [[ "${1:-}" == "stop" ]]; then
  echo "Stopping port-forwards..."
  stop_all
  exit 0
fi

# Trap Ctrl-C / SIGTERM so backgrounded processes get cleaned up
trap 'echo ""; echo "Caught signal, cleaning up..."; stop_all; exit 0' INT TERM

# Kill any existing forwards first
echo "Stopping existing port-forwards..."
stop_all 2>/dev/null
echo ""

start_forward() {
  local name="$1" namespace="$2" target="$3" local_port="$4" remote_port="$5"

  kubectl port-forward -n "$namespace" "$target" "${local_port}:${remote_port}" >/dev/null 2>&1 &
  local pid=$!
  echo "$pid" > "$PID_DIR/${name}.pid"
  sleep 1

  if kill -0 "$pid" 2>/dev/null; then
    echo "  $name  http://localhost:${local_port}"
  else
    echo "  $name  FAILED (check if $target exists in $namespace)"
    rm -f "$PID_DIR/${name}.pid"
    return 1
  fi
}

echo "Starting port-forwards..."
echo ""

# Find the running agentgateway proxy pod
PROXY_POD=$(kubectl get pods -n agentgateway-system \
  -l app.kubernetes.io/name=agentgateway-proxy \
  --field-selector=status.phase=Running \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")

start_forward "kagent"        kagent-system         svc/kagent-ui                      15000 8080
start_forward "langfuse"      langfuse              svc/langfuse-web                   15001 3000
start_forward "grafana"       monitoring            svc/kube-prometheus-stack-grafana  15002 80

if [[ -n "$PROXY_POD" ]]; then
  start_forward "agentgateway" agentgateway-system "pod/$PROXY_POD" 15003 19000
else
  echo "  agentgateway  SKIPPED (no running proxy pod found)"
fi

start_forward "keycloak"      keycloak              svc/keycloak                       15004 8080
start_forward "kiali"         istio-system          svc/kiali                          15005 20001
start_forward "agentregistry" agentregistry         svc/agentregistry                  15006 8080

echo ""
echo "Run '$0 stop' to kill all forwards."
echo "Or press Ctrl-C to stop all and exit."

# Wait for all background processes so the script stays alive and the
# trap can catch Ctrl-C. If any forward dies, report it.
wait
