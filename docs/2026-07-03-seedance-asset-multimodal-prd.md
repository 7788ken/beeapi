# Seedance（豆包/BytePlus）视频渠道支持素材库 / 视频参考 / 多模态 —— 设计方案

> 日期：2026-07-03（修订：2026-07-16，v2）
> 范围：本仓（beeapi，new-api 的自研分支），渠道类型 54 `doubao-video`
> 关键词：Seedance 2.0 / 多模态 content[] / 视频参考 reference_video / 素材库 asset:// / 素材上传代理 / 视频输入折扣
> 决策（v2）：**多模态受控透传（✅ 已实现）+ 便捷字段 videos[]（✅ 已实现）+ asset:// 透传（✅ 已实现）+ 素材上传/查询代理（原 P1 → 升级为本期必做 P0，对外表面对齐 `/v1/sd/assets`）**；计费折扣模型名归一化缺陷 **已修复**
> 上游文档：厂商官方 Seedance 文档
> 对标参考：`sd_real_max.md`（第三方网关 model.service-inference.ai 的素材+视频 API 表面）

---

## 0. 结论速览（TL;DR）

**需求**：Seedance 渠道要支持上游 BytePlus 已开放的三项能力——**素材库（`asset://`）**、**视频参考（`reference_video`）**、**多模态 `content[]`**（含首/尾帧、参考音频、视频编辑/延展/衔接），并且客户能**不出 beeapi 就完成端到端链路**：

```
① 上传素材 POST /v1/sd/assets  →  ② 查询素材 GET /v1/sd/assets/{id}（等 Active）
→  ③ 生成视频 POST /v1/video/generations（content[] 引用 asset://）
→  ④ 轮询任务 GET /v1/video/generations/{task_id}
```

**实现状态（2026-07-16 对照代码核实）**：
- **模块 A（多模态 content[] 受控透传）✅ 已实现**：`TaskSubmitReq.Content`（`relay/common/relay_info.go:693`）+ 白名单校验 `validateContentItems`（`adaptor.go:268`）+ 透传组装 `buildContentItems`（`adaptor.go:569`），含 `multimodal_test.go` 测试。
- **模块 B（videos[] 便捷字段）✅ 已实现**：`TaskSubmitReq.Videos`（`relay_info.go:696`）+ `collectVideoURLs`（`adaptor.go:552`）。
- **模块 C-1（asset:// 规范化透传）✅ 已实现**：`normalizeAssetURL`（`adaptor.go:302`）。
- **模块 C-2（素材上传/查询代理）✅ 已实现（2026-07-16）**：`/v1/sd/assets` 上传 + `/v1/sd/assets/{id}` 查询，见 §3.3。
- **模块 D（sd 网关上游协议，✅ 已实现 2026-07-16）**：新增渠道类型 **58 `SdVideo`**，对接 sd_real_max.md 描述的上游（如 `model.service-inference.ai`）——复用 doubao 适配器全部校验/计费/content 组装，仅线协议切换：提交 `POST {base}/v1/video/generate`、轮询 `GET {base}/v1/video/tasks/{id}`（`{"task":{...}}` 信封），素材走 `POST {base}/v1/sd/assets` / `GET {base}/v1/sd/assets/{id}` 透传。实现见 `relay/channel/task/doubao/sd_flavor.go` + `asset.go`（`*ForChannel` 按渠道类型分发）。渠道类型 54 仍为火山方舟原生协议，两者并存。
- **计费折扣修复 ✅ 已实现**：`canonicalSeedanceModel`/`GetVideoInputRatio`（`constants.go:38/:62`）+ 视频输入三入口识别 `hasVideoInput`（`adaptor.go:339`）。

**关键约束（向后兼容底线）**：
1. 不传 `content[]`/`videos[]` 的存量调用必须与现状**字节等价**——新逻辑只在新字段出现时触发。
2. `model` 注入的防绕过语义不变（`taskcommon/helpers.go:21` `UnmarshalMetadata` 仍 `delete metadata["model"]`）。
3. 遵循 CLAUDE.md **Rule 1**（JSON 走 `common.Marshal/Unmarshal`）、**Rule 2**（新表三库兼容）、**Rule 6**（可选标量用指针+omitempty 保留显式零值）。

