#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib.sh
. "$ROOT/scripts/lib.sh"

require_cluster

kubectl delete pod -n "$NS" -l experiment=scheduling --ignore-not-found

for node in $(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); do
  kubectl label node "$node" workload- --ignore-not-found >/dev/null 2>&1 || true
  kubectl label node "$node" gpu- --ignore-not-found >/dev/null 2>&1 || true
  kubectl taint node "$node" gpu=true:NoSchedule- >/dev/null 2>&1 || true
done

echo "scheduling experiment objects and node decorations cleaned."
