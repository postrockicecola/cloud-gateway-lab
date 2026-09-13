#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib.sh
. "$ROOT/scripts/lib.sh"

require_cluster
node="$(first_worker)"

echo_step "label + taint ${node} as a GPU-style dedicated Node"
kubectl label node "$node" gpu=true --overwrite
kubectl taint node "$node" gpu=true:NoSchedule --overwrite

kubectl apply -f "$ROOT/k8s/namespace.yaml"
kubectl apply -f "$ROOT/k8s/scheduling/taint-toleration.yaml"

echo
echo "ordinary-no-toleration should stay Pending."
echo "gpu-with-toleration should schedule onto ${node}."
echo
kubectl get pods -n "$NS" -l demo=taint -o wide || true
echo
echo "kubectl describe pod ordinary-no-toleration -n $NS"
echo "Undo: make scheduling-cleanup"