---

## 1. 现状（2026-07-16 核实）

### 1.1 请求模型
- `relay/common/relay_info.go:680` `TaskSubmitReq`：字段 `Prompt`、`Model`、`Image`、`Images[]`、`Size`、`Duration`(int)/`Seconds`(string)、`InputReference`、`Metadata map[string]interface{}`；**已新增** `Content []map[string]interface{}`（:693）与 `Videos []string`（:696），均 `omitempty`。
- `relay/channel/task/taskcommon/helpers.go:16` `UnmarshalMetadata`：把 `metadata` map JSON round-trip 进目标 struct，:21 先 `delete "model"` 防计费绕过。

### 1.2 doubao adaptor 中继链路（现状）
```
ValidateRequestAndSetAction  ← duration/resolution/ratio 前置校验 + validateContentItems 白名单校验
  → BuildRequestBody → convertToRequestPayload
      → content[] 存在 → buildContentItems 透传（asset:// 规范化，忽略 images[]/videos[]）
      → 否则 → collectImageURLs + collectVideoURLs + prompt 简易路径
  → BuildRequestURL (adaptor.go:312)  "%s/api/v3/contents/generations/tasks"
  → EstimateBilling → hasVideoInput (adaptor.go:339)  顶层 content[] / videos[] / metadata.content 三入口识别
```

### 1.3 顶层字段策略
- `content[]` 已从「一律 400」改为受控透传（`contentRoleWhitelist`，`adaptor.go:257`）。
- `topLevelMustGoToMetadata`（`constants.go:92`）黑名单保留：`resolution`/`ratio`/`generate_audio`/`watermark`/`seed`/`return_last_frame`/… 放顶层仍 400，须挪进 `metadata`。**本期不放开**（见 §8 非目标）。

### 1.4 素材库（✅ 已对接，2026-07-16）
- 代理实现：`relay/channel/task/doubao/asset.go`（上游 CreateAsset/GetAsset 封装 + 白名单脱敏）、`middleware/sd_asset_adapter.go`（入参校验 + 默认 model 注入）、`controller/sd_asset.go`（handler + wait 轮询）、`model/sd_asset.go`（素材落库）、`router/video-router.go`（`/v1/sd/assets*` 路由）。
- 历史背景：此前 `asset://` 虽可透传，但客户没有上游 key 拿不到素材 ID，能力形同虚设——C-2 落地后链路闭环。

### 1.5 计费折扣缺陷（已修复，保留记录）
- 原缺陷：`videoInputRatioMap` 键为带版本号全名（`doubao-seedance-2-0-260128`），`EstimateBilling` 用对外名（渠道 119 = `doubao-seedance-2-0`）查表永远 miss → 带视频输入按高费率多计费。
- 修复：折扣表改模型族键（`seedance-2-0`、`seedance-2-0-fast`）+ `canonicalSeedanceModel` 归一化（去 `doubao-`/`dreamina-` 前缀 + 末尾 `-<6 位及以上数字>` 版本后缀）。未配折扣的变体（`mini`/`filter-off`/`-hc` 等）归一后 miss，按基础价（不误打折）。

## 2. 上游能力参考（content[] type/role 与素材库）

### 2.1 多模态 type × role
| type | role | 用途 |
|---|---|---|
| `text` | 无 | prompt 文本 |
| `image_url` | `reference_image` | 普通参考图 |
| `image_url` | `first_frame` / `last_frame` | 严格首/尾帧控制 |
| `video_url` | `reference_video` | 视频编辑 / 延展 / 多段衔接 |
| `audio_url` | `reference_audio` | 参考音频（`mini` 模型不支持） |

场景：编辑 = `text`+`video_url(reference_video)`；延展 = `text`+1 个 `reference_video`（prompt 注明 forward/backward）；衔接 = 2~3 个 `reference_video`。

