# 渠道多 base_url 配置 + 故障自动切换 —— 设计方案

> 日期：2026-06-23
> 范围：本仓（beeapi，new-api 的自研分支）
> 关键词：多 base_url / 故障切换 / 顺序主备 / Redis 冷却 / 渠道路由
> 决策：**轻量档（仅跨请求冷却）+ 顺序主备 + 不新增 DB 列**

---

## 0. 结论速览（TL;DR）

**需求**：单个渠道的 base_url 输入框支持填多个地址（`;` 分隔，如 `http://a:7777;http://b:7777`），某个地址出问题时自动切到下一个，无需人工改配置。

**用例背景**：供应商 A 这类套娃供应商有多个**等价 relay 域名**（`relay-a.example.com` / `relay-b.example.com`），会轮流劣化；此前靠人工切 `base_url` 止血。本功能把这件事自动化。

**采用方案（轻量 + 顺序主备）**：
- 解析：`base_url` 按 `;`+换行拆成有序列表。
- 选择：中继时**永远优先用列表里第一个"健康"（未冷却）的 URL**（顺序主备语义）。
- 冷却：某 URL 因**端点级错误**（连接拒绝/超时/502/503/504）失败 → 写入 Redis 冷却键（短 TTL，默认 60s）。后续请求自动跳过它，TTL 到期自愈。
- **不做**请求内同渠道换 URL（保持改动小、零热路径侵入）。单次请求遇坏 URL 仍走现有"渠道级"重试到别的渠道；冷却使**后续/并发**请求自动避开坏节点——对"持续劣化"场景（供应商 A）几乎瞬间收敛。
- **不新增 DB 列**：复用现有 `BaseURL` 字符串字段，冷却态只存 Redis（临时、自愈），AutoMigrate 零 DDL。

**关键约束（向后兼容底线）**：`GetBaseURL()` 有 **40+ 调用点**（计费/验证/测试/代理/同步）直接拼 URL，必须保证它**始终返回单个合法 URL**（取列表第一个），否则塞 `;` 会让这些路径全部拼出非法地址。

---

## 1. 现状（已核实）

### 1.1 数据与解析
- `model/channel.go:560` `GetBaseURL()`：返回 `*channel.BaseURL`，空则回退 `constant.ChannelBaseURLs[type]`。单字符串，**无多值概念**。
- 多 base_url 的天然先例：**多 Key 模式**（`model/channel.go:112` `ChannelInfo`，`:157` `GetNextEnabledKey`，`MultiKeyMode` Random/Polling + `MultiKeyStatusList` 状态表）——"渠道内多凭证失败切换"已存在，本设计是它的轻量同构版（但状态放 Redis 而非 DB）。

### 1.2 中继链路（唯一注入点）
```
SetupContextForSelectedChannel (middleware/distributor.go:529)
  → SetContextKey(ContextKeyChannelBaseUrl, channel.GetBaseURL())
  → RelayInfo.ChannelBaseUrl (relay/common/relay_info.go:68,196)
  → adaptor.GetRequestURL 拼上游 (如 claude/adaptor.go:45  "%s/v1/messages")
```
**结论**：只要控制写入 `ContextKeyChannelBaseUrl` 的值，就能控制每次尝试用哪个 URL。这是唯一、干净的注入点。

### 1.3 重试循环（controller/relay.go:217-336）
- 每轮 `getChannel`（:227）→ `CacheGetRandomSatisfiedChannel`（加权随机，排除已失败渠道）→ `SetupContextForSelectedChannel` 写 base_url。
- 失败后 `retryParam.ExcludeChannel(channel.Id)`（:331）**整渠道**排除，下轮换别的渠道。
- `shouldRetry`（:427）按错误类型/状态码决定是否重试。
- **当前故障切换粒度 = 渠道级**，不是 URL 级。本功能在"渠道级之下"补一层"URL 级跨请求冷却"。

### 1.4 GetBaseURL() 的 40+ 调用点分类
| 类别 | 代表调用点 | 多 URL 处理 |
|---|---|---|
| 中继主路径 | `distributor.go:529`、`relay_task.go:100/442` | **改用选择器**（取健康 URL） |
| 渠道测试/验证 | `channel-test.go:159`、`channel-verify.go:143/169` | 暂用第一个（可选 Phase 2 逐个测） |
| 计费/用量轮询 | `channel-billing.go`、`codex_usage.go`、`ratio_sync.go` | 用第一个（`GetBaseURL()` 不变） |
| 异步任务/代理 | `video_proxy*.go`、`mjproxy_handler.go:303`、`task_polling.go:330/346` | 用 `GetBaseURL()`（安全，取第一个） |
| 管理/同步 | `channel.go:1796+`、`channel_upstream_update.go` | 用第一个 |

