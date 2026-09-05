# 第二天练习：滑动窗口限流与 Redis 预扣费

第一天把网关做成了 **2 个副本**。这正好是分布式竞态的舞台：同一用户的请求会打到不同 gateway Pod。如果每台机器自己数，或者各自对 Redis 做 `GET → 判断 → SET`，两台都会放行「还剩 1 次」的请求。

今天把限流和配额做成**所有副本共享的一张账本**。账本在 Redis 里，读写收成一条 Lua 脚本。

## 请求链路

```text
client
  → gateway Service
  → 某个 gateway Pod
       1. 滑动窗口限流（按 路由 + 身份）
       2. 预扣费 / 配额（按身份）
       3. 反代到 users / products
```

身份优先读 `X-User-ID`（开放平台套餐），否则用 `X-Forwarded-For` / 对端 IP。

集群默认配置在 `deploy/k8s/base/resources.yaml`：

| 变量 | 默认值 | 含义 |
|---|---|---|
| `RATE_LIMIT_BACKEND` | `redis` | `memory` 是单机窗口，会超卖 |
| `RATE_LIMIT_LIMIT` | `10` | 窗口内最多放行次数 |
| `RATE_LIMIT_WINDOW` | `1s` | 滑动窗口长度 |
| `QUOTA_DEFAULT` | `25` | 每个身份的初始余额；`0` 表示关闭预扣 |
| `REDIS_ADDR` | `redis:6379` | 共享账本 |

本地 `go run ./cmd/gateway` 默认是内存限流、100 次/秒、不扣配额，方便三终端联调。

## 为什么必须是 Redis + Lua

滑动窗口这 4 步必须一起成功或一起不可见：

1. 清掉窗口外的时间戳 `ZREMRANGEBYSCORE`
2. 统计窗口内数量 `ZCARD`
3. 判断是否超限
4. 未超限则 `ZADD` 写入本次请求

脚本在 `internal/ratelimit/sliding_window.lua`。Redis 把整段 `EVAL` 当成一条命令，别的 gateway Pod 插不进中间态。

Sorted Set 的 member 必须全局唯一：score 是毫秒时间戳，同一毫秒里两台机器如果写入同一个 member，`ZADD` 会覆盖而不是新增，窗口计数偏小，限流被击穿。实现里用「时间戳 + 实例 ID + 序号」。

预扣费同理，见 `internal/quota/reserve.lua`：读余额 → 判断 → 扣减，避免两台机器花掉最后一枚 token。

`atomic.Uint64` 救不了这件事。它只保证**一个进程**里计数不丢；两个 Pod 各有一份内存。

## 练习

做之前先 `make images && make load && make deploy && make port-forward`。

### 1. 同一用户打满滑动窗口

```bash
for i in $(seq 1 15); do
  curl -s -o /tmp/body -w "%{http_code} remaining=%header{X-RateLimit-Remaining}\n" \
    -H "X-User-ID: alice" http://localhost:8080/api/users/1
done
```

预期：大约前 10 个 `200`，后面 `429`，`Retry-After: 1`。等 1 秒再打，窗口滑开，又能通过。

看指标：

```bash
curl -s http://localhost:8080/metrics
```

`gateway_rate_limited_total` 应增加。

### 2. 两个副本共用一本账

```bash
kubectl get pods -n gateway-lab -l app=gateway -o wide
kubectl logs -n gateway-lab -l app=gateway --tail=20
```

连续请求会落到不同 Pod（日志里的 hostname / 时间交错），但 Redis 里同一个 key 的计数是共享的。用 redis-cli 看窗口：

```bash
kubectl exec -n gateway-lab deploy/redis -- redis-cli KEYS 'gw:*'
kubectl exec -n gateway-lab deploy/redis -- redis-cli ZCARD 'gw:rl:users:user:alice'
```

### 3. 对比：改成内存限流会超卖

把 ConfigMap 的 `RATE_LIMIT_BACKEND` 改成 `memory`，应用后重启网关，再跑练习 1 的循环。两个 Pod 各放行最多 10 次，同一用户可能看到超过 10 个 `200`。改回 `redis` 后现象消失。

### 4. 配额预扣

集群默认每个身份 25 次。多打几轮（中间等窗口滑开），第 26 个成功请求之前会看到 `403 quota exceeded` 和 `X-Quota-Remaining: 0`。

```bash
kubectl exec -n gateway-lab deploy/redis -- redis-cli GET 'gw:quota:user:alice'
```

### 5. Redis 挂了会发生什么

```bash
kubectl delete pod -n gateway-lab -l app=redis
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8080/readyz
```

预期：`/readyz` 变 `503`，网关从 Service 摘流；业务请求在 Redis 恢复前 fail-closed（`503 limiter unavailable`），而不是偷偷放行。Deployment 会把 redis Pod 拉起来，就绪后再接流量。

## 和面试话术的对应

> 评估流量 → 静态/动态限流防御 → 内存级预扣（Redis+Lua） → 最终一致性保障

当前仓库覆盖了前三段。异步解冻 / MQ 结算还没做，余额扣减是同步预扣，适合先把「多副本不能拆开 check-and-set」讲清楚。