### 2.2 素材库控制面（`POST {base_url}/?Action={X}&Version=2024-01-01`，Bearer 渠道 key）
| 功能 | Action | 要点 |
|---|---|---|
| 上传素材 | `CreateAsset` | 入参 `model`/`GroupId`(可选)/`URL`(公网可下载)/`AssetType`(Image/Video/Audio)/`Name`；返回 `Result.Id` |
| 查询素材 | `GetAsset` | 轮询状态：`Processing`→`Active`/`Failed`，仅 `Active` 可用；退避 2/5/10/20s |
| 素材列表 | `ListAssets` | 需 `Filter.GroupType=AIGC`（本期不代理，见 §8） |
| 素材组 | `CreateAssetGroup`/`ListAssetGroups`/`GetAssetGroup` | 本期不代理（见 §8） |

引用格式 `asset://<Asset_Id>`（平台接受 `Asset://` 并归一化为小写）。`Delete*`/`Update*` 上游不开放（`403 InvalidAction`）→ 不代理删除。

### 2.3 对外表面对标 sd_real_max.md（客户调用链对照）
| sd_real_max 接口 | beeapi 对应 | 状态 |
|---|---|---|
| `POST /v1/sd/assets` 上传素材 | `POST /v1/sd/assets`（本期新增，格式对齐） | §3.3 |
| `GET /v1/sd/assets/{id}` 查素材 | `GET /v1/sd/assets/{id}`（本期新增，格式对齐） | §3.3 |
| `POST /v1/video/generate` | `POST /v1/video/generations`（已有；`resolution`/`ratio`/`generate_audio`/`watermark` 须放 `metadata`） | ✅ 已支持 content[]+asset:// |
| `GET /v1/video/tasks/{id}` | `GET /v1/video/generations/{task_id}`（已有，通用 TaskDto 格式）或 `GET /v1/videos/{task_id}`（OpenAI 格式） | ✅ |
| `GET /v1/video/tasks` 任务列表 | 无对应 relay 端点 | 非本期（§8） |
| `last_frame_url` / `/v1/video/files/{id}/last-frame` | 无 | 非本期（§8） |

> 素材两个接口因是全新端点，直接采用 sd_real_max 的路径与请求/响应格式（PascalCase + `base_resp`），客户可照该文档直接调；生成/轮询沿用 beeapi 现有端点，差异在对客文档中说明。

## 3. 设计

### 3.1 模块 A：多模态 content[] 受控透传 —— ✅ 已实现

**入站字段**（`relay/common/relay_info.go`）：`Content []map[string]any` + `Videos []string`（omitempty）。

**校验**（`validateContentItems`，`adaptor.go:268`）：
- `type` 必须 ∈ {`text`,`image_url`,`video_url`,`audio_url`}，否则 400 `invalid_content`；
- `role`（若提供）必须匹配 `contentRoleWhitelist`（`adaptor.go:257`）；
- `audio_url` 命中 `mini` 模型 → 400（上游必拒，前置拦截省一次 5xx+重试）。

**组装**（`convertToRequestPayload`）：`content[]` 存在 → `buildContentItems` 规范化 asset:// 后直接透传，不叠加 `images[]`/`videos[]`/`prompt` 自动转换；否则走原 `collectImageURLs`+`collectVideoURLs`+prompt 简易路径（零回归）。`model` 始终由 adaptor 注入。

### 3.2 模块 B：视频参考便捷字段 videos[] —— ✅ 已实现

`collectVideoURLs`（`adaptor.go:552`）：`videos[]` → `content[{type:video_url, role:reference_video, video_url:{url}}]`，保序去重，asset:// 规范化。**优先级**：传了 `content[]` → 以 content[] 为准，忽略 `videos[]`/`images[]`。

### 3.3 模块 C：素材库

#### C-1 asset:// 规范化透传 —— ✅ 已实现
`normalizeAssetURL`（`adaptor.go:302`）：`^[Aa]sset://` → 小写 `asset://`，其余原样；作用于所有 `image_url`/`video_url`/`audio_url` 的 url。不校验素材是否 `Active`（交上游返回）。

#### C-2 素材上传/查询代理 —— 本期必做（原 P1 升级）

**目标**：客户用 beeapi 令牌即可上传素材、查询状态，拿到 `asset://` 可引用的素材 ID；上游 Bearer 由渠道 key 注入，客户全程不接触上游凭证。

