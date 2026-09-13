# Cloud Gateway Lab 面试讲稿

> 使用建议：先背熟 30 秒版和 2 分钟版，再从“限流配额”和“故障转移”中各选一个点深入。不要逐条罗列功能，要围绕“为什么做、怎么设计、如何验证、还有什么不足”来讲。

## 一句话定位

这是一个用 Go 实现、可部署到 Kubernetes 的 AI API Gateway 实验项目。它对客户端暴露 OpenAI-compatible 接口，在网关内部完成 API Key 鉴权、跨副本限流和 token 配额、异构模型协议适配、加权路由、健康检查、熔断及故障转移。

## 30 秒版本

我做了一个 AI API Gateway，主要解决多个模型供应商接入方式不统一，以及多副本网关下流量治理和故障隔离的问题。

客户端只需要调用 OpenAI-compatible 的 `/v1/chat/completions`。网关先做 Bearer API Key 鉴权，再通过 Redis Lua 原子完成滑动窗口限流和 token 预扣，然后按模型、健康状态、熔断状态和权重选择 endpoint。上游出现网络错误、5xx 或 429 时，会根据错误类型重试或切换到其他 endpoint。项目支持 OpenAI、Azure OpenAI 和 Anthropic，其中 Anthropic 的请求、响应和 SSE 流都转换成统一格式。

我还把它部署到了 kind：两个 Gateway Pod 共享 Redis，并用两个可控失败和延迟的 mock provider 验证限流、熔断和故障转移。这个项目定位是工程实验，不是生产就绪产品。

## 2 分钟版本

这个项目的背景是：AI 应用接入多个模型服务时，会遇到三个问题。

第一，不同供应商的 URL、鉴权头和协议不一致；第二，网关扩成多个副本后，单机内存限流会导致额度翻倍；第三，上游模型服务通常昂贵且不稳定，需要在请求真正失败前后都具备治理能力。

因此我把系统拆成几个部分：

1. **统一入口**：客户端统一调用 OpenAI-compatible Chat Completions，支持普通 JSON 和 SSE 流式响应。
2. **身份与额度**：API Key 只以 SHA-256 摘要查询。配置 MySQL 时，MySQL 保存 API Key 和 endpoint，Redis负责缓存；实验环境也支持内存配置。
3. **原子限流与配额**：请求到来后先估算 prompt token，再加上 `max_tokens` 或固定 completion reserve。Redis Lua 在一次执行中完成滑动窗口清理、计数、余额检查、预扣和窗口写入，避免两个 Gateway Pod 并发超发。响应返回后，再按上游 usage 多退少补。
4. **路由与容错**：每个逻辑模型可以配置多个 endpoint。路由先过滤掉不健康和熔断中的节点，再使用平滑加权轮询。真实请求的连续可重试失败会触发熔断；后台健康检查负责发现不可达 endpoint；单次请求还会排除失败节点并尝试其他 endpoint。
5. **协议适配**：路由层只依赖统一 provider 接口。OpenAI、Azure 和 Anthropic adapter 分别处理路径、鉴权、模型名及协议差异，Anthropic SSE 会被转换为 OpenAI delta 和 usage。

部署方面，我用 kind 启动两个 Gateway Pod、一个 Redis 和两个 mock provider。通过让某个 mock 返回 503 或直接下线，可以验证请求内切换、健康摘除和恢复。`/healthz` 只表示进程存活，`/readyz` 还检查 Redis，Redis 不可用时业务请求 fail-closed，避免绕过限流。

我认为项目最有价值的部分不是功能数量，而是把“一致性、成本控制和上游容错”放进了同一条实际请求链路，并且能在本地 Kubernetes 环境复现实验。

## 5～8 分钟完整讲稿

### 1. 项目背景

我把它定位成一个 AI 基础设施学习项目，而不是普通反向代理。

普通 API 网关主要按 QPS 和状态码治理流量；AI 请求还要考虑模型映射、token 成本、长连接流式响应，以及不同模型供应商的协议差异。因此我希望实现一条完整链路：客户端使用统一协议，网关负责身份、额度、路由和容错，上游可以自由切换。

