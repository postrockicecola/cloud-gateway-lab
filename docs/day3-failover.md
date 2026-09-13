# Day 3：上游健康检查、熔断与故障转移

kind 配置为模型 `gpt-5` 注册了两个 endpoint。`mock-a` 权重为 3，`mock-b` 权重为 1。正常情况下大部分请求会落到 A；A 不可用时，网关应自动改用 B。

## 练习 1：观察加权选择

以较慢速度发送请求，避免触发限流：

```bash
for i in $(seq 1 12); do
  curl -s http://localhost:8080/v1/chat/completions \
    -H "Authorization: Bearer sk-lab" \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}'
  echo
  sleep 0.2
done
```

响应应同时出现 `response from mock-a` 和 `response from mock-b`，其中 A 通常更多。权重不是强制比例，少量请求的分布可能有偏差。

## 练习 2：让一个 endpoint 离线

```bash
kubectl scale deployment/mock-a -n gateway-lab --replicas=0
kubectl get endpoints mock-a -n gateway-lab -w
```

等待约 6 秒。主动健康检查连续失败两次后，会把 `mock-a` 标记为 unhealthy。再次发送请求：

```bash
curl -s http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-lab" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5","messages":[{"role":"user","content":"fail over"}]}'
```

响应应来自 `mock-b`。查看网关日志和指标：

```bash
kubectl logs -n gateway-lab -l app=gateway --prefix --tail=80
curl -s http://localhost:8080/metrics
```

## 练习 3：观察请求内重试

恢复 A，并把它配置为返回 `503`：

```bash
kubectl scale deployment/mock-a -n gateway-lab --replicas=1
kubectl rollout status deployment/mock-a -n gateway-lab
kubectl set env deployment/mock-a -n gateway-lab FAIL_STATUS=503
kubectl rollout status deployment/mock-a -n gateway-lab
```

`/v1/models` 探针仍然成功，因此 A 保持 healthy。当请求选中 A 时，Gateway 会收到可重试的 `503`，在同一请求中排除 A，然后改选 B。

连续发送几次请求，并在日志中寻找同一个 `request_id` 对应的失败和成功 provider call：

```bash
kubectl logs -n gateway-lab -l app=gateway --prefix --tail=100
```

恢复 A：

```bash
kubectl set env deployment/mock-a -n gateway-lab FAIL_STATUS-
kubectl rollout status deployment/mock-a -n gateway-lab
```

## 练习 4：观察 endpoint 恢复

如果 A 曾经被缩容，确保它已经恢复：

```bash
kubectl scale deployment/mock-a -n gateway-lab --replicas=1
kubectl rollout status deployment/mock-a -n gateway-lab
```

下一轮主动探活成功后，A 会重新进入候选池。再次运行练习 1，可以重新看到来自两个 provider 的响应。

## 三种状态不要混淆

- Kubernetes readiness：决定 mock provider Pod 是否进入自己的 Service Endpoint。
- Gateway 主动健康检查：决定某个模型 endpoint 是否进入路由候选池。
- Gateway 熔断器：根据真实请求的连续失败临时阻止流量，冷却后允许恢复。

它们处理的是不同层次的问题：Kubernetes 管 Pod 流量，健康检查管上游可达性，熔断器管真实调用的失败压力。
