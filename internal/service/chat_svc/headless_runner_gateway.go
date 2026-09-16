package chat_svc

import "context"

// HeadlessRunnerGateway 是无头运行方(ctl_svc 的控制 API、subagent_svc 的子 agent 工具)对
// 本服务的窄依赖:建会话 → 起轮 →(可选)读最终文本 → 停止,外加子 agent 工具继承调用方
// 项目所需的 SessionProjectID。六个方法是 ctl_svc/subagent_svc 两个消费方需求的并集
// (ctl_svc 不需要 SessionProjectID,但 ChatSvc 结构性地满足这个超集接口)。
type HeadlessRunnerGateway interface {
	EnsureSession(ctx context.Context, req *EnsureSessionRequest) (*EnsureSessionResponse, error)
	Send(ctx context.Context, req *SendRequest) (*SendResponse, error)
	ObserveTurn(sessionID int64) (<-chan TurnResult, func())
	Stop(ctx context.Context, req *StopRequest) (*StopResponse, error)
	FinalAssistantText(ctx context.Context, messageID int64) (string, error)
	// SessionProjectID 返回某会话所属的 project id(0=未挂项目);子 agent 工具用它继承调用方项目/cwd。
	SessionProjectID(ctx context.Context, sessionID int64) (int64, error)
}

// HeadlessRunnerSvcGateway 生产用无头运行端口实现(供 ctl_svc / subagent_svc bootstrap
// 接线)。Chat() 直接满足这套方法集,所以这里只是取当前单例。
//
// 调用方必须把接线放在 RegisterChat 之后(app.go registerChatService):RegisterChat
// 之前 Chat() 为 nil,拿到的端口会在调用时 panic。
func HeadlessRunnerSvcGateway() HeadlessRunnerGateway { return Chat() }
