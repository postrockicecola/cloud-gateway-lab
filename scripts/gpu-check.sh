#!/usr/bin/env bash
# Report GPU lab prerequisites. Never fails the rest of the project.
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib.sh
. "$ROOT/scripts/lib.sh"

echo "GPU experiment requires:"
echo "  - NVIDIA GPU"
echo "  - NVIDIA driver"
echo "  - NVIDIA Container Toolkit"
echo "  - Kubernetes NVIDIA Device Plugin"
echo

ok=0
if has_nvidia; then
  echo "[ok] nvidia-smi"
  nvidia-smi -L || true
  ok=1
else
  echo "[missing] NVIDIA GPU / driver (nvidia-smi)"
fi

if command -v nvidia-container-cli >/dev/null 2>&1; then
  echo "[ok] NVIDIA Container Toolkit"
else
  echo "[missing] NVIDIA Container Toolkit"
fi

if command -v kubectl >/dev/null 2>&1 && kubectl cluster-info >/dev/null 2>&1; then
  if node_has_resource 'nvidia.com/gpu'; then
    echo "[ok] Node advertises nvidia.com/gpu"
    ok=1
  else
    echo "[missing] nvidia.com/gpu on any Node (Device Plugin not running or no GPU)"
  fi
else
  echo "[skip] no Kubernetes cluster; cannot check Device Plugin"
fi

echo
if [ "$ok" -eq 0 ]; then
  echo "This machine has no usable NVIDIA GPU for Kubernetes."
  echo "Ordinary labs still work:  make k8s-up && make k8s-deploy"
  echo "Scheduler concept without a GPU:  make gpu-deploy"
  echo
  echo "mock-gpu is NOT equivalent to nvidia.com/gpu."
  echo "It only shows: Node has N units of a custom resource, Pod requests N, Scheduler places."
else
  echo "Real GPU path is available. make gpu-deploy will prefer nvidia.com/gpu."
fi
exit 0
