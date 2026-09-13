# Fault experiments

Healthy demos are not enough. Each experiment here is meant to break in a specific way.

```bash
make fault-pending
make fault-image
make fault-crash
make fault-readiness
make fault-gpu
make fault-cleanup
```

## 1. Pending + NODE=&lt;none&gt;

`pending.yaml` uses `nodeSelector.workload: nonexistent`.

```bash
kubectl get pod pending-nosuch-label -n gateway-lab -o wide
kubectl describe pod pending-nosuch-label -n gateway-lab
```

`Pending` with `NODE=<none>` means the Scheduler never bound the Pod. Check, in order:

- Node Ready
- Node Label
- nodeSelector / affinity
- taint / toleration
- requests vs Node allocatable

`Pending` with a Node name already assigned is usually kubelet / image / volume, not scheduling.

## 2. ImagePullBackOff

`image-pull.yaml` asks for `mock-model:does-not-exist`.

## 3. CrashLoopBackOff

`crash.yaml` sets `CRASH_ON_START=1` on the mock model server.

```bash
kubectl logs -n gateway-lab -l demo=crash
kubectl describe pod -n gateway-lab -l demo=crash
```

## 4. Readiness failure

`readiness.yaml` probes `/does-not-exist`. The container is Running; Ready is False. The Service has no ready EndpointSlice addresses.

> Running != Ready

## 5. GPU resource insufficient

After `make gpu-deploy` advertises 1 `example.com/mock-gpu`, `gpu-insufficient.yaml` requests 2.

This is the same Pending class as experiment 1, but the reason is resource, not label.