**(a) 路由与中间件**（`router/video-router.go` 新增分组，模式对齐 jimeng 官方 API 分组）：
```go
sdAssetRouter := router.Group("/v1/sd")
sdAssetRouter.Use(middleware.RouteTag("relay"))
sdAssetRouter.Use(middleware.SdAssetRequestConvert(), middleware.TokenAuth(),
    middleware.TokenHealthRecord(), middleware.Distribute())
{
    sdAssetRouter.POST("/assets", controller.RelaySdAssetCreate)
    sdAssetRouter.GET("/assets/:asset_id", controller.RelaySdAssetGet)
}
```
- `SdAssetRequestConvert()`（新中间件，模式对齐 `JimengRequestConvert`，`middleware/jimeng_adapter.go:15`）：
  - POST：解析 PascalCase 请求体（`URL`/`Name`/`AssetType`，兼容可选 `GroupId`、`model`），校验后注入 `model`（缺省用配置项 `SdAssetDefaultModel`，默认 `doubao-seedance-2-0`）供 `Distribute()` 按渠道类型 54 选渠道；
  - GET：无请求体，标记 `shouldSelectChannel=false` 路径（对齐现有任务查询类端点，`middleware/distributor.go:176`），渠道由素材落库记录决定（见 (c)）。

**(b) 对外协议**（对齐 sd_real_max.md）：

上传（`POST /v1/sd/assets`）：
```json
// 请求
{ "URL": "https://example.com/your-image.jpg", "Name": "avatar_front", "AssetType": "Image" }
// 响应
{ "success": true, "data": { "Id": "asset-20260705003737-njxmg",
    "base_resp": { "status_code": 0, "status_msg": "success" } } }
```
查询（`GET /v1/sd/assets/{id}`）：
```json
{ "success": true, "data": { "Id": "...", "Status": "Active", "AssetType": "Image",
    "Name": "avatar_front", "URL": "<asset-url>", "GroupId": null,
    "CreateTime": "...", "UpdateTime": "...",
    "base_resp": { "status_code": 0, "status_msg": "success" } } }
```
- `AssetType` 白名单 {`Image`,`Video`,`Audio`}，`URL` 必填且须 `http(s)://` → 否则 400；
- **双素材体系（2026-07-17 v3）**：sd 网关上游有两套互不相通的素材库——HC 用旧 `/v1/sd/assets` 体系；**260128/ep/fast-ep/mini 系用 sd2 素材组体系**（`/v1/asset-groups` + `/v1/assets` 异步上传 + `/v1/assets/get` 轮询，上游文档 sd2_real）。对外接口不变（仍是 `POST /v1/sd/assets`）：客户显式传 `model`（经渠道映射）→ 非 hc 族自动走 sd2（素材组由服务端按渠道管理："beeapi-ch<渠道id>"，内存缓存 + 上游轮转失效自动重建；也可显式传 `GroupId`）；hc 族/未传 model → 旧体系（HC 默认空间）。落库新增 `protocol`/`upstream_task_id`/`group_id` 列（安全增量 DDL），GET 查询按 protocol 分发。sd2 状态映射对外统一：processing→Processing / completed→Active / failed→Failed。实现：`relay/channel/task/doubao/asset_sd2.go`（含测试）。
- 上游失败 → `success:false` + 透传上游错误码/信息到 `base_resp`（剥离上游内部字段，防 key/内部 host 泄露）。
- 可选便捷参数 `?wait=true`（POST）：服务端按 2/5/10/20s 退避轮询 GetAsset 直至 `Active`/`Failed`，总超时 60s，超时返回当前状态（`Processing`）不报错。