### 2. 请求主链路

一次请求进入 `/v1/chat/completions` 后，会依次经过：

1. 生成或透传 `X-Request-ID`。
2. 从 Bearer token 识别用户。
3. 解析并校验 OpenAI-compatible 请求，限制请求体最大 8 MiB。
4. 使用 tiktoken 估算 prompt token；不可用时退化为字符数除以 4。
5. 调用 Redis Lua，同时检查滑动窗口和余额，并预扣 token。
6. 根据逻辑模型查找 endpoint，过滤 unhealthy 和熔断节点，再做平滑加权轮询。
7. 通过对应 provider adapter 发起普通或 SSE 请求。
8. 对可重试错误执行退避，并在当前请求中排除失败 endpoint，实现故障转移。
9. 使用上游返回的 usage 结算 token，多退少补。
10. 输出结构化日志和 Prometheus 文本指标。

### 3. 难点一：多副本下的原子限流和 token 配额

这里最容易犯的错误，是在应用里依次调用 Redis：

`删除过期记录 → 查询数量 → 判断 → 增加记录`

两个 Pod 可能同时查询到还有一个名额，最后都放行。余额扣减也有相同的检查再写入竞争。

我的处理方式是使用 Redis ZSet 保存请求时间戳，用 Lua 把窗口清理、计数、余额检查、扣减和写入合并成一个原子操作。成功后再给 ZSet 设置窗口级 TTL，避免长期残留。

token 配额采用“预扣 + 结算”：

- 请求前预扣：`估算 prompt token + max_tokens/默认预留量`；
- 请求成功：以上游 usage 为准，多退少补；
- 上游失败且没有实际 usage：退回预扣额度。

预扣的价值是防止并发请求先通过余额检查、再一起产生高额成本。代价是估算会暂时占用更多余额，而且当前结算操作本身不是带请求状态的幂等事务；生产环境应增加 reservation ID 和结算状态，避免进程退出或重复结算造成账务偏差。

### 4. 难点二：健康检查、熔断和重试的职责划分

这三者处理的问题不同：

- **主动健康检查**发现“这个 endpoint 现在能不能访问”，连续两次失败后标记为 unhealthy。
- **熔断器**根据真实业务调用结果判断“是否应该暂时停止给它压力”。连续可重试失败达到阈值后进入 Open，冷却后进入 Half-Open，只允许一个探测请求。
- **请求重试与故障转移**处理当前用户请求。400、401、403、404 直接失败；网络错误、超时和 5xx 可重试；429 优先尝试其他 endpoint。

路由只从健康且熔断器允许的节点中选择，并在单次请求中排除已经失败的 endpoint，避免重试再次打到同一个节点。

需要特别说明：Kubernetes readiness 决定 Pod 是否进入 Kubernetes Service；Gateway 的主动健康检查决定模型 endpoint 是否进入网关候选池；熔断器则基于真实业务失败保护上游。它们处在不同层次。

### 5. 难点三：统一多供应商协议

我没有在路由代码里写 provider 分支，而是定义统一接口，由 registry 根据 endpoint 配置创建 adapter。

- OpenAI-compatible adapter 处理标准 Bearer 鉴权和 Chat Completions；
- Azure adapter 处理 deployment 路径、`api-version` 和 `api-key`；
- Anthropic adapter 把 OpenAI messages 转成 Messages API：提取 system message，转换角色与 token 字段；流式响应则把 Anthropic event 转成 OpenAI-compatible SSE delta、usage 和 `[DONE]`。

这样路由、限流和熔断逻辑不感知供应商差异，新增 provider 时主要扩展 adapter 和配置校验。

### 6. 部署和验证

本地模式默认可连接 Ollama；kind 模式不依赖外部 API Key，而是部署两个 OpenAI-compatible mock provider。

