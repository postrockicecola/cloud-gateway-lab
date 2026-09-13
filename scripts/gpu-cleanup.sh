#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib.sh
. "$ROOT/scripts/lib.sh"

require_cluster

echo_step "remove GPU experiment Pods"
kubectl delete pod -n "$NS" -l experiment=gpu --ignore-not-found
kubectl delete pod -n "$NS" -l demo=gpu-insufficient --ignore-not-found

echo_step "remove mock-gpu advertisement and gpu labels (best effort)"
for node in $(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); do
  kubectl label node "$node" gpu- --ignore-not-found >/dev/null 2>&1 || true
  kubectl taint node "$node" gpu=true:NoSchedule- >/dev/null 2>&1 || true
  kubectl patch node "$node" --subresource status --type json --patch '[
    {"op":"remove","path":"/status/capacity/example.com~1mock-gpu"},
    {"op":"remove","path":"/status/allocatable/example.com~1mock-gpu"}
  ]' >/dev/null 2>&1 || true
done

echo "GPU experiment objects cleaned."