**(c) 素材落库**（GET 路由渠道选择 + 归属权限的依据，模式对齐 `model/task.go` Task 表）：
```go
// model/sd_asset.go（新）
type SdAsset struct {
    ID        int64  `gorm:"primary_key;AUTO_INCREMENT"`
    CreatedAt int64  `gorm:"index"`
    UpdatedAt int64
    AssetID   string `gorm:"type:varchar(191);index"` // 上游素材 ID
    UserId    int    `gorm:"index"`
    ChannelId int    `gorm:"index"`
    AssetType string `gorm:"type:varchar(20)"`
    Name      string `gorm:"type:varchar(191)"`
    Status    string `gorm:"type:varchar(20);index"`  // Processing/Active/Failed（最后一次查询快照）
    Data      json.RawMessage `gorm:"type:json"`      // 上游原始响应（脱敏后）
}
```
- Rule 2：GORM `AutoMigrate` 注册（`model/main.go:276` 处），JSON 用 `type:json`（三库均由 GORM 映射，SQLite 落 TEXT），无 raw SQL。
- POST 成功 → 落库（AssetID/UserId/ChannelId/AssetType/Name/Status=Processing）；
- GET → `GetSdAssetByAssetId(userId, assetId)` 查记录（**仅创建者可查**，语义对齐 `model.GetByTaskId`，`task.go:331`）→ 按 `ChannelId` 取渠道 key 实时拉上游 `GetAsset`（模式对齐 `tryRealtimeFetch`，`relay/relay_task.go:435`）→ 回写 Status 快照 → 映射响应；
- 渠道已删除/禁用 → 400 `invalid_channel_id`（与任务查询同语义）。

**(d) 上游调用封装**（`relay/channel/task/doubao/asset.go` 新文件，挂在 doubao 包内复用 baseURL/鉴权习惯）：
- `CreateAsset`: `POST {base_url}/?Action=CreateAsset&Version=2024-01-01`，body `{model, URL, Name, AssetType, GroupId?}`，`Authorization: Bearer <渠道key>`；响应取 `Result.Id`；
- `GetAsset`: 同形，`Action=GetAsset`，body `{Id}`；
- 超时/重试：单次 30s 超时，不自动重试（幂等性未知，交客户端重试）；`wait=true` 的轮询仅重复 GetAsset（只读，幂等安全）。

**(e) 计费**：素材上传/查询本期**不计费**（无模型推理消耗；上游素材存储成本视运营情况后续再议按次计费开关）。仅记录请求日志（复用现有 relay 日志通路，quota=0）。

### 3.4 计费：视频输入折扣 —— ✅ 已实现（保留记录）

折扣表模型族键 + `canonicalSeedanceModel` 归一化查询（`constants.go`）；`hasVideoInput`（`adaptor.go:339`）覆盖顶层 `content[]` + `videos[]` + `metadata.content` 三入口，任一命中即应用折扣。命中：`doubao-seedance-2-0`、`doubao-seedance-2-0-260128`、`dreamina-seedance-2-0`、`*-fast*` 各别名；`mini`/`filter-off`/`-hc` 等未配折扣变体按基础价。已有 `constants_test.go` 单测（golang:1.25-alpine ALL PASS）。

## 4. 请求示例（端到端链路）

① 上传素材：
```bash
curl -X POST "$BEEAPI/v1/sd/assets" -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"URL":"https://example.com/dance.mp4","Name":"dance_clip","AssetType":"Video"}'
# → data.Id = "asset-20260716xxxxxx-abcde"
```
② 轮询素材至 Active：
```bash
curl "$BEEAPI/v1/sd/assets/asset-20260716xxxxxx-abcde" -H "Authorization: Bearer $TOKEN"
```
③ 生成视频（content[] 引用素材，视频编辑场景）：
```json
POST /v1/video/generations
{
  "model": "doubao-seedance-2-0",
  "content": [
    { "type": "text", "text": "把镜头改成黄昏色调" },
    { "type": "video_url", "role": "reference_video",
      "video_url": { "url": "asset://asset-20260716xxxxxx-abcde" } }
  ],
  "metadata": { "resolution": "1080p", "ratio": "16:9" },
  "duration": 5
}
```
④ 轮询任务：`GET /v1/video/generations/{task_id}`。

首帧+尾帧：
```json
{ "model": "doubao-seedance-2-0", "content": [
  { "type": "text", "text": "从清晨过渡到夜晚" },
  { "type": "image_url", "role": "first_frame", "image_url": { "url": "https://.../a.png" } },
  { "type": "image_url", "role": "last_frame",  "image_url": { "url": "https://.../b.png" } } ] }
```
便捷视频延展：
```json
{ "model": "doubao-seedance-2-0", "prompt": "向后延展5秒，保持运镜", "videos": ["https://.../clip.mp4"] }
```

## 5. 校验与错误处理

