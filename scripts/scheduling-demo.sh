#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib.sh
. "$ROOT/scripts/lib.sh"

require_cluster
kubectl apply -f "$ROOT/k8s/namespace.yaml"

node="$(first_worker)"
echo_step "label ${node} workload=ai"
kubectl label node "$node" workload=ai --overwrite

echo_step "apply CPU/memory, nodeSelector, affinity demos"
kubectl apply -f "$ROOT/k8s/scheduling/cpu-memory.yaml"
kubectl apply -f "$ROOT/k8s/scheduling/node-selector.yaml"
kubectl apply -f "$ROOT/k8s/scheduling/affinity.yaml"

echo
echo "Wait a few seconds, then:"
echo "  kubectl get pods -n $NS -l experiment=scheduling -o wide"
echo "  kubectl describe pod cpu-memory-demo -n $NS"
echo
echo "cpu-memory-demo requests 2 CPU + 4Gi."
echo "If the kind Node is smaller, it stays Pending — that is the Scheduler saying no."
echo
kubectl get pods -n "$NS" -l experiment=scheduling -o wide || true
echo
echo "Taint experiment (optional): make scheduling-taint"
echo "Pending anti-example:        make fault-pending"
