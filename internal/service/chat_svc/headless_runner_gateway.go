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

// headlessRunnerGateway 委托给 chat_svc 默认单例(懒解析 Chat(),兼容 bootstrap 接线早于
// RegisterChat 的时序)。
type headlessRunnerGateway struct{}

func (headlessRunnerGateway) EnsureSession(ctx context.Context, req *EnsureSessionRequest) (*EnsureSessionResponse, error) {
	return Chat().EnsureSession(ctx, req)
}
func (headlessRunnerGateway) Send(ctx context.Context, req *SendRequest) (*SendResponse, error) {
	return Chat().Send(ctx, req)
}
func (headlessRunnerGateway) ObserveTurn(sessionID int64) (<-chan TurnResult, func()) {
	return Chat().ObserveTurn(sessionID)
}
func (headlessRunnerGateway) Stop(ctx context.Context, req *StopRequest) (*StopResponse, error) {
	return Chat().Stop(ctx, req)
}
func (headlessRunnerGateway) FinalAssistantText(ctx context.Context, messageID int64) (string, error) {
	return Chat().FinalAssistantText(ctx, messageID)
}
func (headlessRunnerGateway) SessionProjectID(ctx context.Context, sessionID int64) (int64, error) {
	return Chat().SessionProjectID(ctx, sessionID)
}

// HeadlessRunnerSvcGateway 生产用无头运行端口实现(供 ctl_svc / subagent_svc bootstrap 接线)。
func HeadlessRunnerSvcGateway() HeadlessRunnerGateway { return headlessRunnerGateway{} }
