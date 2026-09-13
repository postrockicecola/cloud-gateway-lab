# Day 2：多副本 Redis 限流与 token 配额

两个 Gateway Pod 不能各自在内存中维护限流和余额，否则同一用户会得到两份独立额度。本项目用 Redis Lua 将“清理窗口、计数、判断、写入、预扣 token”合并成一次原子操作。

## 请求链路

```text
Client
  → Service/gateway
  → Gateway Pod A 或 B
       1. 按 user ID 执行滑动窗口限流
       2. 预扣 prompt 估算值 + completion reserve
       3. 调用 mock provider
       4. 按响应 usage 结算，多退少补
  → Redis 共享窗口和余额
```

集群默认使用以下参数：

- `RATE_LIMIT=10`：一个窗口最多接受 10 个请求
- `RATE_LIMIT_WINDOW=1s`：滑动窗口长度
- `DEFAULT_BALANCE=10000`：首次访问时初始化的 token 余额
- `COMPLETION_RESERVE=32`：请求前预留的 completion token
- `REDIS_ADDR=redis:6379`：所有 Gateway Pod 使用同一 Redis

## 练习 1：触发共享限流

快速发送 15 个请求：

```bash
for i in $(seq 1 15); do
  curl -s -o /dev/null -w "%{http_code}\n" \
    http://localhost:8080/v1/chat/completions \
    -H "Authorization: Bearer sk-lab" \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}'
done
```

预期会先出现 `200`，随后出现 `429`。由于本地请求和处理耗时会让窗口向前滑动，精确数量可能略有差异。

查看指标：

```bash
curl -s http://localhost:8080/metrics
```

`gateway_rate_limited_total` 应增加。

## 练习 2：确认两个副本共用 Redis

```bash
kubectl get pods -n gateway-lab -l app=gateway -o wide
kubectl logs -n gateway-lab -l app=gateway --prefix --tail=50
kubectl exec -n gateway-lab deploy/redis -- redis-cli KEYS 'llm:*'
kubectl exec -n gateway-lab deploy/redis -- redis-cli ZCARD 'llm:rl:alice'
kubectl exec -n gateway-lab deploy/redis -- redis-cli GET 'llm:bal:alice'
```

日志会来自不同 Gateway Pod，但两个副本读写的是同一个 `llm:rl:alice` 和 `llm:bal:alice`。

## 练习 3：观察 token 预扣和结算

Mock provider 每次返回固定 usage：

```json
{
  "prompt_tokens": 4,
  "completion_tokens": 4,
  "total_tokens": 8
}
```

网关在调用上游前会按本地估算预扣 token；收到成功响应后，以 `total_tokens=8` 结算并退回多扣部分。

先记录余额，等待限流窗口结束后发起一次请求，再读取余额：

```bash
kubectl exec -n gateway-lab deploy/redis -- redis-cli GET 'llm:bal:alice'
sleep 1
curl -s http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-lab" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}'
kubectl exec -n gateway-lab deploy/redis -- redis-cli GET 'llm:bal:alice'
```

成功请求最终扣除 8 token。

## 为什么使用 Lua

如果应用依次执行 `ZREMRANGEBYSCORE → ZCARD → 判断 → ZADD`，两个 Pod 可能同时看到“还有一个名额”，然后都放行。Redis Lua 在单次脚本执行期间不会被其他命令插入，因此判断和写入是一个原子操作。

配额预扣也必须把“读取余额、判断余额、扣减”放在同一脚本中，否则两个请求可能同时花掉最后一笔余额。

对应实现位于：

- [`pkg/limiter/limiter.go`](../pkg/limiter/limiter.go)
- [`lua/rate_limit_prededuct.lua`](../lua/rate_limit_prededuct.lua)

## Redis 不可用时

```bash
kubectl scale deployment/redis -n gateway-lab --replicas=0
curl -i http://localhost:8080/readyz
```

`/readyz` 会返回 `503`，业务请求也会 fail-closed，而不是绕过限流。恢复 Redis：

```bash
kubectl scale deployment/redis -n gateway-lab --replicas=1
kubectl rollout status deployment/redis -n gateway-lab
```

下一步参见 [Day 3](day3-failover.md)，观察 endpoint 探活、熔断和跨 provider 故障转移。