Kubernetes 中有两个 Gateway 副本，它们通过 Service 访问 Redis 和 provider。ConfigMap 保存运行参数和 endpoint YAML，Secret 保存实验 API Key；Gateway 配置了 CPU/内存限制、liveness/readiness probe 和优雅退出。

我设计了三组可复现实验：

1. 快速发送超过窗口上限的请求，观察跨两个 Pod 的共享 `429` 和 Redis ZSet。
2. 观察一次成功请求前后的余额，验证最终按 mock usage 扣费。
3. 让高权重的 `mock-a` 下线或返回 503，验证健康摘除、请求内切换、熔断和恢复。

测试以 Go 单元测试为主，覆盖网关主链路、限流、熔断、路由、重试、健康检查、provider adapter、缓存和 mock provider；`make test` 还会执行 `go vet` 和 Kubernetes manifest 渲染校验。

### 7. 复盘与生产化方向

当前实现刻意保持实验规模，主要不足有：

1. Redis 是单实例，生产环境需要 Sentinel/Cluster、持久化和容量评估。
2. endpoint 健康状态、熔断状态和指标都在单个 Gateway Pod 内存中，各副本判断可能暂时不同；这能避免状态中心成为请求热路径瓶颈，但需要接受短暂不一致，并通过集中监控观察。
3. 指标目前由每个 Pod 暴露文本格式，需要 Prometheus 分别抓取和聚合。
4. 流式响应一旦已经向客户端发送数据，就不能安全地透明切换上游；当前重试更适合发生在首字节之前。生产实现应明确区分 pre-commit 和 mid-stream failure。
5. token 结算缺少持久化 reservation ledger。进程在预扣后崩溃时，可能无法自动退款。
6. 配置文件本身不热加载；MySQL endpoint 模式每 30 秒刷新。
7. 目前只覆盖 Chat Completions 的常用文本字段，尚未完整支持 tool calls、多模态、embeddings 和 responses API。

如果继续迭代，我会优先做幂等账务、流式失败语义、完整 Prometheus 指标与压测，再考虑管理面和更多模型协议。

## 高频追问与回答

### 为什么不用固定窗口限流？

固定窗口实现简单，但窗口边界可能在很短时间内放行接近两倍请求。滑动窗口能更准确地表达“任意连续时间段内最多 N 次”。代价是 ZSet 的存储和操作成本更高。当前实验用每个用户一个 ZSet，生产中还要评估高基数用户的内存占用。

### 为什么 Redis Lua 能保证原子性？

Redis 在执行单个 Lua 脚本时不会穿插执行其他客户端命令，所以脚本中的读取、判断和写入不会被另一个 Gateway Pod 打断。它解决的是 Redis 内部操作的原子性，不等于跨 Redis 与上游调用的分布式事务。

### 为什么限流和余额检查放在同一个脚本？

如果先占用限流名额、再发现余额不足，会污染窗口；如果先扣余额、再发现超限，又需要补偿。放在同一个脚本里可以确保只有两个条件都满足时才同时扣余额并记录请求。

### Redis 挂了为什么选择 fail-closed？

这个网关承担成本和额度控制。Redis 不可用时继续放量可能产生不可控账单，所以业务请求返回 503，readiness 也失败。若业务更看重可用性，可以设计有上限的本地应急额度，但必须接受跨副本超发并设置明确熔断线。

### 平滑加权轮询和普通加权随机有什么区别？

加权随机在长期符合比例，但短样本波动较大。平滑加权轮询会累加每个节点的当前权重，选择最高者后减去总权重，因此能把高权重流量更均匀地穿插在序列里。当前权重状态是每个 Gateway Pod 本地维护的，所以全局比例是近似值。

### 429 为什么不计入熔断失败？

429 通常表示供应商限额或短期流控，不一定代表 endpoint 故障。当前策略释放半开探测占用，并优先切换其他 endpoint，而不增加连续故障计数。生产中可以按 provider、租户和 Retry-After 做更细的限额建模。

### 如何避免缓存击穿？