沿用「本地前置拦截会致上游 5xx+无谓重试的非法请求」哲学：
- `content[].type` 非白名单 → 400；`role` 与 type 不匹配 → 400（✅ 已实现）。
- `audio_url` 命中 `mini` 模型 → 400（✅ 已实现）。
- `asset://` 仅规范化格式，不校验 Active（✅ 已实现）。
- 顶层黑名单（`constants.go:92`）保留，仅 `content` 不在其列（✅ 已实现）。
- **素材代理（新）**：`AssetType` ∉ {Image,Video,Audio} → 400；`URL` 缺失或非 http(s) → 400；`GET` 素材不存在或非本人 → 404 `asset_not_exist`；渠道失效 → 400 `invalid_channel_id`；上游错误 → `success:false` + 脱敏 `base_resp` 透传。

## 6. 改动文件清单

| 文件 | 改动 | 阶段 |
|---|---|---|
| `relay/common/relay_info.go` | ✅ 已改：`Content`/`Videos`（omitempty） | 已完成 |
| `relay/channel/task/doubao/adaptor.go` | ✅ 已改：content[] 白名单透传、`collectVideoURLs`、`normalizeAssetURL`、`hasVideoInput` | 已完成 |
| `relay/channel/task/doubao/constants.go` | ✅ 已改：折扣模型族键 + 归一化 | 已完成 |
| `relay/channel/task/doubao/constants_test.go` / `multimodal_test.go` | ✅ 已加：折扣归一化 + 多模态用例 | 已完成 |
| `relay/channel/task/doubao/asset.go`（新） | ✅ **已实现**：上游 CreateAsset/GetAsset 封装 + 响应脱敏映射（30s 超时，白名单字段） | 已完成 |
| `relay/channel/task/doubao/asset_test.go`（新） | ✅ **已加**：成功/空 Id/业务错误/非 JSON 5xx 脱敏/白名单字段丢弃 用例 | 已完成 |
| `model/sd_asset.go`（新）+ `model/main.go` | ✅ **已实现**：SdAsset 表 + AutoMigrate 注册（Rule 2 三库兼容） | 已完成 |
| `middleware/sd_asset_adapter.go`（新）+ 测试 | ✅ **已实现**：`SdAssetRequestConvert` PascalCase 解析校验 + 默认 model 注入（`SD_ASSET_DEFAULT_MODEL`，默认 `doubao-seedance-2-0`） | 已完成 |
| `controller/sd_asset.go`（新） | ✅ **已实现**：`RelaySdAssetCreate`/`RelaySdAssetGet`（含 `wait=true` 2/5/10/20/20s 退避轮询、状态快照回写、归属校验） | 已完成 |
| `router/video-router.go` | ✅ **已改**：`/v1/sd/assets` POST（走 Distribute）+ GET（按落库记录路由，不走 Distribute） | 已完成 |
| `dto/sd_asset.go`（新） | ✅ **已实现**：`SdAssetCreateRequest`/`SdBaseResp` | 已完成 |
| `constant/channel.go` + `relay/relay_adaptor.go` + `controller/channel-test.go` | ✅ **已实现**：渠道类型 58 `SdVideo`（默认 base `https://model.service-inference.ai`）+ adaptor 注册（`UpstreamFlavor: sd`）+ 渠道测试豁免 | 已完成 |
| `relay/channel/task/doubao/sd_flavor.go`（新）+ 测试 | ✅ **已实现**：sd 上游线协议（generate/tasks 信封解析、状态映射、fail_reason 三形态、OpenAI Video 转换） | 已完成 |
| `relay/channel/task/doubao/asset.go` | ✅ **已改**：`CreateAssetForChannel`/`GetAssetForChannel` 按渠道类型分发（58→sd 透传协议，54→方舟 Action 控制面） | 已完成 |
| `web/default/src/features/channels/`（constants.ts / channel-utils.ts） | ✅ **已改**：前端渠道类型 58 选项 + 图标 | 已完成 |

## 7. 测试计划

