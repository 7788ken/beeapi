package doubao

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/pkg/errors"
)

// sd 网关风格上游（如 model.service-inference.ai，渠道类型 58）：
// 与火山方舟同一套 Seedance 语义（content[]/asset:///duration/resolution/ratio），
// 仅线协议不同 —— 提交 POST {base}/v1/video/generate、轮询 GET {base}/v1/video/tasks/{id}、
// 响应统一包在 {"task":{...}} 信封里。请求 payload 与方舟一致，直接复用 requestPayload。
const (
	UpstreamFlavorArk = "ark"
	UpstreamFlavorSd  = "sd"
)

// sdTask sd 网关的任务对象（提交与轮询响应共用）。
type sdTask struct {
	ID              string          `json:"id"`
	Status          string          `json:"status"` // pending / processing / completed / failed
	Model           string          `json:"model"`
	DurationSeconds int             `json:"duration_seconds"`
	Outputs         []string        `json:"outputs"`
	Error           json.RawMessage `json:"error"` // null / 字符串 / {code,message}
	LastFrameURL    string          `json:"last_frame_url"`
	CreatedAt       string          `json:"created_at"` // RFC3339 字符串（方舟为 int64，不能共用结构）
	CompletedAt     string          `json:"completed_at"`
	Usage           struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type sdTaskEnvelope struct {
	Task *sdTask `json:"task"`
}

// parseSdTaskEnvelope 解析 sd 任务响应；容忍缺失 {"task":...} 信封的裸任务对象。
func parseSdTaskEnvelope(body []byte) (*sdTask, error) {
	var envelope sdTaskEnvelope
	if err := common.Unmarshal(body, &envelope); err == nil && envelope.Task != nil {
		return envelope.Task, nil
	}
	var bare sdTask
	if err := common.Unmarshal(body, &bare); err != nil {
		return nil, errors.Wrap(err, "unmarshal sd task response failed")
	}
	if bare.ID == "" && bare.Status == "" {
		return nil, fmt.Errorf("sd task response has neither task envelope nor task fields")
	}
	return &bare, nil
}

// parseSdTaskResult 把 sd 任务响应映射为内部 TaskInfo（轮询用）。
func parseSdTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	task, err := parseSdTaskEnvelope(respBody)
	if err != nil {
		return nil, err
	}

	taskResult := relaycommon.TaskInfo{Code: 0}
	switch task.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "completed", "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		if len(task.Outputs) > 0 {
			taskResult.Url = task.Outputs[0]
		}
		taskResult.CompletionTokens = task.Usage.CompletionTokens
		taskResult.TotalTokens = task.Usage.TotalTokens
	case "failed":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = sdFailReason(task.Error)
	default:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}
	return &taskResult, nil
}

// sdFailReason 从 sd 任务的 error 字段（null / 字符串 / {code,message}）提取失败原因。
func sdFailReason(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "upstream returned failed status without details"
	}
	var s string
	if err := common.Unmarshal(raw, &s); err == nil && strings.TrimSpace(s) != "" {
		return s
	}
	var obj struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := common.Unmarshal(raw, &obj); err == nil {
		code, msg := strings.TrimSpace(obj.Code), strings.TrimSpace(obj.Message)
		switch {
		case code != "" && msg != "":
			return fmt.Sprintf("code=%s: %s", code, msg)
		case code != "":
			return fmt.Sprintf("code=%s", code)
		case msg != "":
			return msg
		}
	}
	return string(raw)
}

// convertSdTaskToOpenAIVideo 把落库的 sd 任务快照转成 OpenAI Video 格式（/v1/videos/{id} 用）。
func convertSdTaskToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	task, err := parseSdTaskEnvelope(originTask.Data)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal sd task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	if len(task.Outputs) > 0 {
		openAIVideo.SetMetadata("url", task.Outputs[0])
	}
	if task.LastFrameURL != "" {
		openAIVideo.SetMetadata("last_frame_url", task.LastFrameURL)
	}
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	if task.Status == "failed" {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: sdFailReason(task.Error),
		}
	}

	return common.Marshal(openAIVideo)
}