→ 只有"中继主路径"需要感知多 URL；其余靠 `GetBaseURL()` 仍返回单值零改动。

### 1.5 ⚠️ 直接解引用 `*channel.BaseURL` 的出站路径（Codex review 核实补充）
以下路径**不走 `GetBaseURL()`，直接解引用原始字段**，填 `;` 会拼出非法 URL → 必须一并改为 `GetBaseURL()`：

| 路径 | 行 | 用途 | 修复 |
|---|---|---|---|
| `controller/midjourney.go` | `:80` `*midjourneyChannel.BaseURL` | MJ 任务 list-by-condition | 改 `midjourneyChannel.GetBaseURL()` |
| `service/task_polling.go` | `:195` `adaptor.FetchTask(*ch.BaseURL, …)` | 异步任务轮询 | 改 `ch.GetBaseURL()` |
| `controller/ratio_sync.go` | `:233/242` `chItem.BaseURL + …` | 倍率同步拉 `/v1/models` | 改 `chItem.GetBaseURL()` |

> 注：Codex 最初点名的 `mjproxy_handler.go:303`、`channel-test.go:159`、`channel-verify.go:143/169` 经核实**已安全使用 `GetBaseURL()`**（Codex 行号误报）；真正受影响的是上表 3 处。修复用现成的 `GetBaseURL()` 即可，**无需**新增 `PrimaryBaseURL()` 方法（YAGNI）。

---

## 2. 设计

### 2.1 解析（model/channel.go）
新增：
```go
// GetBaseURLs 把 base_url 按 ';' 和换行拆成有序、去空、去重的列表；至少 1 个。
func (channel *Channel) GetBaseURLs() []string {
    raw := ""
    if channel.BaseURL != nil { raw = *channel.BaseURL }
    parts := splitTrimDedup(raw, ";", "\n")        // trim 每段、丢空、保序去重
    if len(parts) == 0 {
        return []string{constant.ChannelBaseURLs[channel.Type]}  // 类型默认值
    }
    return parts
}
```
改造：
```go
func (channel *Channel) GetBaseURL() string {
    urls := channel.GetBaseURLs()
    return urls[0]                 // 始终返回单个合法 URL —— 40+ 旧调用点零改动
}
```
> 单 URL 渠道：`GetBaseURLs()` 返回 `[url]`，`GetBaseURL()` 行为与现状完全一致。

### 2.2 选择器（service 层，顺序主备）
```go
// SelectChannelBaseURL 顺序主备：返回列表里第一个未冷却的 URL；全冷却则回退第一个（不硬失败）。
func SelectChannelBaseURL(channelId int, urls []string) string {
    if len(urls) <= 1 {            // 快速路径：单 URL 零 Redis 开销
        return urls[0]
    }
    for _, u := range urls {
        if !isBaseURLCooling(channelId, u) {   // Redis EXISTS，miss 即健康
            return u
        }
    }
    return urls[0]                 // 全冷却：宁可探活主 URL，也不返回空
}
```
**Redis fail-open（Codex review 补充）**：`isBaseURLCooling` 在 Redis 未启用 / 连接错误 / 超时时一律返回 `false`（视为健康）。语义降级 = 始终用主 URL（顺序主备退化为单 URL，不报错）。这对应 `common/redis.go` 的 `RDB == nil` 禁用模式与历史 Redis 重启场景（如 .252 OOM）。绝不能因 Redis 不可用而阻断中继。
注入点 `distributor.go:529` 改为：
```go
selected := service.SelectChannelBaseURL(channel.Id, channel.GetBaseURLs())
common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, selected)
```

### 2.3 冷却记录（失败回写）
在重试循环失败分支（`relay.go:328` `processChannelError` 旁）增加：
```go
service.RecordBaseURLFailure(channel.Id, relayInfo.ChannelBaseUrl, newAPIError)
```
`RecordBaseURLFailure`：
1. 渠道只有 1 个 URL → 直接 return（廉价短路）。
2. 错误**非端点级**（见 §2.4）→ 不冷却（避免误拉黑好节点）。
3. 否则 `SET channel_baseurl_cooldown:{channelId}:{sha1(url)[:12]} 1 EX {CooldownSeconds}`（带 ±10% jitter 防 TTL 边界惊群）。
> 时序：冷却在本次尝试失败后写入；本请求该渠道已被 `ExcludeChannel`，故收益落在**后续/并发**请求——即"轻量跨请求冷却"。

