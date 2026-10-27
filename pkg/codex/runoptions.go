package codex

import "strings"

type RunOption func(*runSpec)

func Resume(threadID string) RunOption {
	return func(s *runSpec) { s.resumeID = threadID }
}

func RunCollaborationMode(mode CollaborationMode) RunOption {
	return func(s *runSpec) { s.collaborationMode = mode }
}

// RunModel 指定这一轮 turn/start 的 model。
//
// app-server 的 TurnStartParams.model 语义是「Override the model for this turn and
// subsequent turns」——codex 只能在 turn/start 换模型,没有独立的热切换调用。会话里
// 选中的 CLI 模型因此由下一轮带到这里,而不是重开 app-server 或改 -c model=。
// 空串表示这一轮不下发 model(帧与今天逐字相同),是否要显式下发由调用方决定。
func RunModel(model string) RunOption {
	return func(s *runSpec) { s.turnModel = strings.TrimSpace(model) }
}
