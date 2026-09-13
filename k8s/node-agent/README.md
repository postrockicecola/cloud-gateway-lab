# Node Agent (DaemonSet)

```text
Deployment:  I want N Pods
DaemonSet:   every eligible Node gets one Pod
```

```bash
kubectl get ds,pods -n gateway-lab -l app=node-agent -o wide
kubectl logs -n gateway-lab -l app=node-agent --tail=30
```

The process only prints hostname, loadavg, memory, and disk every 30s. It is not a metrics platform. Numbers are the container's view of `/proc`, plus `NODE_NAME` from the Downward API so you can see which Node the Pod landed on.
