package types

type RelayFormat string

const (
	RelayFormatOpenAI                    RelayFormat = "openai"
	RelayFormatClaude                                = "claude"
	RelayFormatGemini                                = "gemini"
	RelayFormatOpenAIResponses                       = "openai_responses"
	RelayFormatOpenAIResponsesCompaction             = "openai_responses_compaction"
	RelayFormatOpenAIAudio                           = "openai_audio"
	RelayFormatOpenAIImage                           = "openai_image"
	RelayFormatOpenAIRealtime                        = "openai_realtime"
	RelayFormatRerank                                = "rerank"
	RelayFormatEmbedding                             = "embedding"
	// RelayFormatSystemOne TypeSafe 决策模型 API：state + 带类型 questions 进，类型化答案与概率出。
	// 不是聊天形态，原样透传。
	RelayFormatSystemOne = "system_one"

	RelayFormatTask    = "task"
	RelayFormatMjProxy = "mj_proxy"
)
