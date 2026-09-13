#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib.sh
. "$ROOT/scripts/lib.sh"

require_cluster
kubectl apply -f "$ROOT/k8s/namespace.yaml"

case "${1:-}" in
  pending)
    echo_step "Experiment 1: nodeSelector matches no Node"
    kubectl apply -f "$ROOT/k8s/faults/pending.yaml"
    sleep 2
    kubectl get pod pending-nosuch-label -n "$NS" -o wide || true
    echo
    echo "Expected: Pending, NODE=<none>"
    echo "kubectl describe pod pending-nosuch-label -n $NS"
    kubectl describe pod pending-nosuch-label -n "$NS" | tail -n 20 || true
    ;;
  image)
    echo_step "Experiment 2: missing image"
    kubectl apply -f "$ROOT/k8s/faults/image-pull.yaml"
    sleep 3
    kubectl get pod fault-image-pull -n "$NS" -o wide || true
    echo
    echo "Expected: ErrImagePull / ImagePullBackOff"
    kubectl describe pod fault-image-pull -n "$NS" | tail -n 16 || true
    ;;
  crash)
    echo_step "Experiment 3: process exits on start"
    kubectl apply -f "$ROOT/k8s/faults/crash.yaml"
    echo "waiting for CrashLoopBackOff..."
    sleep 8
    kubectl get pods -n "$NS" -l demo=crash -o wide || true
    echo
    echo "Expected: CrashLoopBackOff"
    echo "kubectl logs -n $NS -l demo=crash"
    kubectl logs -n "$NS" -l demo=crash --tail=20 || true
    ;;
  readiness)
    echo_step "Experiment 4: Running but not Ready"
    kubectl apply -f "$ROOT/k8s/faults/readiness.yaml"
    sleep 6
    kubectl get pods -n "$NS" -l demo=readiness -o wide || true
    kubectl get endpointslice -n "$NS" -l kubernetes.io/service-name=fault-readiness || true
    echo
    echo "Expected: Running, Ready=False, empty EndpointSlice addresses"
    echo "Running != Ready"
    ;;
  gpu)
    echo_step "Experiment 5: mock GPU available < requested"
    if ! node_has_resource 'example.com/mock-gpu'; then
      echo "advertising 1 mock GPU first via make gpu-deploy"
      "$ROOT/scripts/gpu-deploy.sh"
    fi
    kubectl apply -f "$ROOT/k8s/faults/gpu-insufficient.yaml"
    sleep 2
    kubectl get pod fault-gpu-insufficient -n "$NS" -o wide || true
    echo
    echo "Expected: Pending because Node has 1 mock-gpu and Pod asks for 2"
    kubectl describe pod fault-gpu-insufficient -n "$NS" | tail -n 16 || true
    ;;
  cleanup)
    echo_step "remove fault experiment objects"
    kubectl delete -f "$ROOT/k8s/faults/pending.yaml" --ignore-not-found
    kubectl delete -f "$ROOT/k8s/faults/image-pull.yaml" --ignore-not-found
    kubectl delete -f "$ROOT/k8s/faults/crash.yaml" --ignore-not-found
    kubectl delete -f "$ROOT/k8s/faults/readiness.yaml" --ignore-not-found
    kubectl delete -f "$ROOT/k8s/faults/gpu-insufficient.yaml" --ignore-not-found
    echo "fault objects cleaned."
    ;;
  *)
    echo "usage: $0 {pending|image|crash|readiness|gpu|cleanup}" >&2
    exit 2
    ;;
esac
