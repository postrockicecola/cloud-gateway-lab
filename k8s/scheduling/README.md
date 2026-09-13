# Scheduler experiments

These manifests isolate Kubernetes scheduling. They do not start the AI Gateway.

```text
Pod
 ↓
Kubernetes Scheduler
 ↓
Node
```

## requests vs limits

`cpu-memory.yaml` asks for 2 CPU and 4Gi memory.

- **requests** are the Scheduler's packing input: "does this Node still have that much unused allocatable?"
- **limits** are the runtime ceiling (cgroup). A Pod can burst up to its limit.

If your kind Node has less than 2 CPU or 4Gi allocatable, the Pod stays `Pending` with `NODE=<none>`. That is the lesson, not a broken YAML.

```bash
make scheduling-demo
kubectl describe pod cpu-memory-demo -n gateway-lab
```

## Node Label + nodeSelector

```bash
kubectl label node <worker> workload=ai
kubectl apply -f k8s/scheduling/node-selector.yaml
kubectl get pod node-selector-match -n gateway-lab -o wide
```

The anti-example lives in [`../faults/pending.yaml`](../faults/pending.yaml): `workload: nonexistent` cannot match any Node, so the Pod stays Pending.

## Affinity

`affinity.yaml` is nodeSelector expressed as `matchExpressions`. Same placement idea, richer operators.

## Taint / Toleration

Label and taint are different:

| Mechanism | Question it answers |
| --- | --- |
| Label + nodeSelector / affinity | Which class of Node should this Pod prefer? |
| Taint + Toleration | Which Pods are allowed onto this Node? |
| Resource request | Does the Node still have enough CPU / memory / GPU? |

```bash
make scheduling-taint
kubectl get pods -n gateway-lab -l demo=taint -o wide
kubectl describe pod ordinary-no-toleration -n gateway-lab
```

Clean up:

```bash
make scheduling-cleanup
```