API Key 查询先查 Redis；单进程内使用 singleflight 合并相同 key 的并发回源，跨 Pod 使用短期 Redis `SETNX` 锁；不存在的 key 使用短 TTL 空值缓存；正缓存 TTL 加抖动，减少同一时间集中失效。

### API Key 为什么存哈希？

查询时对原始 key 做 SHA-256，只使用摘要访问存储，减少数据库或缓存泄露时直接暴露有效凭据的风险。不过 SHA-256 不能替代完整的密钥管理，还需要 TLS、Secret 管理、轮换、审计和最小权限。

### 如何处理上游超时和客户端取消？

请求 context 会传递给 provider HTTP 请求。网络超时和 deadline exceeded 被归类为可重试错误，客户端取消则直接停止；退避等待也监听 context，避免客户端已经离开后仍继续占用资源。

### SSE 为什么难做故障转移？

HTTP 状态码和部分 token 一旦发送给客户端，响应就已经提交。如果此时切换 provider，可能重复文本或拼接两个不同模型的输出。因此透明故障转移只能安全发生在响应提交前；中途失败应终止流并让客户端决定是否重试，或者引入带序号的应用层恢复协议。

### 健康检查会不会造成误判？

会。单次网络抖动不应立刻摘除，因此当前连续两次失败才标记 unhealthy，成功后恢复。生产中还应加入并发探测、随机抖动、不同错误权重和观测窗口，避免所有 Gateway 同时探测造成尖峰。

### 这个项目和 Nginx/Kong 有什么区别？

它不是为了替代成熟通用网关，而是在应用层增加 AI 语义：逻辑模型映射、token 预算、provider 协议转换、usage 结算和模型 endpoint 容错。实际生产中可以把它放在 Ingress/API Gateway 后面，由前者负责 TLS、WAF 和通用流量治理。

### 如果 QPS 很高，当前实现的瓶颈在哪里？

首先是每次请求都要执行 Redis 脚本，其次是上游长连接占用和 token 结算写入。优化方向包括 Redis Cluster 分片、连接池与 pipeline、按用户哈希、批量或异步结算，以及网关并发和队列控制。但在优化前应先用 pprof、Redis latency 和端到端压测定位，而不是假设瓶颈。

## 面试中不要说过头

- 不要说“生产级”或“高并发已验证”：当前没有生产流量和压测数据。
- 不要说“强一致账务”：只有预扣脚本是原子的，上游调用和结算不是分布式事务。
- 不要说“全局熔断”：熔断与健康状态是 Gateway Pod 本地状态。
- 不要说“实现了真正的 Prompt/KV Cache”：当前 `prefixcache` 只维护前缀索引并标记命中。
- 不要说“优先级调度已进入主链路”：`pkg/scheduler` 和旧式 `pkg/proxy` 有实现与测试，但当前 `cmd/main.go` 使用的是 `internal/aigateway`，没有接入调度器。
- 不要承诺流式响应中途可以无感切换 provider。
- 不要把 mock provider 实验结果描述成真实云模型的 SLA 或性能结果。

## 可展示的 Demo 顺序

1. 正常请求：展示统一 OpenAI-compatible 接口和 `X-Request-ID`。
2. 连续请求：展示 `429`、Redis 中的窗口和余额。
3. 查看多 Pod 日志：证明两个副本共享同一 Redis 状态。
4. 让 `mock-a` 返回 503：展示同一 request ID 下先失败、再由 `mock-b` 成功。
5. 下线 `mock-a`：等待健康检查摘除，再展示恢复。
6. 查看 `/metrics`：展示请求、token、provider 错误、健康和熔断状态。

## 最后总结

这个项目让我真正处理了 AI 网关中的几个核心矛盾：多副本扩展与额度一致性、统一协议与供应商差异、提升可用性与避免重试放大，以及流式体验与失败恢复之间的冲突。

它目前是一个可运行、可测试、可复现实验的最小系统。下一步不是继续堆功能，而是补齐幂等账务、流式失败语义、集中监控和压测数据，把设计假设变成可量化结论。
