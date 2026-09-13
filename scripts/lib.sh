#!/usr/bin/env bash
# Shared helpers for lab scripts. Source this file; do not execute it.

CLUSTER="${CLUSTER:-gateway-lab}"
NS="${NS:-gateway-lab}"

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    return 1
  fi
}

require_cluster() {
  need_cmd kubectl || return 1
  if ! kubectl cluster-info >/dev/null 2>&1; then
    echo "no Kubernetes cluster. Run: make k8s-up" >&2
    return 1
  fi
}

worker_nodes() {
  kubectl get nodes -l '!node-role.kubernetes.io/control-plane' -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null \
    || kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'
}

first_worker() {
  local node
  node="$(worker_nodes | head -n 1)"
  if [ -z "$node" ]; then
    node="$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')"
  fi
  if [ -z "$node" ]; then
    echo "no Node found" >&2
    return 1
  fi
  printf '%s\n' "$node"
}

echo_step() {
  printf '\n==> %s\n' "$*"
}

has_nvidia() {
  command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi >/dev/null 2>&1
}

node_has_resource() {
  local resource="$1"
  kubectl get nodes -o json 2>/dev/null | grep -q "\"${resource}\""
}
