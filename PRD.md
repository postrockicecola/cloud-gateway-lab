# Project PRD: Mini-LLM-Gateway

## 1. 项目简介与目标

构建一个基于 **Go + Redis** 的轻量级、高性能 **AI API 网关与调度中间件**。
项目旨在解决大模型 API 调用中的 **高并发限流、流式（SSE）传输拦截、精确 Token 预扣费/结算** 以及 **请求优先级排队** 问题。

## 2. 技术栈架构

* **开发语言**：Go (>= 1.22)
* **核心依赖**：
  * `net/http/httputil` (反向代理)
  * `github.com/redis/go-redis/v9` (Redis 客户端)
  * `github.com/pkoukk/tiktoken-go` (Token 数量预估)
  * `github.com/armon/go-radix` (内存前缀树索引)
* **下游服务**：兼容 OpenAI API 规范的本地 Ollama 服务 (`http://localhost:11434`) 或 Mock 服务。

## 3. 项目目录结构规范

```text
mini-llm-gateway/
├── cmd/
│   └── main.go                 # 服务启动入口与路由注册
├── pkg/
│   ├── limiter/                # 基于 Redis Lua 的滑动窗口限流与预扣费
│   ├── proxy/                  # SSE 流式反向代理与 Token 动态统计
│   ├── scheduler/              # 基于 Worker Pool 与 Channel 的优先级排队调度
│   └── prefixcache/            # 基于 Radix Tree 的 Prompt 前缀匹配
├── lua/
│   └── rate_limit_prededuct.lua # 滑动窗口限流与预扣费原子 Lua 脚本
├── go.mod
└── PRD.md
```

## 4. 核心功能模块与实现要求

### 模块 A：Redis Lua 原子限流与预扣费 (`pkg/limiter`)

1. **Lua 脚本需求 (`lua/rate_limit_prededuct.lua`)**：
   * 输入：`KEYS[1]` (限流 Key), `KEYS[2]` (余额 Key)；`ARGV[1]` (微秒时间戳), `ARGV[2]` (窗口时长微秒), `ARGV[3]` (限流上限 N), `ARGV[4]` (预扣 Token 数)。
   * 步骤：
     1. 使用 `ZREMRANGEBYSCORE` 剔除过期时间戳。
     2. `ZCARD` 检查当前窗口请求数，若 `>= N` 则返回失败状态 `RATE_LIMIT_EXCEEDED`。
     3. `GET` 检查用户余额，若 `< ARGV[4]` 则返回失败状态 `INSUFFICIENT_BALANCE`。
     4. `DECRBY` 预扣余额，`ZADD` 记录本次时间戳并设置 `EXPIRE`。
     5. 返回成功状态与成功标识。

2. **Go 调用封装**：提供 `PreDeduct(ctx, userID, tokens) (bool, error)` 方法。

### 模块 B：流式反向代理与异步结算 (`pkg/proxy`)

1. 基于 `httputil.NewSingleHostReverseProxy` 实现代理。
2. 重写 `ModifyResponse` 方法：
   * 利用 `io.Pipe` 打造零拷贝管道，将响应流实时吐给客户端。
   * 启动后台 Goroutine 读取响应流中的 SSE 数据行（`data: {...}`），解析生成内容并统计实际消耗的 Token 数量 `actualTokens`。
3. 请求结束后，触发 `SettleQuota(userID, preDeductedTokens, actualTokens)`：
   * 计算差额 `diff = preDeductedTokens - actualTokens`。
   * 若 `diff > 0`，通过 Redis `INCRBY` 原子的退还多扣配额；若 `diff < 0`，继续追扣。

### 模块 C：优先级队列调度器 (`pkg/scheduler`)

1. 设计 `Scheduler` 结构体：
   * `highPriorityChan`: 存放高优先级/VIP/短文本请求。
   * `lowPriorityChan`: 存放普通/长文本请求。
   * `workerSem`: 基于 buffered channel 实现信号量，限定并发打下游的总槽位数（如 `maxConcurrency = 5`）。
2. 提供 `Submit(ctx, req, priority)` 方法：当无空闲 worker 槽位时，请求进入队列排队，优先消费 `highPriorityChan`。

### 模块 D：Prompt 前缀缓存索引 (`pkg/prefixcache`)

1. 基于 `armon/go-radix` 封装 `PrefixIndexer`。
2. 提供 `MatchPrefix(prompt string) (hit bool, matchedLen int)`：
   * 提取输入 Prompt，检索是否存在相同前缀。
   * 若匹配度超过预设门限，给请求 Header 打上 `X-Prefix-Cache-Hit: true` 标记并记录日志（模拟 KV Cache 命中）。