### 2.4 错误分类（关键，决定冷却谁）
| 错误 | 冷却该 URL？ | 理由 |
|---|---|---|
| 连接拒绝 / reset / DNS 失败 | ✅ | 端点不可达 |
| 超时（context deadline / i/o timeout） | ✅ | 端点劣化（供应商 A 主场景：use_time 飙 500s+） |
| 502 / 503 / 504 | ✅ | 网关/上游不可用 |
| 500 | ⚠️ 默认**不**冷却（可配开启） | 多为上游应用错，非端点；避免误判 |
| 401 / 403（密钥/权限） | ❌ | 账号问题，所有 URL 一样坏 |
| 429（限流） | ❌ | 账号/配额问题，换 URL 无用 |
| 400 / 404 / 内容策略 / quota | ❌ | 请求/账号问题 |
| **流式中途断流**（响应头已发 200 后 scanner 错误/连接 reset，Codex review 补充） | ✅ | 端点劣化的典型表现，状态码已是 200 不会落入 5xx 分支，**需在分类里显式纳入**（检查 `relayInfo` 是否已发响应 + 底层连接错误），否则坏 primary 流式断连漏冷却 |
判定实现：复用 `types.IsChannelError` + 显式状态码白名单 `{502,503,504}` + 网络/超时错误探测（`errors.Is(ctx.DeadlineExceeded)` / `net.Error.Timeout()` / 连接类错误字符串）+ 流式断流（已发响应 + EOF/reset/scanner err）。

### 2.5 配置（admin 运行时，遵循 `feedback_newapi_admin_runtime_config`）
最小集（`config.GlobalConfig.Register` + 前端 section）：
| key | 默认 | 含义 |
|---|---|---|
| `base_url_failover.enabled` | `true` | 全局开关（kill-switch）；填了 `;` 即自动生效，无需渠道级开关 |
| `base_url_failover.cooldown_seconds` | `60` | 冷却 TTL |
| `base_url_failover.cooldown_on_500` | `false` | 是否把 500 也算端点级 |
> KISS：不做渠道级开关、不做轮询/权重——填多个即顺序主备自动切换。

---

## 3. 前端（web/default）

- `base_url` 输入框（按渠道类型分布于 `channel-mutate-drawer.tsx:1335/1407/1553/1582/1807`）：
  - placeholder / 帮助文案补："多个地址用 `;` 分隔，主地址故障时自动切换到备用地址"。
  - `/v1` 结尾校验（`drawer:695-703`）改为**逐段**检查 `;` 分隔的每个 URL。
- 不改表单结构、不加新字段（仍是 `base_url: z.string()`，`channel-form.ts:12`）。
- i18n：新增/改写文案 key，先补 `zh`，再 `bun run i18n:sync` 同步 en/fr/ja/ru/vi（遵循 `feedback_newapi_en_json_chinese_leak` 校验 en 无中文）。

---

## 4. 可观测性

- 重试日志（`relay.go:338` `重试：A->B`）补充本轮实际使用的 URL host，便于排障。
- 冷却 set/命中跳过 打 debug 级日志：`baseurl cooldown: channel #N skip {host} (Ns left)`。

---

## 5. 测试计划

**单元**
- `GetBaseURLs`：`;`、换行、混合、首尾空格、重复、空串、单值、类型默认回退。
- `GetBaseURL`：多值返回第一个；单值与现状一致。
- `SelectChannelBaseURL`：单 URL→首个；多 URL 全健康→主；主冷却→次；全冷却→回退主。
- 错误分类：502/超时/连接拒绝→冷却；401/429/400→不冷却；500 默认不冷却。

**集成冒烟**（本地 13000）
- 渠道配 `http://127.0.0.1:9/;http://<good>/`（首个必失败）：
  - 连发请求，观察首请求走渠道级重试、坏 URL 被写入冷却（`redis-cli KEYS channel_baseurl_cooldown:*`），后续请求直接走 good URL。
  - 等 TTL 到期，确认冷却键消失、恢复探活主 URL。
- `docker exec ... redis-cli` 验证键 + TTL；`curl` 验证响应。

**构建**：`DOCKER_DEFAULT_PLATFORM=linux/amd64` go build + `tsc -b` + rsbuild；本地切 `local-dev` 13000 三主题验证。**不推生产**（`feedback_local_only_no_prod_push`，待授权）。

