# beeapi

**beeapi** 是 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 的重度自研分支 —— 一个聚合 40+ 上游 AI 服务商的 API 网关，带用户管理、计费、限流与管理后台。

本分支在上游基础上补了大量**生产环境打磨出来的**渠道治理、计费正确性与安全加固。开源出来供同行参考。

> 📖 **[完整功能与设计说明 → docs/FEATURES.md](docs/FEATURES.md)** —— 36 项自研功能与修复，每项含介绍 / 原理 / 实现方案。

---

## 相对上游的主要增强

### 流量治理与容错
- **协议互转层** —— OpenAI Chat / Claude Messages / Gemini / OpenAI Responses 全矩阵互转，做成有向图路由 + 质量标注，新增协议只需接到图上
- **渠道健康度自动降级** —— 真实请求驱动的被动状态机，渐进降级而非一刀切禁用，连续成功自动恢复
- **多 base_url 故障切换** —— 同渠道多节点按「稳（熔断冷却）+ 快（TTFB EWMA）」双维择优，带迟滞防横跳与保底探索
- **重试短路网关** —— 拦截客户端超时断连后的 SDK 自动重放，终止烧钱循环
- **容量感知路由** —— 滑动窗口 RPM 计数参与选渠道，配合渠道级软/硬桶全满策略
- **prompt-cache 感知重试** —— 跨渠道重试会丢 Claude 缓存，按渠道 × 令牌两轴取最保守策略
- **Token 错误率熔断**、**用户级渠道软限速**（额度抖动 + 配额内延迟）

### Relay 传输层正确性
- 流式客户端断开**不再连带取消上游**，读到底拿真实 usage 防漏计费
- 出站请求补 `GetBody`，支持 HTTP/2 流重置后透明重放
- 流式 scanner 统一缓冲上限，修超长 SSE 行断流

### 观测与度量
- **错误归因分类** —— 上游 / 客户端 / 网关三分，只有上游故障进可用率分母
- **渠道与分组可用率** —— 按样本发生时刻归属，排除禁用期探活污染
- **渠道自动验收测评** —— 可调度、可排队、可中断的异步验收流水线
- 大表查询优化：复合游标翻页、索引列序自愈、扫描边界、SQL 层聚合

### 计费与资金安全
- **钱包预扣凭据 + 清扫任务** —— 先落凭据再动余额，退款失败有兜底
- **资金路径锁序统一 + 信任旁路** —— 消除 AB-BA 死锁，根治 `users.quota` 单行热点
- **零产出免单** —— RelayFormat 白名单 fail-closed，`client_gone` 加时长门槛防刷缓存
- 跨组重试后按最终分组倍率结算、批量账本落库、价格发布与变更通知、上游倍率监控与实付反推

### 认证与安全
- **完整会话控制体系** —— access token + refresh 轮换 + 可吊销会话，带重放窗口与双版本号
- **一次性认证流程凭据** + **敏感操作二次证明**（scope × method 绑定）
- **SSRF 深度防护** —— 校验下沉到 Dialer，拨号时锁定已校验 IP，消除 DNS rebinding
- **密钥泄露三层封堵** —— SQL 日志、错误响应、管理接口

### 平台工程
- **日志迁移到 ClickHouse** —— 可选后端 + schema 自校验 + 断点回填 + 回滚工具
- **优雅关闭与长连接排空** —— SSE / WebSocket 的两阶段排空
- 敏感词异步审计、子站模式、邮件出站限速、异常监控

---

## 与上游的关系

本分支与上游**无共同 git 祖先**，采用选择性合并 + 自研，版本号自编号，**不对应上游版本**。

- 上游：https://github.com/QuantumNous/new-api （AGPL-3.0）
- 上游原始 README 保留为 [README.upstream.md](README.upstream.md)
- 上游更早源自 [One API](https://github.com/songquanpeng/one-api)（MIT）

部署方式、渠道配置、环境变量等基础用法与上游一致，参见 [README.upstream.md](README.upstream.md)。

---

## 构建

```bash
# 前端（需要 bun）
cd web/default && bun install && bun run build
cd ../classic && bun install && bun run build

# 后端
go build -o beeapi
```

前端产物会被 `embed` 进二进制，所以**必须先构建前端再编译后端**。

---

## 说明

- 本仓库为**源码分享**，不含任何生产配置、部署脚本与运维文档。
- 上游 CI workflow 已移除，避免误触发镜像推送。
- 外部测评网关（`VERIFY_GATEWAY_BASE`）默认未配置，该功能处于关闭状态；需要时自行部署兼容 `/api/verify/{protocol}` SSE 协议的网关。
- 代码中的中文注释保留了大量设计取舍与踩坑记录，是本分支最有参考价值的部分之一。

## License

[GNU Affero General Public License v3.0](LICENSE)，与上游一致。