**单元**
- ✅ 已过：折扣归一化（`constants_test.go`）；content[] 各 type/role 组装、asset:// 规范化、videos[]→reference_video 去重、mini+audio 拒绝（`multimodal_test.go`/`adaptor_test.go`）。
- ✅ 已过（素材代理，2026-07-16 本机 go1.26 ALL PASS）：PascalCase/小写字段解析与校验（AssetType/URL 非法 → 400，sd base_resp 错误形状）、默认/显式 model 注入（`sd_asset_adapter_test.go`）；CreateAsset 成功/空 Id/上游业务错误映射/非 JSON 5xx 脱敏/白名单字段丢弃（`asset_test.go`）。
- 待补（需 DB/集成环境）：归属校验（非本人 404）、`wait=true` 退避实测、渠道失效 400。
- 回归：现有 duration 优先级 / reference_image role / 顶层黑名单拒绝保持通过（✅ 2026-07-16 复跑通过）。

**集成冒烟**（本地 13000）
- 渠道配 Seedance：① `POST /v1/sd/assets` 上传真实图片/视频 URL → 拿到 asset id；② 轮询至 Active；③ 分别用 content[]（含 asset://）、videos[]、纯 prompt+images[] 提交生成任务，验证上游 payload 与计费折扣命中；④ 用户 B 查用户 A 的素材 → 404。
- 三库迁移冒烟：SQLite/MySQL/PostgreSQL 各启一次验证 SdAsset AutoMigrate。

**构建**：在正式构建环境跑 `go test ./relay/channel/task/doubao/ ./model/ ./middleware/` + `go build`。本地无 Go/Docker 环境，仅本地开发验证，**不推生产**（待授权）。

## 8. 已知限制 / 非目标（KISS / YAGNI）

1. 素材删除/更新不代理（上游 `403 InvalidAction`）；素材列表 `ListAssets`、素材组 `*AssetGroup*` 本期不代理（sd_real_max 表面用不到，YAGNI，后续有需求再加）。
2. 审核策略（`Moderation.Strategy=Skip`）不开放，不实现。
3. **任务列表**（sd_real_max 的 `GET /v1/video/tasks` 分页列表）非本期：现有 relay 无该端点，客户按 task_id 单查（`/v1/video/generations/{id}`）。
4. **last_frame_url** 非本期：请求侧 `return_last_frame` 字段已存在（`adaptor.go:87`），但上游响应解析（`responseTask` 无该字段）、响应露出、`/v1/video/files/{id}/last-frame` 文件代理均不做。
5. **`/v1/video/generate` 别名与顶层参数放行**非本期：生成沿用 `/v1/video/generations`，`resolution`/`ratio`/`generate_audio`/`watermark` 仍须放 `metadata`（`topLevelMustGoToMetadata` 黑名单不动）；差异写进对客文档（§2.3 对照表）。
6. 前端「创作中心」多模态编排 UI 归 P2，本期仅 API/渠道层。
7. content[] 透传以白名单前置校验为主，非法组合其余交上游返回并透传 `fail_reason`（`formatDoubaoFailReason`）。

## 9. 风险

- 放开 content[] 后客户可能传上游不支持的组合 → 白名单尽量前置，其余透传上游错误。（已上线，暂无反馈异常）
- 计费折扣修复改变线上计费数值（方向「少计费」，不会多收费）→ 上线前用日志比对 + 灰度观察。
- **素材代理上游响应透传须脱敏**：上游 `Result` 可能含内部字段/预签名 URL 参数，映射白名单字段（Id/Status/AssetType/Name/URL/GroupId/CreateTime/UpdateTime），禁止整包透传。
- **`wait=true` 轮询挂死**：总超时 60s 硬上限 + 只读幂等（仅 GetAsset），超时返回 Processing 而非报错。
- **SdAsset 新表迁移**：AutoMigrate 三库各冒烟一次；表只增不改，回滚无 schema 风险。
- **GET 路由依赖渠道存活**：素材记录绑定创建时渠道，渠道删除后素材不可查（与任务查询同语义）；文档提示客户素材与渠道生命周期绑定。
- 素材上传不计费 → 恶意刷上传的成本风险：受 TokenAuth + 现有限流中间件约束；如出现滥用再加按次计费/配额开关。
- 遵循 Rule 6：新增可选标量务必指针+omitempty，防显式零值被静默丢弃。