---

## 6. 已知限制 / 非目标（KISS / YAGNI）

1. **不做请求内同渠道换 URL**：单次请求遇坏 URL 不会在本请求内切到同渠道下一个 URL，而是走现有渠道级重试到别的渠道。若某模型**只有这一个渠道**，则该次请求可能失败（但下一次起就避开坏 URL）。如后续需要，可升级为"完整档"（§见对话决策）。
2. **异步任务/代理路径**（video/mjproxy/task 轮询）：提交时可复用选择器，但轮询按已存 URL，不做多 URL 切换。
3. **渠道测试/验证**：暂用第一个 URL；逐个 URL 健康展示列为可选 Phase 2。
4. **不引入并发计数/权重/负载均衡**：顺序主备即可满足"故障切换"。轮询负载均衡是显式非目标。
5. **冷却态不持久化**：进程/Redis 重启即清空（自愈），可接受。

---

## 7. 改动文件清单（预估）

| 文件 | 改动 |
|---|---|
| `model/channel.go` | 新增 `GetBaseURLs()`；`GetBaseURL()` 改为取首个 |
| `service/baseurl_failover.go`（新） | `SelectChannelBaseURL` / `RecordBaseURLFailure` / 冷却 Redis / 错误分类 |
| `middleware/distributor.go:529` | 改用选择器写 `ContextKeyChannelBaseUrl` |
| `controller/relay.go:~328` | 失败分支调用 `RecordBaseURLFailure` |
| `controller/midjourney.go:80` | `*midjourneyChannel.BaseURL` → `GetBaseURL()`（§1.5） |
| `service/task_polling.go:195` | `*ch.BaseURL` → `ch.GetBaseURL()`（§1.5） |
| `controller/ratio_sync.go:233/242` | `chItem.BaseURL` → `chItem.GetBaseURL()`（§1.5） |
| `controller/channel.go`（写入校验） | 保存 base_url 时逐段 trim + 校验（GAP，避免空格/尾分号） |
| `setting/operation_setting`（配置） | 注册 `base_url_failover.*` |
| `web/default/.../channel-mutate-drawer.tsx` | base_url 文案 + 逐段 `/v1` 校验 |
| `web/default/src/i18n/locales/*` | 文案 key（zh + sync 5 locale） |
| `service/baseurl_failover_test.go`（新） | 单元测试 |

---

## 8. 风险

- 错误分类误判会"误拉黑好节点"或"漏切坏节点"——以保守白名单（502/503/504+网络超时）起步，500 默认不冷却。
- 顺序主备在 TTL 边界有轻微探活惊群——jitter 缓解。
- 热路径只在"多 URL 渠道"上多一次 Redis EXISTS；单 URL 渠道走快速路径零开销。

---

## 9. Codex Review 核实结论（2026-06-23）

Codex 在"模型满载、中断前部分输出"状态给出 review，**行号多处编造/张冠李戴**，经逐条实测核实：

| Codex 发现 | 核实 | 处置 |
|---|---|---|
| P0 直接读 `.BaseURL` 字段会被 `;` 破坏 | ✅ 核心真实，但点名行错（mjproxy/channel-test/verify 实际安全） | 已实测真实 3 处补入 §1.5/§7 |
| P1-1 流式中途断流漏冷却 | ✅ 真实有效 | 补入 §2.4 错误分类 |
| P1-2 Redis 不可用无兜底 | ✅ 真实有效 | 补入 §2.2 fail-open 语义 |
| GAP base_url 写入未 trim/校验 | ✅ 真实 | 补入 §7 写入校验 |
| P1-3 "GetBaseURL 仅 8-12 处不是 40+" | ❌ 误报（实测 35+ 调用点） | 不采纳 |
| P1-4 cooldown 与 ExcludeChannel 层次错位致重试耗尽 | ⚠️ 已被 §2.2"全冷却回退主 URL"缓解；选渠道阶段不感知 URL 冷却是轻量档 by design | 维持设计 |
| P2-1 TTL jitter 未说明 | ❌ §2.3 已写 ±10% jitter | 已覆盖 |
| P2-2 尾部分号空串 panic | ❌ §2.1 已写"丢空" | 已覆盖 |
| 建议新增 `PrimaryBaseURL()` 方法 | ❌ 多余，`GetBaseURL()` 已胜任 | 不采纳（YAGNI） |

**净收益**：1 个真实遗漏（§1.5 直接字段访问点）+ 2 个有效设计补充（流式断流分类、Redis fail-open）+ 1 个 GAP（写入校验）。其余为误报或已覆盖。

