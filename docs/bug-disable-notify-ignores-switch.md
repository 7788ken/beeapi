# BUG: DisableChannel/EnableChannel 通知不受开关控制

## 现象

用户在渠道健康设置中关闭了 `notify_on_degrade`，但渠道被动禁用/启用时仍收到邮件通知。

## 根因

通知有两条独立路径，开关只覆盖了降级路径：

| 路径 | 代码位置 | 受 `NotifyOnDegrade` 控制 |
|------|---------|:---:|
| 降级 L0→L1→L2 | `service/channel_health.go:518` | ✅ |
| 禁用 AutoDisabled | `service/channel.go:43` | ❌ 无条件发送 |
| 启用 Enabled | `service/channel.go:61` | ❌ 无条件发送 |

## 修复方案

方案 A：新增 `NotifyOnDisable` / `NotifyOnEnable` 开关（粒度更细）

方案 B：让 `NotifyOnDegrade` 同时覆盖禁用/启用通知（简单，但语义略偏）

建议方案 A，在 `channel_health_setting.go` 加两个字段，`DisableChannel` 和 `EnableChannel` 中加 if 判断。

## 涉及文件

- `service/channel.go:41-43`（禁用通知）
- `service/channel.go:59-61`（启用通知）
- `setting/operation_setting/channel_health_setting.go:46-47`（现有开关定义）

## 发现日期

2026-05-09
