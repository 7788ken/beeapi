# beeapi 功能与设计说明

> 基于 [QuantumNous/new-api](https://github.com/QuantumNous/new-api)（AGPL-3.0）的自研增强。
> 与上游逐文件比对：上游 1997 文件 / 本 fork 2426 文件，**fork 独有 1810 个**（前端 1442、后端 368）。
> 提交构成：**329 个 commit 触及本仓内容**，其中 fix 130 / feat 124 / refactor 17 / perf 3
> （口径为「改动过本仓文件」的提交；私有仓另有百余个只动部署脚本与运维文档的提交，不在开源范围内）。
> 本文档收录 **41 项自研功能与修复**，每条含：介绍 / 原理 / 实现方案。

---

# 一、流量治理与容错

## 1. 协议互转层 relayconvert

**介绍**
一个网关要同时接 OpenAI Chat Completions、Claude Messages、Gemini generateContent、OpenAI Responses 四种协议，客户端用哪种、上游支持哪种，是两件独立的事。传统做法是给每个渠道类型写一套 adapter，N 种入协议 × M 种出协议就要写 N×M 份转换代码，且流式和非流式各一份，很快失控。

**原理**
把「协议转换」抽象成**有向图上的路由问题**。每种协议是一个节点（`types.RelayFormat`），每个转换器是一条有向边。请求进来时只声明"我是 A 格式，目标是 B 格式"，由注册表查路由：有直达边就走直达，没有就走多跳（如 Responses → Chat → Claude）。每条边带**质量标注**（`good` / `fair` / `discouraged`），多跳路径的质量取路径上最差的一段，让上层能感知"这次转换是有损的"。

**实现方案**
- `service/relayconvert/request_registry.go` / `response_registry.go`：转换器注册表 + 路由查找 + 多跳展开（`expandRequestConverterSteps`）。核心类型 `RequestConverterSpec{From, To, Quality, Via[]}`。
- `service/relayconvert/internal/<from>/to_<to>_*.go`：42 个文件，每个只负责一个方向的一个环节。请求 / 响应 / 流式响应分三个文件，避免流式逻辑污染非流式。
- `service/relayconvert/internal/shared/`：跨协议共享的子问题单独抽出 —— Claude 的 prompt cache 标记、tool_choice 语义映射、Gemini 的 JSON Schema 方言差异。
- `request_compat.go` / `response_compat.go`：对外稳定 API 门面，内部重构不影响调用方。

**可借鉴点**：多跳 + 质量标注让"新增一种协议"从写 2N 个转换器降到写 2 个，代价是链路变长时质量可感知地下降 —— 用标注把代价显式化，而不是藏起来。

---

## 2. 重试短路网关

**介绍**
长耗时非流式请求上，客户端 SDK 到了默认超时（常见 600s）会掐线重试。但上游那边请求还在跑、跑完照样计费。官方 SDK 自动重试同一请求 → 又一次全额计费 → 又超时 → 再重试。一次调用能烧掉数倍的钱，用户还拿不到任何结果。

**原理**
关键洞察：**官方 SDK 对 4xx 不自动重试，对 5xx/超时才重试**。所以只要在重放到达时返回 400，循环立刻终止。做法是给"刚被客户端主动取消的请求"打指纹，短 TTL 内同指纹再来就直接 400 拒绝，不进 relay、不选渠道、零计费。

**实现方案**
- 指纹 = `sha256(tokenID | 模型名 | 原始请求体)`。请求体必须取**原始 body**，不能取解析后的结构体 —— 序列化顺序不稳定会让指纹漂移。
- 存储走混合缓存（`pkg/cachex.HybridCache`）：有 Redis 用 Redis（多实例共享），否则退进程内。
- 写入点在失败收尾。判定"是客户端取消"必须用 `baseReqCtx` 而非当前请求 ctx —— 后者在网关内部超时时也会 done，会误判。
- 读取点在 `controller.Relay` 最入口，**命中只读不续期**：客户端调大超时或改流式后不会被永久锁死。
- 默认关闭，后台可调 TTL（`setting/operation_setting/retry_short_circuit_setting.go`）。

---

## 3. 渠道健康度自动降级

**介绍**
渠道会慢慢坏：先偶发 5xx，然后错误率上升，最后彻底不可用。上游只有"连续失败就禁用"这种一刀切，禁用后要人工恢复。实际需要的是**渐进降级 + 自动恢复**。

**原理**
由真实用户请求驱动的**被动状态机**。不额外发探测请求，每次真实请求的结果就是一次采样。信号分三类：成功 / 失败 / 单 key 失效。输出的不是"禁用与否"这个布尔量，而是调整渠道的 `Priority` 和 `Weight` —— 让坏渠道**先少接流量**而不是直接下线。连续成功累积到阈值再逐级恢复。

**实现方案**
- `service/channel_health.go`，入口 `RecordChannelResult(channelId, usingKey, err, ttftMs)`。
- 三组连续计数器落 Redis：`channel:err_streak:{id}` / `ok_streak` / `lat_streak`（TTFT 超阈值也算一种坏）。
- **降级冷却** `channel:demote_cooldown:{id}`：一次降级后进冷却期，防止一波瞬时故障把渠道连降到底。
- **分布式锁** `channel:health_lock:{id}`：多实例同时观测到失败时只有一个执行降级，避免叠加。
- 错误分类是关键：`isCountableError`（哪些错算渠道的）/ `isTransientOverloadError`（429 过载不重罚）/ `isKeyFatalError`（key 失效该摘 key 而不是废掉整个渠道）。
- 通知去重 + `notify_on_upgrade` 开关：间歇性故障渠道会反复启用/禁用，不去重会刷屏告警。
- Redis 不可用退进程内 `sync.Map`，计数偏低 —— 刻意选偏低方向，**漏降级好过误降级**。

---

## 4. 渠道多 base_url 故障切换

**介绍**
不少上游给同一渠道提供多个等价 relay 域名（不同区域节点）。这些节点会**轮流劣化**：不是硬故障，而是响应时间从 2s 飘到 500s。人工发现再切 base_url，中间已积累大量超时。

**原理**
把多个 base_url 当成小型负载均衡池，按**两个维度**同时择优：
- **稳**：连续失败熔断 + 冷却自愈（半开试探）
- **快**：TTFB 的指数移动平均（EWMA）

两个坑必须处理：**横跳**（两节点速度接近时来回切）用迟滞解决 —— 当前节点只有明显劣于最优才切；**信息陈旧**（一直不选就永远不知道恢复没）用保底探索解决 —— 超过一定时间没采样的节点强制试一次。

**实现方案**
- `service/url_health.go`：`urlStat{consecFails, openUntil, ewmaTTFB, sampleCount, lastSampledAt}`，`channelId → url` 二级 map。
- `PickURLForChannel`：跳过熔断中的 → 优先久未采样的 → 否则取 EWMA 最低 + 迟滞。**全部熔断时挑冷却最早结束的做半开试探**，保证不会全线拒绝。
- 刻意**不做请求内换 URL**：单次请求遇坏节点仍走渠道级重试，冷却只影响后续请求。对 relay 热路径零侵入。
- 熔断阈值 / 冷却 / EWMA α / 迟滞 / 探索间隔全部后台可调。

---

## 5. 渠道容量感知与限速排队

**介绍**
`priority` + `weight` 的加权轮询不感知**当前在途请求数**。一个渠道即使打满，只要权重高就继续灌，结果它开始吐 429，而旁边空闲的低权重渠道没人用。

**原理**
给每个渠道维护**滑动窗口内的请求计数**，选渠道时作为额外排除条件。计数用分钟切桶 + 加权估算：
```
最近60秒 ≈ 当前桶 + 上一桶 × ((60 - 当前桶已过秒数) / 60)
```
比精确滑动窗口便宜得多（两次 GET 而非一个 sorted set），精度对限流足够。

**实现方案**
- `common/rpm_realtime.go`：`rpm_min:{u|c}:{id}:{minute}`，INCR + EXPIRE(130s)。用户维度和渠道维度共用一套。
- `common/channel_capacity_store.go` + `service/channel_capacity.go`：存储层与业务层拆开。**拆分原因值得注意** —— 选渠道逻辑在 `model` 层，而 `model → service` 是循环依赖，所以纯存储下沉到 `common`。
- **TTL 陷阱**：per-channel 窗口可能小于全局窗口，桶会过早 EXPIRE 导致读取低估。写入时取两者最大值作 TTL。
- **fail mode 可配**：Redis 故障时 `fail_open`（放行，可能超卖）还是 `fail_closed`（拒绝，保守）由后台决定。
- 批量读用 MGET，选渠道时一次拿全部候选的计数。

---

## 6. 渠道级全满策略（软桶 / 硬桶）

**介绍**
容量满了之后怎么办？直接 503 会让客户端以为整个服务挂了，但无脑排队又可能堆积。而且不同渠道该有不同策略：便宜的备用渠道满了应该排队等，昂贵的应急渠道满了应该直接拒。

**原理**
把「全满策略」从全局下放到渠道级，分两种桶：
- **软桶**：满了不算出局，仍留在候选层里，走排队降级（转移 → 排队 → 429）
- **硬桶**：满了直接退出候选，让路由去下一优先级

同一优先级层内**按最宽松优先合成** —— 只要有一个渠道是软桶，整层就不判 503。

**实现方案**
- `model/channel.go` 新增 `CapacityFullStrategy` 字段（空 = 继承全局），`ResolveFullStrategy` 做渠道级 > 全局的解析。
- `model/channel_cache.go` 按「最宽松优先」合成层级策略。
- 硬桶拒绝改返回 **429 + Retry-After**（原本误报 503）—— 429 语义正确且客户端 SDK 会退避重试，503 会触发换服务器。
- 修掉的根因 bug：fallback 场景下单渠道无次优先级可降，被误判为"all priority layers exhausted"报 503。

---

## 7. 用户级渠道软限速

**介绍**
需要对特定用户在特定渠道上限速，但**不希望用户察觉到这是固定额度**。硬限速会让用户很快摸清阈值，然后卡着阈值打满。

**原理**
两层伪装：
- **额度抖动**：生效 RPM = 基准 ± 随机百分比，随机种子由 `(userId, channelId, 分钟桶)` 决定 —— 同分钟内稳定（不会忽松忽紧），跨分钟变化（摸不出固定值）。
- **配额内延迟**：没超限的请求也注入随机延迟，让响应时间分布看起来像上游本身的抖动。

**实现方案**
- `middleware/soft_user_channel_rate_limit.go`，挂在选完渠道之后、`c.Next()` 之前。
- 规则表 `(user_id, channel_id) → {enabled, base_rpm, jitter_pct}`。
- **全链路 fail-open**：规则查询失败、Redis 失败、延迟计算失败一律放行并记 warn。限速功能自己出故障不该影响业务。
- 保底 `effectiveRpm >= 1`，防止 jitter 把 base=1 抖成 0 导致全拒。

---

## 8. Token 错误率熔断

**介绍**
某个用户的 API Key 在疯狂重试一个必然失败的请求（参数错、模型不存在、上游封号），每秒几十次。这些请求全打到上游，消耗渠道配额、拉低渠道健康度、污染可用率统计。

**原理**
按 token 维度统计滑动窗口错误率，超阈值冷却该 token，期间直接 429 + `Retry-After`，不进 relay。到期自动恢复。

**实现方案**
- `service/token_health.go` + `middleware/token_health.go`。
- 计数 `token:req:{id}:{min}` / `token:err:{id}:{min}` 分钟切桶；冷却 `token:cooldown:{id}`。
- **触发时清空窗口桶**：冷却结束后从全新窗口累积，否则陈旧错误会让它立刻二次熔断。
- **统计所有错误（≥400 含 5xx）**，不限于 4xx —— 早期只算 4xx 导致熔断基本不生效。
- `ExcludedStatusCodes` 可配：上游大面积故障时不想让正常 key 被连坐，把对应码排除。
- **检查路径 fail-OPEN**：功能关闭或 Redis 抖动一律放行。熔断器自己不能成为故障源。

---

## 9. prompt-cache 感知的重试策略

**介绍**
Claude 的 prompt cache 绑定在具体上游账号上。跨渠道重试虽然能救活一次请求，但会**丢掉整个缓存**，下一次请求要重新付全价建缓存。对长 system prompt 的场景，一次跨渠道重试的代价可能远高于让这次请求失败。

**原理**
把「能不能跨渠道重试」变成**可配置的两轴策略**，取最保守者生效：
- **渠道级** `Channel.retry_strategy`：inherit / cost_guard / same_domain / cross_channel，配合 `cache_domain` 标识缓存归属域
- **令牌级** `Token.relay_retry_policy`：system / disabled / cache_domain_only / allow_cross_channel

统一为 `EffectiveRetryScope = min(渠道, 令牌)` —— **显式放宽的一侧不能松开另一侧**，只能收紧。

**实现方案**
- `service.EffectiveRetryScope` 做两轴合成，有完整真值表测试。
- `NoCrossChannel`（cost_guard / disabled）→ 在 `shouldRetry` / `shouldRetryTaskRelay` 直接终止跨渠道重试。
- `SameDomain`（same_domain / cache_domain_only）→ 重试候选过滤到同 `cache_domain`，**无 fallback、不逃逸到其他账号**。
- 原始策略与域在 distributor 阶段捕获，默认 `inherit` ⇒ 零行为变更。

---

# 二、Relay 传输层正确性

## 10. 流式客户端断开不再连带取消上游

**介绍**
客户端在流式请求中途断开，旧逻辑会连带取消上游请求、扫描器立即退出。结果 `completion_tokens` 停在 0，触发零产出免单 —— **实际上游已经生成并计费了，我们却免单，直接漏计费**。

**原理**
客户端断开和上游请求是两件事。客户端走了，我们仍应把上游的流**读到底**，才能拿到真实的 usage 用于计费。

**实现方案**
- `api_request.go`：流式上游请求用 `context.WithoutCancel` 剥离客户端取消；非流式保持 deadline 管控。由 `StreamingTimeout` idle ticker 兜底防挂起。
- `stream_scanner.go`：移除扫描循环内的 `client_gone` 提前 return；主 select 断开后继续 drain，等 idle ticker 或 stopChan，拿到上游真实 usage。
- 与零产出免单的 `client_gone` 时长门槛形成闭环：正常久等照常计费，秒断刷 cache 仍免单。

---

## 11. 出站请求补 GetBody，支持 HTTP/2 透明重放

**介绍**
HTTP/2 下上游可能在 body 已写出后重置流（`REFUSED_STREAM` / `GOAWAY`）。`net/http` 本可以透明重试，但前提是请求带 `GetBody`。而流式化的 body 是类型擦除的 `io.Reader`，`GetBody` 保持 nil，重试能力直接失效，请求硬失败。

**原理**
`net/http` 只对 `*bytes.Reader` / `*bytes.Buffer` / `*strings.Reader` 自动推导 `GetBody`。要让包装过的 body 也能重放，必须自己提供一个能**重新开一份独立游标**的方法。

**实现方案**
- `BodyStorage` 新增 `NewReader()`：内存态在同一份不可变数组上新建 `bytes.Reader`；磁盘态对同一缓存文件另开 fd。两者都是零拷贝且游标独立。
- 副本 `Close` 只释放副本；storage 关闭后返回 `ErrStorageClosed` 而不交出失效 reader；unlink 与 open 共用一把锁，已打开的 fd 由 inode 保活。
- `ReaderOnly` 签名改为 `BodyStorage -> ReplayableBody`，9 个出站调用点无需改动即获得重放能力，**传非 storage 直接编译失败**（fail fast）。
- 删掉手写的 `GetBody`：它返回同一个已消费的 reader，任何重放都会静默发出空 body，还会覆盖 `net/http` 推导出的正确快照。**不可重放的 body 保持 GetBody 为 nil，让重试直接失败，而不是发出被破坏的请求。**

**可借鉴点**：这类 bug 的危害在于「静默」—— 重放发出空 body，上游返回一个语义正常但内容错误的响应，没有任何报错。

---

## 12. 流式 scanner 统一缓冲上限

**介绍**
`bufio.NewScanner` 默认单行上限 64KB。上游返回超长 SSE 行（大 base64 图、长 thinking 块）时报 `token too long`，表现为**真实断流** —— 用户看到响应突然截断。

**原理**
所有流式处理器必须走统一的、带可配置缓冲上限的 scanner 封装，而不是各自 `bufio.NewScanner`。

**实现方案**
- 导出既有的 `NewStreamScanner` 封装，统一 8 处渠道流式处理器的调用。
- 复用已有的 `InitialScannerBufferSize` / `getScannerBufferSize`（跟随 `STREAM_SCANNER_MAX_BUFFER_MB`），**不新增第二套常量**。
- 某处硬编码 64MB 绕开配置的也一并收编。
- 补 `scanner.Err()` 日志 —— 原本吞掉的正是这个错误。
- 回归测试：1MB 单行在裸 scanner 下 `token too long`，改后正常读出。

---

## 13. 连接池空闲连接过期

**介绍**
长连接池里的空闲连接可能已被上游或中间设备单方面关闭，复用时才发现，表现为随机的连接重置。

**实现方案**
`common/relay_idle_conn_timeout.go` 给出站 Transport 配置空闲连接超时，让池主动淘汰陈旧连接，而不是等复用时踩雷。配合 Ali 轮询等场景统一走共享 client，避免每次新建 Transport 造成连接泄漏。

---

## 14. zstd 请求体解压

**介绍**
客户端为省带宽用 zstd 压缩请求体（大 prompt 场景收益明显）。网关此前只认 gzip / deflate，zstd 请求会按原始字节去解析，直接失败。

**实现方案**
- `middleware/gzip.go` 增加 `zstd` 分支，与 gzip / deflate 共用 `wrapMaxBytes` 大小上限；三种编码都在解压成功后才 `Header.Del("Content-Encoding")`，未识别的编码原样透传。
- **`zstd.WithDecoderConcurrency(1)` 是必需的，不是调优**：`zstd.NewReader` 默认并发度取 GOMAXPROCS，会 spawn 后台 goroutine 阻塞在一个没人 drain 的 output channel 上，只有 `Close()` 能释放。而这个中间件注册在 TokenAuth **之前** —— 被鉴权拒掉的请求永远走不到 `common.GetRequestBody`（唯一会关闭 body 的调用方），goroutine 就永久泄漏。实测每个被拒请求泄 3 个，而压缩载荷只要约 150 字节、解压后超过 1MB 就能触发。同步解码不留这类后台状态。

**可借鉴点**：任何注册在鉴权之前的中间件，都要假设"请求可能在下游 close 之前就被中止"。带后台 goroutine 的解码器放在这个位置尤其危险。

---

## 15. 流式超时配置的非正值兜底

**介绍**
`STREAMING_TIMEOUT=0`。运维把超时设成 0 表示"禁用"是很常见的直觉，实际结果是每个流式请求都 panic。

**原理**
`Atoi("0")` 不报错，`GetEnvOrDefault` 因此不走默认分支，0 一路传到 `time.NewTicker(0)` 与 `ticker.Reset(0)` —— 这两个对非正值都直接 panic。同一个函数里 `pingInterval` 有 `<= 0` 兜底，`streamingTimeout` 没有，属明显疏漏。

**实现方案**
- `relay/helper/stream_scanner.go`：在源头把非正值回退到 `DefaultStreamingTimeout = 300s`（与 `common/init.go` 的默认值一致），`NewTicker` 与 `Reset` 两处同时受益。

**可借鉴点**：这个缺陷还有个影子——`relay/helper` 测试包长期有约 1/10 概率的并发 flaky，一直被当成测试夹具问题。真相是测试二进制不跑 `common/init.go` 的 env 加载，全局初值就是 0，任一并行用例的 Cleanup 把它还原成 0，其它正在跑的用例读到 0 就 panic。**改测试只能掩盖，改生产才是根因**。反复出现的 flaky 值得先怀疑是生产缺陷在测试环境下的提前暴露。

---

## 16. 上游请求参数与工具调用的透传保真

**介绍**
一组同类缺陷：客户端传了参数，网关在协议转换途中把它弄丢了，全程无报错。丢参数比报错更难查——上游按默认值执行，结果"看起来正常但不对"。四处修复均带反证测试（藏掉修复即复现）。

**实现方案**
- **AWS Claude 显式零值被 omitempty 吞掉**：`AwsClaudeRequest` 的 `MaxTokens` / `TopP` / `TopK` 是值类型 + `omitempty`，用户显式传 `top_p:0` / `top_k:0` 时字段整个消失，上游收不到该约束。改为指针后零值可表达、缺省仍省略。这个 struct 是整条链上唯一把指针语义降级回值语义的一环（`dto.ClaudeRequest` 早已是指针）。
- **Qwen `thinking_budget` 在解析阶段就没了**：dto 无该字段，客户端在请求顶层传的参数在 JSON→struct 阶段即丢失；ali 转换链是 struct 重建而非透传，再无从捞回；`ExtraBody` 路子不通（只捕获字面 `extra_body` 键，而 DashScope 惯例是平铺在顶层）。补字段之后还要**两道闸**：`GeneralOpenAIRequest` 是全仓复用结构，加字段意味着客户端往任何渠道传该参数都会被原样转发，从"静默丢弃"变成"上游 400 未知参数"，所以值接收者 `MarshalJSON` 对非千问系模型序列化时抹掉该字段；ali 层再按 `UpstreamModelName` 闸一道，因为 `MarshalJSON` 只看得到 `request.Model`，拦不住"客户端模型是 qwen、渠道映射到非 qwen 上游"。
- **Ollama 非流式响应丢 tool_calls**：聚合循环只写 thinking 与 content，从不读 `ck.Message.ToolCalls`，非流式请求的工具调用整条丢失且 `finish_reason` 停在 `stop`。抽出 `ollamaToolCallsToOpenAI` 供流式 / 非流式共用，有工具调用时 `finish_reason` 翻为 `tool_calls`。额外跳过空 `function.name` 的工具：空名会让下游 OpenAI→Claude 转换产出无 name 键的 `tool_use`，被 Anthropic 按 `^[a-zA-Z0-9_-]{1,128}$` 判整个请求 400。
- **OpenAI→Claude 转换注入空 tools / 丢无参工具**：无工具的请求也带 `"tools":[]` 发给上游；无参工具归一化为空 schema（新增 `service/relayconvert/internal/shared/claude/schema.go`，responses 侧收敛到同一实现）。顺带消除 `params["type"].(string)` 缺 ok 断言在非字符串 type 上的 panic。

**可借鉴点**：给全仓复用的请求结构体加字段，等于给**所有**渠道加了一条透传路径。要么在序列化层按模型闸住，要么准备好接受"某些上游收到不认识的参数直接 400"。而只闸一处是不够的——序列化层看得到客户端传的模型名，看不到渠道映射之后的上游模型名。

---

# 三、观测与度量

## 17. 错误归因分类

**介绍**
计算「渠道可用率」时，一次失败到底算不算上游的锅？客户端传了非法参数、用户余额不足、网关自己数据库挂了 —— 这些都会产生失败日志，但都不该计入"上游是否可用"。

**原理**
把 relay 错误分成三类，只有上游侧的进可用率分母：
- `AttributionUpstream` 上游故障 → 计入分母，记失败
- `AttributionClient` 客户端自己的错（参数非法 / 余额不足 / 无权限 / 敏感词）→ 完全不计入
- `AttributionGateway` 网关自身故障（DB / 序列化 / 计价配置）→ 完全不计入，避免自家 bug 污染上游可用性语义

**实现方案**
- `types/error_attribution.go`，`ClassifyErrorAttribution(*NewAPIError) ErrorAttribution`。
- 401/403/429 归入上游：严格说是配置或配额问题而非"进程挂了"，但从"这个分组现在能不能用"的角度结果一致。
- 404 要看来源：`ErrorCodeBadResponseStatusCode` 下的 404 是上游把模型下线了（算上游）；网关侧"本站没配这个模型"走 `ErrorCodeModelNotFound`（算客户端）。
- **刻意不复用自动禁用的判定逻辑**：那套依赖运行时可配的状态码区间和关键词表，管理员改一次配置就会让指标口径漂移、历史数据前后不可比。**指标语义必须写死在代码里。**

---

## 18. 渠道与分组可用率

**介绍**
两个面向不同受众的可用率视图：渠道列表给管理员看单渠道真实健康度；分组广场给用户看"这个分组的请求成功率"。同一份日志，取样范围必须不同。

**原理**
- **渠道页**：不论渠道当前启用还是被自动禁用都统计 —— 被禁用的渠道仍在被定时测活，曲线要能反映它恢复没有。
- **分组页**：按**样本发生时刻是否可被路由**归属，而不是按渠道当前启停状态回溯。三类样本三种处理：
  1. 真实流量 → 按 abilities 全量映射计入（真实请求只会路由到当时启用的渠道，样本天然自选择）
  2. 启用态测活 → 按当前 enabled 映射计入，填补低流量分组的空窗
  3. **禁用期探活 → 永不计入**

第 3 条是踩过的坑：被探活反复禁停/启用的"翻转渠道"，在自动启用的瞬间会把禁用期攒下的失败**整窗带回**分组曲线，曲线随刷新时机忽红忽绿。实测有单渠道一天翻转 128 次。

**实现方案**
- `service/channel_uptime.go`（渠道）/ `service/group_uptime.go`（分组）。
- 禁用期探活靠**写时打标**识别：探活日志 `token_name` 写成 `模型测试-停用`，读时排除。写时打标比读时回溯可靠 —— 读时无法还原"当时渠道是什么状态"。
- **不按 `logs.group` 聚合**：渠道测试构造的假请求带的是 root 用户的分组，直接聚合会把全部测试数据堆到 default 上。改为按 `channel_id` 聚合，再经 abilities 的 `(group, channel)` 映射归属，一个渠道服务多分组时各计一次。
- 大表防护：结果按 `(hours, tz)` 进程内缓存 5 分钟，miss 时**持锁查库、并发请求排队复用同一次结果**（互斥即单飞）。
- 归属边界要说清楚：abilities 是"当前映射表"不是历史表。启停只翻 enabled 位不删行，所以启停维度历史归属稳定；改分组会删旧行重建、删渠道会删行 —— 这两种下历史样本会追溯改挂或失联。

---

## 19. 渠道自动验收测评流水线

**介绍**
新接一个上游渠道，怎么确认它是真直连还是套娃转发、模型是否真实、稳定性如何？人工测一次要半小时，且渠道质量会随时间漂移，需要定期复验。

**原理**
把验收做成**可调度、可排队、可中断**的异步任务：管理员触发或定时触发 → 进队列 → 调用外部测评网关跑多维探针 → 落库成报告 → 异常时通知。

**实现方案**
- `service/channel_verify_queue.go`：信号量控制并发。单次测评 30~120s，测评网关侧有 IP 限流，不限并发必被 429。`VerifyQueueAcquire` 返回排队位置给前端展示。
- `model/channel_verify_report.go`：报告落库支持历史对比 —— **渠道定性有时效性会漂移**，同一渠道两次验收结论可能不同，必须留档才能发现。
- `controller/channel_verify_schedule.go`：定时调度 + 阈值联动（可配置为验收不通过自动停用渠道）。
- `RegisterVerifyCancel` / `CancelVerify`：长任务可中断，避免管理员误触发后只能干等。

---

## 20. 大表查询优化系列

**介绍**
调用日志是网关里增长最快的表，很容易到亿级、上百 GB。几乎所有"页面打不开"的投诉最后都落到这张表上。

**原理与实现（四类问题）**

**（1）复合游标翻页**
CSV 导出按 `id` 翻页时，`created_at` 只是过滤条件而非定位条件。窗口内数据取完后会**倒扫整条渠道历史**凑批，导致某批查询卡死、下载中途停住。改为按 `(created_at desc, id desc)` 复合游标翻页，让 `created_at` 成为可定位范围，命中前导索引，扫描限定在时间窗口内。

**（2）索引列序**
`idx_created_at_id` 反建成 `(id, created_at)` 时，按 `created_at desc, id desc` 排序会踩掉主键早停优化，大窗口直接全表扫（实测 400s+）。索引列序必须与排序列序一致。修复方案是**启动时自愈重建**：检测到列序错误的索引自动在线 DDL 重建，让无 shell 的 CI 部署节点也能自动修复。

**（3）扫描边界**
渠道质量统计、用户日志计数、退款搜索这类聚合，若不加时间边界会随数据量线性劣化。统一给这类查询加 bounded 时间窗口和 LIMIT。

**（4）SQL 层聚合**
缓存 token 的对账原先是取出行再在应用层累加，数据量大时内存和传输都是瓶颈。改为在 SQL 里 `SUM` 聚合。

**踩坑提醒**：在线 DDL 等 MDL 锁时，**全站对该表的写入会公平排队停摆**。执行前查 `metadata_locks`，杀掉持有 GRANTED 锁的长事务。

---

# 四、计费与资金安全

## 21. 钱包预扣凭据与清扫

**介绍**
按量计费都要预扣：请求开始时按预估扣钱，结束后按实际用量多退少补。问题出在**退款失败** —— 如果退款只在内存里做异步补偿，那一刻数据库不可用，这笔钱就永久消失了，且没有任何记录能查出来。

**原理**
**先落凭据，再动余额**。预扣扣减 `users.quota` 的**同一事务内**写入一条预扣记录，所以"钱已被扣走"这件事永远有落库证据。退款只做两件事：余额加回、状态置 `refunded`。若退款那刻 DB 不可用，记录停留在 `reserved`，由后台清扫任务在 DB 恢复后完成退款。

**实现方案**
- `model/wallet_preconsume_record.go`：`{RequestId(唯一键), UserId, TokenId, PreConsumed, Status}`，状态 `reserved → settled | refunded`。
- **CAS 抢占**：`claimWalletPreConsumeTx` 用条件更新抢占凭据，抢到才动余额。**顺序不能反** —— 先动余额再改状态的话，清扫任务和迟到的正常结算会各退一次钱。
- `service/wallet_reservation_sweeper.go`：每 5 分钟扫一批（200 条）。
- **阈值必须显著大于单请求最长生命周期**：relay 链路超时 6000s，清扫阈值设 2 小时，否则会把仍在进行中的长请求误判为遗留并提前退款。

**可借鉴点**：资金路径通用法则 —— **先 CAS 抢占凭据，再动余额**。任何"幂等分支直接 return 但上面已经动过钱"的写法都是双退 bug。

---

## 22. 资金路径锁序统一与信任旁路

**介绍**
`users.quota` 是典型的单行热点。一个高频用户能把整个数据库拖停，表现为 **CPU 内存都闲但全站卡死**。实测出现过 800 事务的锁车队。

**原理（两件事）**

**（1）锁序统一消除 AB-BA 死锁**
预扣路径是「先扣 users → 再写凭据」，而结算/清扫路径是「先 claim 凭据 → 再动 users」。两条路径对同样两个资源反序加锁，并发时必然 AB-BA 死锁。把预扣也改成 **凭据 → users → tokens**，全链路锁序一致。

**（2）信任旁路免除预扣**
预扣本身是为了防止余额透支。但对余额远高于单次消耗的用户，预扣毫无意义却制造了热点写。所以：余额扣除本次预估后仍不低于阈值、且令牌为无限额度或剩余同样充足时，**跳过预扣**（不写 users 行、不落凭据），结算时走批量聚合。

**实现方案**
- `service/billing_session.go`：`walletTrustThresholdQuota()` 读运行时选项 `WalletTrustQuotaUsd`（默认 0 = 关闭）。旁路准入要同时满足：钱包计费、余额充足、令牌额度充足（对齐上游 `shouldTrust` 语义）、非 `ForcePreConsume`、非 Playground。
- `model/user.go` 的 `TrustedSettleUserQuota`：旁路结算走 `BatchUpdateTypeUserQuota` 批量通道，每用户一个 flush 周期聚合成单条 UPDATE。
- **无 `quota >= X` 守卫且 Unscoped 穿透软删** —— 债务必须落账，允许授信透支。这是刻意的：旁路已经放行了请求，结算时再拒绝记账等于白送。
- 有**锁序 SQL 顺序回归测试**：还原成旧序必须 FAIL（已验证），防止后人改回去。

---

## 23. claim 死锁修复（PK-first 访问路径）

**介绍**
按 `request_id + status` 直接 UPDATE 时，MySQL 优化器可能选 `status` 索引扫描，与走唯一索引的并发 claim 对 PK / status 索引**反序加锁**。上线后实测约 0.5 次/分钟的死锁，后果是结算失败、凭据漏收。

**原理**
同一张表的所有写路径必须走**同一个加锁顺序**。优化器选哪条索引不受控，所以不能依赖"我以为它会走唯一索引"。

**实现方案**
改为两步：唯一键点查拿 `id` → 按主键 CAS 更新。本表所有写路径锁序统一为 **PK → 二级索引**；清扫任务改用已持有的主键。语义不变。

**可借鉴点**：`UPDATE ... WHERE 二级索引条件` 的加锁顺序是优化器决定的，高并发下不可控。**先查主键再按主键更新**，多一次点查换确定的锁序。

---

## 24. 零产出免单

**介绍**
上游收了 token 但一个字没吐（首帧都没到，或首帧到了但内容为空）。这种请求按 prompt token 照常计费，用户会觉得被白扣钱。但一刀切免单又会被滥用——"秒断刷缓存"：故意发起请求建好 prompt cache 就立刻断开，缓存留在上游，下次享受折扣却不付这次的钱。

**原理**
`completion_tokens == 0` 是免单的必要条件，但要叠三层护栏防误退和滥用：
1. **白名单而非黑名单**的 RelayFormat 准入
2. 排除非 completion 计费组件
3. `client_gone` 场景额外要求**请求已持续 ≥ 阈值**

护栏之外还有一层**策略性拒退**：本可免单、但上游是被客户内容触发风控而显式拒绝的，按输入照常计费并落审计标记（见下）。

**实现方案**
- `service/text_quota.go` 的 `shouldRefundNoOutput`。
- **白名单设计的理由**：只有 OpenAI / Claude / Gemini / Responses / ResponsesCompaction 五种格式享受免单。用白名单保证**未来新增 RelayFormat 默认不免单，要 opt-in** —— 避免"加了新模式忘了加排除"导致误退。故意不收录 audio / image / realtime / rerank / embedding，这些不按 completion 计费，`completion=0` 是常态不是异常。
- **用 `RelayFormat` 而非 `RelayMode` 判定**：前者在 `GenRelayInfo*` 构造时显式设值；后者依赖路径匹配，未匹配会落 Unknown 桶。
- 混合计费护栏：白名单格式内嵌了 `image_generation_call` / `web_search` 调用时，有非 completion 的计费组件，不免单。
- **`client_gone` 时长门槛**（默认 60s，可配）：正常久等的冤案 `use_time` 普遍远超此值，秒断刷 cache 必然极短。其它结束原因（EOF / 超时）不受时长限制。应用 shutdown 不算客户免单。
- 是否真有首帧记录到审计字段 `had_first_chunk`，便于事后复盘。
- `model/anomaly.go` 提供免单请求的查询视图，管理员可核对。
- **上游显式拒绝不免单**（`refund_no_output_exclude_upstream_refusal`，默认关）：客户内容触发上游风控被拒——Claude `stop_reason=refusal` / OpenAI `finish_reason=content_filter` / Gemini 安全类 blockReason 与 finishReason——上游确实做了工作也确实收了输入 token，按输入照常计费并落 `refund_denied_reason` 审计。
  - 取值协议集中在 `constant/reject_reason.go`：写点在 relay 适配层（claude / openai / gemini），消费点在 `service.isUpstreamRefusalReject`，两端引用同一份常量，禁止裸字面量。
  - Gemini 侧**只认安全类枚举**（SAFETY / PROHIBITED_CONTENT / BLOCKLIST / SPII / IMAGE_SAFETY / IMAGE_PROHIBITED_CONTENT）。刻意排除 OTHER 与 UNSPECIFIED（上游"原因不明"桶，多为链路侧异常）、RECITATION（模型复述训练语料被掐，归因在模型侧）、LANGUAGE（能力缺口不是风控）——**归因不清或属模型侧原因的，不向客户收费**。
  - 标记 first-write-wins：先到的拒绝证据不被后续弱信号（如 `gemini_empty_candidates`）覆盖；每轮渠道重试由 `controller/relay.go` 统一清空后重新累积。
  - 注意 `service/relayconvert` 里 Gemini→OpenAI 的展示层映射（含 OTHER→content_filter）是给客户端看的宽口径，与这份计费白名单语义不同，**属刻意分离，勿互相对齐**。

---

## 25. 跨组重试后按最终分组倍率结算

**介绍**
分层计价（tiered_expr）的计费快照在**预扣阶段冻结** `GroupRatio`。而重试选到新渠道时只刷新了 `PriceData.GroupRatioInfo`、没刷快照，导致结算沿用首轮分组的倍率。贵组 → 便宜组多收用户，便宜组 → 贵组平台亏损。

**实现方案**
在重试选到渠道后同步刷新快照倍率。选渠道失败的分支直接 break 不发请求，快照不会用于结算，所以刷新放在错误检查前。

本地实测（倍率 1.0 组必失败 → 5.0 组成功，固定 usage）：修复前扣 5250（首组倍率），修复后扣 26250（最终组倍率），**相差 5 倍**。

**可借鉴点**：任何在请求早期"冻结快照"的设计，都要检查后续路径上有没有会改变快照前提的分支。重试是最典型的一个。

---

## 26. 批量账本落库

**介绍**
每次请求都直接 UPDATE `users.quota` 会在热点用户上形成单行锁 convoy。

**原理**
把配额变更在内存里按 `(类型, id)` 聚合，定时批量 flush。同一用户一分钟内的 1000 次变更合并成一次 UPDATE。

**实现方案**
- `model/batch_updater.go`：按 `BatchUpdateType`（用户配额 / 令牌配额 / 已用配额 / 渠道已用配额 / 请求数）分桶。
- 完整状态机 `new → running → stopping → stopped`，`Stop` 时保证最后一次 flush 完成，不丢数据。
- `checkedAddInt` 做溢出检查 —— 累加型聚合必须防 int 溢出。
- `flushOperationID` 经 context 传递，让每次 flush 可追踪。
- `model/flush_operation_ledger.go`：flush 操作本身也落账本，便于对账"内存聚合了多少、实际写进去多少"。

---

## 27. 价格发布与变更通知

**介绍**
调价要通知用户。但价格配置散落在几十个 option key 里，管理员改一个字段就发一封邮件显然不行；改完一批再手工写通知又容易漏。

**原理**
**发布制**而非钩子制：管理员随便改，改完点"发布"，系统对比本次快照与上次发布的快照，自动 diff 出涨了什么、降了什么、新增删除了什么。

**实现方案**
- `model/price_publish.go`：`PricePublishBatch{Snapshot, Summary, AffectedGroups, IsBaseline, ...}`。Snapshot 存发布时全部定价 key 的完整快照，作为下次 diff 的基准。
- **不挂保存钩子** —— 核心取舍。钩子会在每次字段修改时触发，无法表达"一批调价是一个整体"。
- **断点续发**：`EmailState(none/sending/done/partial)` + `EmailCursor(已处理到的 user id)`。几万用户的邮件中途失败可从断点继续，不重发也不漏发。
- MySQL 下 Snapshot 字段要从 `text` 升级到 `mediumtext`（AutoMigrate 只能建 text，需额外迁移函数）。

---

## 28. 上游倍率监控与实付反推

**介绍**
作为中间商，成本是上游的分组倍率，收入是自己的分组倍率。上游偷偷调价你不知道，毛利就悄悄被吃掉。更麻烦的是：有些上游根本不公开倍率，只能从账单反推。

**原理**
两条路并行：
- **正向抓取**：定时拉取上游的公开定价接口，与本地配置比对，差异超阈值打角标告警。
- **反向反推**：从实际扣费和 usage 反推出"实付倍率"，与人工录入的采购价比对，偏差超阈值告警。

**实现方案**
- `service/channel_ratio_monitor.go`：按上游类型分派，各自定价接口路径不同。抓取带 15s 超时 + 10MB body 上限，防上游返回巨大响应拖死监控。
- 倍率**精确到本渠道 key 所属的上游分组**，而不是笼统按站点 —— 同一上游不同 key 可能在不同分组。
- 每小时更新，**自动禁用的渠道也纳入抓取** —— 禁用只是暂时的，价格该继续跟踪。
- 反推失败时说明原因（如 usage 缺字段、样本不足），便于管理员手动指定，而不是给一个沉默的空值。
- 倍率列排序按「分组固定倍率优先、实付兜底 0.01」，避免反推虚高干扰排序。
- **坑**：面板域名 ≠ 渠道 base_url，很多上游的管理面板和 API 端点不同域。

---

## 29. 模型计价默认值与官方调价跟进

**介绍**
默认计价表跟不上官方调价，新装站点就会按旧价收费。gpt-5.6 三个型号的默认值一度停在 2026-07-26，此后官方两次调价还改了 service_tier 的名字。

**实现方案**
- `setting/ratio_setting/model_ratio.go`：sol 5/30→4/20（08-21 促销价）、terra 2.5/15→2/12、luna 1/6→0.2/1.2；缓存读取按输入 10%，缓存写入按输入 1.25×；长上下文档输入 ×2、输出 ×1.5。
- **`priority` 于 07-30 更名 Fast mode，两个名字都要走 2×** —— 原来只判 `priority`，会让传 `fast` 的请求**少收一半**。
- 拆成两个单条件因子相乘，而不是写 `||`：前端 `splitBillingExprAndRequestRules` 的条件解析只支持 `&&` 串联的单条比较，写 `||` 前端就拆不开。

**可借鉴点**：计价 DSL 的表达式能力受**前端解析器**约束，不是后端求值器能算就行。改表达式之前先确认前端拆不拆得开。另外，服务商改 service_tier 的名字不会通知你，旧名字往往还继续可用——按名字判档位的代码要把新旧名字都覆盖上，否则少收的那一半要很久才会被发现。

---

# 五、认证与安全

## 30. Dashboard 会话控制体系

**介绍**
上游的后台登录是简单的 session cookie，没有会话列表、没法单独吊销某台设备、改密码后旧会话仍然有效。作为一个管着钱和 API Key 的后台，这个强度不够。

**原理**
一套完整的 **access token + refresh 轮换 + 可吊销会话** 体系：
- **短命 access token**（15 分钟）：JWT，无状态校验，不查库
- **长命会话**（30 天）：落库的控制面记录，可列表、可吊销
- **refresh 轮换**：每次刷新换新的 refresh secret，旧的作废
- **双版本号**：`UserAuthVersion`（用户级，改密码后全部会话失效）+ `SessionVersion`（会话级，单会话吊销）

难点在 refresh 轮换的**重放判定**：网络抖动导致客户端重发刷新请求，与攻击者拿着偷来的旧 refresh 重放，表现完全一样。

**实现方案**
- `model/auth_schema.go` 的 `UserSession`：`{SID, UserID, Version, UserAuthVersion, Status, RefreshHash, PreviousRefreshHash, PreviousValidUntil, LoginMethod, IP, UserAgent, ExpiresAt, RevokedAt, RevokedReason}`。**refresh 明文从不落库**，只存 keyed digest。
- **重放窗口**（`RefreshReplayWindow = 30s`）：保留 `PreviousRefreshHash` + `PreviousValidUntil`，旧 secret 在 30 秒宽限期内仍接受，超窗后再用旧的就判定为 `ErrUserSessionRefreshReuse` —— 这是 refresh token 被盗的强信号，触发整条会话吊销。
- `ErrUserSessionRefreshRace`：并发刷新只让一个成功，另一个明确报竞态而不是静默给出两套凭据。
- **会话缓存 + 否定围栏**：`GetUserSessionCached` 走缓存扛住每请求校验的压力；吊销时写 **deny fence**（`writeUserSessionDenyFence`），保证吊销立刻生效而不用等缓存过期。缓存条目带 schema 版本号，结构变更时自动失效。
- `confirmUserSessionActiveSnapshot`：缓存观测过期时回源确认，避免用陈旧快照放行。
- `RevokeUserSession` / `RevokeOtherUserSessions` / `RevokeAllUserSessions`：单会话 / 其他设备 / 全部，批量吊销分批 500 条。
- 硬删除用户时**清理全部认证凭据**并补缓存围栏、结清在途工作项 —— 否则删了用户但 token 还能用。

---

## 31. 一次性认证流程凭据（AuthFlow）

**介绍**
登录不是一步完成的：密码验证后要走 2FA、OAuth 回调要带状态、passkey 要挑战应答、注册要邮箱验证。每个中间态都需要一个短命凭据，如果实现得随意（比如塞进 session、或者用可预测的 ID），就是账号接管漏洞。

**原理**
把所有认证中间态统一抽象成 `AuthFlow`：一次性、带用途、带绑定、可过期、消费即失效。

**实现方案**
- `service/auth_flow.go`：`AuthFlowSpec{Purpose, Provider, Intent, UserID, SessionID, Payload, TTL}`，TTL 默认 10 分钟。
- 用途枚举严格区分：`login` / `login_2fa` / `login_passkey` / `login_oauth` / `registration`。**消费时必须校验 purpose 匹配** —— 否则一个注册流程的 token 可能被拿去完成登录。
- token 只存 hash（`hashAuthFlowToken`），明文不落库。
- 消费走事务 + 状态机，错误分得很细：`ErrAuthFlowInvalid` / `ErrAuthFlowExpired` / `ErrAuthFlowConsumed`。**已消费与不存在必须区分** —— 前者说明有人在重放。
- `ConsumeBoundAuthFlow`：绑定到具体 user/session 的消费，防止 A 的流程凭据被 B 使用。
- `ClaimExternalIdentity` + `ExternalIdentityClaim` 表：OAuth 外部身份的抢注防护，`ErrIdentityConflict` 明确拒绝"这个 GitHub 账号已绑定到另一个用户"。

---

## 32. 敏感操作二次证明（Security Proof）

**介绍**
已登录不等于可以改密码、删账号、导出全部 API Key。这些操作需要**当场再证明一次身份**，且证明不能被跨操作复用。

**原理**
签发一个短命（5 分钟）、**带 scope 和 method 绑定**的证明 token。scope 标识"这个证明是给哪类操作用的"，method 标识"用什么方式证明的"（密码 / 2FA / passkey）。验证时两者都必须匹配。

**实现方案**
- `service/auth_token.go`：`IssueSecurityProof` / `VerifySecurityProof`，`SecurityProofTTL = 5 * time.Minute`。
- 错误分为 `ErrProofScope`（scope 不匹配）和 `ErrProofMethod`（证明方式不够强）—— 后者支持"改密码必须用密码证明，不接受 passkey"这类策略。
- 与 access token 用**不同的签名密钥**（`authSigningKey(purpose)` 按用途派生），access token 不能冒充 proof。
- passkey 重置强制**角色层级检查**：低权限管理员不能通过 passkey 重置高权限账号。

---

## 33. SSRF 深度防护

**介绍**
网关有多处出站：webhook 回调、文件下载、Bark/Gotify 推送、子站配置。这些 URL 都由用户或管理员填写，是典型 SSRF 入口。常见的"解析一次 DNS 再校验 IP"存在 **TOCTOU 窗口**：校验时解析到公网 IP，实际连接时 DNS 已被改成 `169.254.169.254`（DNS rebinding）。

**原理**
校验必须下沉到**拨号那一刻**：解析出 IP 后立刻锁定该 IP 直连，不给 DNS 第二次机会。重定向每一跳都要重新校验。

**实现方案**
- `service/protected_fetch_client.go`：`ssrfProtectedRoundTripper` + `protectedFetchDialer.DialContext`，**拨号时锁定已校验 IP 直连**，消除 TOCTOU。
- `checkProtectedFetchRedirect`：重定向每跳复校验 + 10 跳上限。
- `ValidateNetworkTarget` / `ValidateResolvedIP` 抽出复用，覆盖私有段、回环、链路本地（含 IPv6）。
- **出站点分类处理**：webhook / download / bark / gotify 这类直连路径挂保护 client；video_proxy / mjproxy 走渠道代理，保留请求前一次性校验；**主 relay 转发链路不挂** —— provider 的 base_url 可以合法指向私网（自建上游）。
- `SubSiteAllowIntranet` 等选项允许显式绕过，但必须是管理员主动打开。
- 默认 `EnableSSRFProtection=true`。

**可借鉴点**：SSRF 防护的正确位置是 Dialer，不是 URL 校验函数。任何"先校验后使用"的写法都有 TOCTOU 窗口。

---

## 34. 密钥泄露三层封堵

**介绍**
API Key 和 access token 会从想不到的地方漏出去。实测发现三条独立通道，任何一条没堵都等于没堵。

**原理与实现（三层）**

**（1）SQL 日志层**
GORM 默认 logger 打印**插值后的完整 SQL**。`tokens.key` 和 `users.access_token` 都带唯一索引，一条 1062 重复键错误就会把真实密钥原样写进日志文件（生产实测出现过 `Duplicate entry 'sk-...' for key 't.k'`）。
- 开启 `ParameterizedQueries` 只参数化 SQL 字符串
- 驱动错误消息是**独立泄漏通道**，参数化管不到，需在日志 Writer 层把驱动错误收敛为错误码
- 脱敏放 **Writer 层而非包装 `logger.Interface`** —— 后者会让 GORM 的 `FileWithLineNum` 把调用点归因到包装层，丢失真实 caller
- `DEBUG=true` 时保留原文便于本地排障

**（2）错误响应层**
上游/relay 错误路径会把内部渠道信息（base URL、host、port、bearer/api key）直接返回给 API 客户端。
- `MaskSensitiveInfo` 扩展：URL host 剥离端口（`net.SplitHostPort`）、IP+端口脱敏、Bearer token 脱敏（要跑在 api_key 模式之前）
- key 模式拓宽到 `api_key` / `api_token` / `access_token` / `auth_token` / `secret_key` / `authorization`，支持 `:` `=` 和引号分隔
- mjproxy 非 200 路径脱敏原始上游 body 后再返回
- distributor 的 5 处面向客户端的 i18n 错误消息脱敏 `err.Error()`
- **渠道数字 ID 有意保留**，便于用户报障时定位

**（3）管理接口层**
- 管理员查询用户接口 redact 敏感字段（`access_token` 原本明文返回）
- 匿名请求体大小限制，防止用超大 body 探测
- 未定价模型的 RBAC 收紧，阻断普通用户越权调用
- 脱敏范围扩到 base64 / Basic 凭据
- 带**回归测试**：断言不泄露 + 断言 ID 仍保留

---

## 35. 越权与账号接管防护

**介绍**
一组独立的权限边界修复，单个看都很小，合起来是后台的准入底线。

**实现方案**
- **混合会话来源拦截**：同一请求里出现多种认证来源（cookie + header token）时明确拒绝，防止低权限凭据混进高权限上下文。
- **trusted origins 强制**：会话类请求校验 Origin 白名单。**多域反代同一后端时，白名单必须枚举全部入口域名** —— 漏配某个入口会导致该入口全量登录/注册被拦，且 curl 带 Origin 测试测不出来（要用真实浏览器 fetch）。
- **只读 token 禁用拦截**：只读令牌不能执行写操作。
- **access token 与邀请额度转账加用户级限流**：防止暴力枚举和刷邀请奖励。
- **secure cookie 配置**：SameSite / Secure / HttpOnly 按部署形态自适应。
- **用户已注销时返回 401 而非 500**：注销用户的请求原本走到空指针，既是体验问题也是信息泄露（500 堆栈）。

---

## 36. 管理员细粒度权限

**介绍**
上线前只有"管理员"一档，一个管理员能做的事等于其他所有管理员。实际需要的是：这个人只看日志，那个人能充值但不能碰渠道。

**原理**
落库 `users.admin_perms` 逗号分隔字符串，六个权限位：`channel.view` / `channel.edit` / `log.view` / `quota.grant` / `user.manage` / `quota.deduct_self`。三态：

| 存储值 | 含义 |
|---|---|
| `""` | 未配置 —— 按 `defaultAdminPerms` 处理 |
| `"none"` | 超级管理员显式收走了全部权限 |
| `"channel.view,log.view"` | 显式授予的子集 |

**用 `"none"` 而不是空串表示"显式无权限"**：这样存量管理员升级后不掉权限，同时不需要一次性数据回填——多节点同时启动时回填会和 root 的配置抢写。

**实现方案**
- `model/user_admin_perm.go` 定义权限位与判定；`middleware/admin_perm.go` 的 `RequireAdminPerm(perms...)` 挂在 `AdminAuth()` 之后，命中任意一个即放行，root 恒通过。
- **权限现读库、不走用户缓存**：收回权限必须立刻生效，而管理端接口本身低频。角色则不从这里取——鉴权中间件已经算过一次，避免同一请求里出现两个角色来源。
- **`channel.edit` 是唯一一处"默认值不等于上线前行为"**：上线前任何管理员都能建/改渠道，而改渠道就能把 `base_url` 指到自己机器上把上游 key 骗出来。所以这一项收成默认关，须由超级管理员逐个显式开；其余四项的默认值严格等于上线前行为。
- `quota.grant` 对非 root 只能**增加**额度，不能扣减或覆盖。
- `quota.deduct_self` 是**计费行为开关而不是访问权限**：开了之后该管理员给用户充的额度从他自己账户划走，不足则整笔拒绝，两侧各记一条账。正因为不是访问权限，root 也不豁免。
- 前端 `web/default/src/lib/admin-perms.ts` 据此隐藏入口，但后端每个接口自己会再校验一次——前端这层只负责别让人点到 403。

**可借鉴点**：给已上线系统加权限位，默认值必须等于"加权限位之前的行为"，否则升级即掉权限。唯一该破例的是那些**本来就不该默认开**的高危能力，且破例要把理由显式写在默认值旁边。

---

# 六、平台工程

## 37. 日志迁移到 ClickHouse

**介绍**
日志表到亿级后，MySQL 上做任何时间窗口聚合都会全表扫，日志页直接打不开。

**原理**
日志是典型的**只追加、按时间查询、要 TTL 过期**的数据，正是列存的主场。做成可选后端：DSN 是 ClickHouse 就走 ClickHouse，否则维持 MySQL，业务代码不感知。

**实现方案**
- `internal/logmigration/clickhouse.go`：`IsClickHouseDSN` 识别 → `EnsureClickHouseLogSchema` 建表/校验。
- **schema 自校验**：检查必需列与排序键；`ValidateClickHouseLogTTL` / `syncClickHouseLogTTL` 保证 TTL 与配置一致，改了保留天数自动 ALTER。
- `backfill.go`：存量回填，支持断点。`state_lock_unix.go` / `state_lock_windows.go` 做跨平台文件锁，防止两个迁移进程同时跑。
- `restore.go` + `cmd/log-restore/`：回滚路径。**迁移工具必须有回滚**，否则没人敢在生产上按执行。
- 回填与校验之间要串行化（`serialize verify-only against concurrent backfill`），否则校验会读到写了一半的状态。
- `cmd/log-migrate/` 独立二进制，不占主服务进程。

**可借鉴点**：迁移工具的测试源库必须与生产同方言。用 SQLite 当源库测试会把保留字、JSON 函数、布尔类型全测歪，到生产才发现。

---

## 38. 敏感词监控与异步审计

**介绍**
内容合规检查如果放在 relay 热路径上同步做，正则匹配会直接抬高每个请求的延迟；命中后要落库存证，原文可能很大。

**原理**
热路径只做**最小必要拦截**，完整审计异步做。规则预编译并缓存，普通词预存 lowercase 避免热路径重复 `ToLower`。命中存证时正文写独立 dump 文件，DB 只留左右各 60 字符的摘要。

**实现方案**
- `service/sensitive_monitor.go`：`CompiledSensitiveWord{Pattern, LowerPattern, IsRegex, Regex, Action}` 预编译缓存，同步冷备与异步 worker 共用一份。
- **守护范围收窄**：只扫 LLM 文本类的 JSON 路径，不做全 body 正则。
- `service/sensitive_audit.go` + `model/sensitive_audit_job.go`：异步审计任务队列。
- `service/sensitive_dump_cleaner.go`：dump 文件生命周期管理，定期清理过期存证。
- 踩过的坑：命中拦截时若把日志正文降级成文本会丢失结构，排障时无法还原原始请求形态。

---

## 39. 优雅关闭与长连接排空

**介绍**
部署时直接 kill 会掐断进行中的请求。`net/http` 的 `Shutdown` 能排空普通请求，但**排不掉 SSE 流式响应和 hijack 后的 WebSocket** —— 这两类可能持续几十分钟，等它们自然结束等于无限期等待。

**原理**
自己维护在途请求表，区分普通请求和长连接。关闭分两阶段：先停止接受新请求，等普通请求排空；超时后主动 cancel 长连接的 context。

**实现方案**
- `pkg/httplifecycle/manager.go`：`Manager{ready, longLivedCanceled, active map[uint64]*requestState}`，每个 `requestState` 带 `cancel` 和 `streaming` 标记。
- `ErrDraining`：排空期间新请求直接拒绝，让上游负载均衡把流量转走。
- `pkg/backgroundtask/group.go`：后台任务统一生命周期，关闭时一并等待。
- `shutdown_resources.go`：DB 连接池、Redis、批量账本 flush 的关闭顺序编排 —— **账本 flush 必须在 DB 关闭之前**，否则内存里聚合的配额变更全部丢失。
- 同样要处理的还有：dashboard 配额缓存 flush、邀请奖励生命周期这类"半完成状态"，关闭时必须结清而不是直接丢弃。

**可借鉴点**：排空的自然收敛没有上界。实践做法是"封口在前" —— 先停止接受新请求，此时排空时长就等于中断时长，可预期；边排空边放新请求进来可能永远排不完。

---

## 40. 邮件出站全局限速

**介绍**
验证码、告警、调价通知这些邮件是突发的。一次调价通知几万封、一次故障告警瞬间几百封，很容易撞上 SMTP 服务商的频率上限，结果**整批被拒**——包括夹在里面的用户验证码。

**原理**
在 `SendEmail` 这个唯一出口做进程内滑动窗口限速，把突发削平。

**实现方案**
- `common/email-rate-limit.go`：默认 30 封 / 60 秒，`EMAIL_SEND_RATE_LIMIT_*` 可调或关闭。
- 放在最底层的发送函数而不是各个调用点 —— 调用点会不断新增，限速必须在收口处。
- 与价格通知的断点续发配合：限速导致的排队不会丢邮件，只是变慢。

---

## 41. 异常监控与运行时大盘

**介绍**
计费出问题往往是静默的：某个模型的免单率突然飙升、某渠道的 usage 全为 0、某用户的消耗曲线断崖。等用户投诉时已经损失了。

**实现方案**
- `model/anomaly.go` + `controller/anomaly.go`：异常场景的查询视图，包括零产出免单列表（区分"首帧到达但无内容"和"完全无返回"两种）。
- `service/runtime_metrics.go` / `service/user_metrics.go`：运行时指标采集。
- **RPM 只统计成功请求**：把错误请求算进去会让故障渠道的 RPM 虚高，看起来"很忙"实际全在报错。
- 大盘数据源一致性：同一个指标在渠道页、用户页、大盘页必须走同一个取数函数，否则三个页面三个数字。

---

# 七、其他自研模块（未展开）

| 模块 | 说明 |
|---|---|
| `service/channel_quality_history.go`、`model/log_channel_quality.go` | 渠道质量历史留档、评分明细快照、hover 查看原始指标 |
| `model/task_refund.go`、`task_refund_reconciliation.go` | 异步任务（视频/生图）退款与对账 |
| `service/tool_billing.go`、`pkg/billinglifecycle/coordinator.go` | 工具调用计费与计费生命周期编排 |
| `model/affiliate_commission.go` | 分佣结算（含"有效用户才发奖励"的准入） |
| `service/openaicompat/` | Chat ↔ Responses 策略层，按渠道/模型决定走哪条路径 |
| `relay/channel/task/doubao/asset*.go` | 视频生成的素材库代理，双协议素材注册与 `asset://` 透传 |
| `controller/codex_oauth.go`、`service/codex_credential_refresh.go` | OAuth 凭据自动轮换 |
| `model/channel_routing.go` | 渠道路由模式可切换 |
| 渠道亲和性 | 支持 `request_header` 作为 Key 来源，同一客户端粘住同一上游账号 |
| `setting/pay_channel.go`、`service/cryptomus.go`、`agou.go` | 多支付通道抽象（waffo 为上游自带，非自研） |
| `controller/sub_site.go`、`model/sub_site.go` | 子站模式：多前台站点共享后端 |
| `service/price_diff.go`、`controller/price_changes.go` | 价格公告跑马灯、分组倍率变更播报 |
| `web/default/src`（976 文件）、`web/classic/src`（418 文件） | 双主题自研前端控制台 |

---