---

## 10. 附加硬性需求：在线充值接口按分组限制（复用 `;` 分隔约定）

> **硬性规定**：在线充值（支付）接口必须在后台为**每个支付方式**新增「允许使用的组别」配置项，**使用 `;` 分隔多个分组**——与本文档 §2.1 `base_url` 的 `;` 多值约定保持一致，复用同一套 `splitTrimDedup(raw, ";", "\n")` 解析（trim 每段 / 丢空 / 保序去重）。

### 10.1 语义
- 后台每个支付方式（`PayMethods` 项）**各自独立**新增字段 `allowed_groups`：`;` 分隔的用户分组白名单，如 `vip;premium`。每个充值接口单独配置、**互不影响**（A 方式限 `vip`，B 方式可同时限 `premium;default`，C 方式留空）。
- **不设置（空）= 对所有分组开放**（向后兼容，存量配置零影响——这是默认值，缺省即放行）。
- 仅当当前用户 `user.Group`（`model/user.go:42`，单值 `varchar(64)`）**精确命中**白名单某一段时，`/api/user/topup/info`（`GetTopUpInfo`）才向其返回该支付方式；前端 `/wallet` 充值页据此只渲染该用户可用的支付方式。
- 过滤**只在出参（GetTopUpInfo）时发生**，不触碰下单/回调/对账链路。

### 10.2 落点（已核实）
| 层 | 文件 | 改动 |
|---|---|---|
| 配置存储 | `setting/operation_setting/payment_setting_old.go:20`（`PayMethods []map[string]string`，:39 `UpdatePayMethodsByJsonString` 反序列化） | 每项新增 `allowed_groups` key——**`map[string]string` 无需改 struct**，反序列化天然兼容（旧配置无此 key→读到空串→放行） |
| 过滤逻辑 | `controller/topup.go:24` `GetTopUpInfo()`（:26 读 `operation_setting.PayMethods`） | 函数内先取 `userGroup`（**沿用同文件 :226 / :451 的 `model.GetUserGroup(c.GetInt("id"), true)` 先例**），组装方式列表时按 `userGroup ∈ split(allowed_groups, ";")` 过滤；空白名单放行 |
| 后台编辑 UI | `features/system-settings/integrations/payment-method-dialog.tsx`（现有 name/type/color/min_topup 表单） | 新增「允许使用的组别」字段；可复用 `features/keys/components/group-picker-dialog.tsx` / `components/model-group-selector.tsx` 做多选，提交时 `groups.join(';')`，留空表示全部开放 |
| 文案 | help 文案固定为：**「允许使用的组别，使用 `;` 分隔多个分组；留空对所有分组开放」** | zh 先补 + `bun run i18n:sync` 同步 en/fr/ja/ru/vi（遵循 `feedback_newapi_en_json_chinese_leak` 校验 en 无中文） |

### 10.3 先例对齐
参照订阅套餐 `model/subscription.go` 的 `BoundGroup` / `ListBoundGroups()`「按分组限制可见性」既有模式——本需求是其「**多分组（`;` 白名单）**」版本；与本文档 base_url 多值是**同一个 `;` 约定的两处应用**，解析与文案保持统一。

### 10.4 边界
- 分组匹配按**逐段精确相等**比较（不做前缀/模糊），避免 `vip` 误命中 `vip2`。
- `user.Group` 当前是单值（非 `;` 列表）；白名单语义是「该用户的单一分组是否落在允许集合内」。
- 解析复用 §2.1：trim / 去空 / 去重，规避尾分号空串与首尾空格。

### 10.5 改动文件清单（本需求增量）
| 文件 | 改动 |
|---|---|
| `setting/operation_setting/payment_setting_old.go` | `PayMethods` 项新增 `allowed_groups` key（map 无需改 struct） |
| `controller/topup.go` `GetTopUpInfo()` | 取当前用户分组 + 按 `allowed_groups` 白名单过滤支付方式 |
| `features/system-settings/integrations/payment-method-dialog.tsx` | 新增「允许使用的组别」输入（`;` 分隔/留空全开放） |
| `web/default/src/i18n/locales/*` | 文案 key（zh + sync 5 locale） |

