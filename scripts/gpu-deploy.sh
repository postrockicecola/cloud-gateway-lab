#!/usr/bin/env bash
# Deploy a GPU experiment. Prefer real nvidia.com/gpu; otherwise simulate.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib.sh
. "$ROOT/scripts/lib.sh"

require_cluster

echo_step "GPU prerequisite check"
"$ROOT/scripts/gpu-check.sh"

if node_has_resource 'nvidia.com/gpu'; then
  gpu_node="$(first_worker)"
  echo_step "Mode A: real NVIDIA GPU on ${gpu_node}"
  kubectl label node "$gpu_node" gpu=true --overwrite
  kubectl apply -f "$ROOT/k8s/namespace.yaml"
  kubectl apply -f "$ROOT/k8s/gpu/gpu-workload.yaml"
  echo
  echo "Watch: kubectl get pod gpu-workload -n $NS -o wide"
  echo "Node:  kubectl describe node $gpu_node | grep -A4 'nvidia.com/gpu'"
  exit 0
fi

node="$(first_worker)"
echo_step "Mode B: scheduler simulation on ${node}"
echo "Advertising example.com/mock-gpu=1"
echo "This is NOT a real NVIDIA GPU."

kubectl label node "$node" gpu=true --overwrite

# Official extended-resource teaching method: PATCH Node status.
# kubelet does not overwrite unknown extended resources.
kubectl patch node "$node" --subresource status --type json --patch '[
  {"op":"add","path":"/status/capacity/example.com~1mock-gpu","value":"1"},
  {"op":"add","path":"/status/allocatable/example.com~1mock-gpu","value":"1"}
]'

kubectl apply -f "$ROOT/k8s/namespace.yaml"
kubectl apply -f "$ROOT/k8s/gpu/mock-gpu.yaml"

echo
kubectl get pod mock-gpu-workload -n "$NS" -o wide || true
echo
echo "describe Node capacity:"
kubectl describe node "$node" | grep -E 'Name:|example.com/mock-gpu|nvidia.com/gpu' || true
echo
echo "mock-gpu only teaches extended-resource scheduling."
echo "It is not equivalent to nvidia.com/gpu or a Device Plugin."
