package setting

// SubscriptionStrictGroupIsolation 控制订阅扣费是否按 channel group 严格隔离。
// true（默认）：BoundGroup 必须与请求 UsingGroup 严格相等才能扣费；
//   BoundGroup="" 的订阅视为未绑定，不允许任何带 group 的请求消费；
//   UsingGroup="" 的请求不允许消费任何订阅。
// false：兼容模式，允许 BoundGroup="" 的订阅被任意 group 消费（仅用于灰度过渡，
//   修复方案 2026-05-12-subscription-group-isolation-bug.md 上线稳定后建议保留 true）。
var SubscriptionStrictGroupIsolation = true