### 10.6 动态在线支付方式（Waffo / Cryptomus / Agou）分组限制
> **背景**：§10.1–10.5 的 `allowed_groups` 只作用于静态 `PayMethods`（支付宝/微信/自定义）。而 **Waffo / Waffo Pancake / Cryptomus / Agou** 是 `GetTopUpInfo` 按各自 `enable_*` 开关动态追加的，不在静态列表里，需单独门控。**用户实际要的就是这四个动态网关**（Waffo 经典与 Pancake 是两个独立网关，各有独立白名单，互不影响）。

**配置（每个方式独立一个白名单，`;` 分隔，空=全部分组）**
| 方式 | 配置项（option key） | 后台入口 |
|---|---|---|
| Waffo（经典） | `WaffoAllowedGroups` | 系统设置→集成→支付网关→Waffo 区「允许使用的组别」 |
| Waffo Pancake | `WaffoPancakeAllowedGroups` | 同上 Waffo Pancake 区「允许使用的组别」 |
| Cryptomus | `CryptomusAllowedGroups` | 同上 Cryptomus 区 |
| Agou | `AgouAllowedGroups` | 系统设置→集成→Agou 支付网关「允许使用的组别」 |

**双层强制（缺一不可）—— 注意是「显示但禁用」，不是隐藏**
1. **置灰而非隐藏**（`controller/topup.go` `GetTopUpInfo`）：`enable_*` 与 `pay_methods` **照常返回全部渠道**（不再按分组关开关/删渠道，避免误触发「尚未启用在线充值」兜底）。改为新增响应字段 `group_blocked_methods: []string`（当前分组不可用的方式 type，如 `["cryptomus","waffo_pancake"]`）。前端 `/wallet` **依旧渲染全部渠道**，命中该列表的渲染为**灰显虚框 + 不可选 +「您的分组暂不支持」**（复用既有 `comingSoon` 占位禁用样式）。标识符与前端 `PAYMENT_TYPES` / `entry.payType` 对齐：`waffo` / `waffo_pancake` / `cryptomus` / `agou` + 静态方式 `type`。
2. **下单强制**（`RequestWaffoPay` / `RequestWaffoPancakePay` / `RequestCryptomusPay` / `RequestAgouPay`）：取 `group` 后 `IsGroupAllowed` 校验，不允许直接 `{"message":"error","data":"您的分组暂不支持该支付方式"}` 返回——防止绕过置灰 UI 直接打下单接口。Agou 的校验前移到 payType 解析之前（fail-fast 授权）。

**共享 helper**：`operation_setting.IsGroupAllowed(allowedGroups, userGroup string) bool`（`payment_setting_old.go`），`IsPayMethodAllowedForGroup` 改为调用它，静态/动态两套同一套 `;` 解析。

**改动文件（增量）**
| 文件 | 改动 |
|---|---|
| `setting/operation_setting/payment_setting_old.go` | 抽出 `IsGroupAllowed`；`IsPayMethodAllowedForGroup` 复用之 |
| `setting/payment_{waffo,waffo_pancake,cryptomus,agou}.go` | 各加 `XAllowedGroups string` 变量 |
| `model/option.go` | InitOptionMap 注册 4 key + updateOptionMap 4 case |
| `controller/topup.go` `GetTopUpInfo` | 顶部算 4 个 `xGroupAllowed`，**不门控 enable**，收集 `group_blocked_methods` 列表返回（Pancake 用独立 `waffoPancakeGroupAllowed`） |
| `controller/topup_{waffo,waffo_pancake,cryptomus,agou}.go` | 下单 handler 加分组校验（agou 前移到 payType 前） |
| `web/.../{waffo,waffo-pancake,cryptomus,agou}-settings-section.tsx` | 各加「允许使用的组别」输入 + 保存 + 默认值 |
| `web/.../system-settings/{section-registry.tsx,integrations/index.tsx,types.ts}` | 透传 + 类型 + 默认值各补 4 key |
| `web/.../wallet/types.ts`、`wallet/components/recharge-form-card.tsx` | `TopupInfo.group_blocked_methods`；`MethodEntry.groupBlocked` 置灰渲染 +「您的分组暂不支持」 |
| `web/.../i18n/locales/{zh,en}.json` + sync | `Not available for your group` |

**本地验证（真实容器 13000，已通过）**：def-test 四方式 **`enable_*=true`、`pay_methods` 照常含全部渠道**（不再触发「尚未启用在线充值」兜底），`group_blocked_methods=["waffo_pancake","cryptomus"]` 标记置灰；四下单接口仍被 `您的分组暂不支持该支付方式` 拦截。vip-test `group_blocked_methods=[]`、可正常下单（Pancake VIP 实拿到真实 `pancake.waffo.ai` checkout_url）。
