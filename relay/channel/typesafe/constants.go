package typesafe

const ChannelName = "typesafe"

// RequestPath 厂商唯一的评估端点。所有 System One 模型都靠 body 里的 model 字段选择，
// 不靠路径。
const RequestPath = "/v1/systemone"

// ModelList 新建 TypeSafe 渠道时预填的模型。jev-latest / jev-preview 是厂商别名，
// 目前都解析到 jev-1.13.0；别名会随厂商发版移动，阈值对答案敏感的客户应自行钉版本号。
var ModelList = []string{
	"jev-latest",
	"jev-preview",
}
