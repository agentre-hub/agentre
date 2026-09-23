package ctl_svc

import (
	"context"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// approvalTimeout 是会话审批卡与桌面弹窗的挂起上限（spec 决策 12，与 orgtool 一致）。
const approvalTimeout = 4 * time.Minute

// ApprovalPendingHeader 是写请求挂起等审批前，执行者先发的 102 Processing 里带的头：
// `session=<id>` 表示审批卡在该会话里，`desktop` 表示在桌面端弹窗里。agrctl 据此打印
// waiting 行（它在 ctlcmd 里有一份同值常量，两边由 ctlcmd 的端到端测试钉住）。
const ApprovalPendingHeader = "Agentre-Ctl-Approval"

// SessionApprovals 在会话的对话流里登记 / 决议审批卡（chat_svc 通用工具审批的窄投影）。
type SessionApprovals interface {
	BeginToolApproval(ctx context.Context, sessionID int64, blk *blocks.ToolApprovalBlock) (<-chan bool, error)
	FinishToolApproval(ctx context.Context, sessionID int64, requestID, status, result string) error
}

// ExternalApproval 是一条等桌面端用户审批的外部调用写请求。
type ExternalApproval struct {
	RequestID string
	Input     blocks.CtlApprovalInput
	// Caller 是调用方进程信息，只作提示、不作为信任依据（spec「外部调用的审批弹窗」）。
	//
	// 目前恒为 nil：CtlWriteRequest（pkg/wire/proto/agentre/wire/wire.proto）只带
	// caller 分类枚举和抹掉密钥的 command，agrctl（internal/cli/ctlcmd）并没有上报
	// 父进程名/pid/工作目录。要显示真实值，得先在那条请求里补上这些字段——这里先把
	// 展示这一端接好，字段留空不影响其它审批行为。
	Caller *CallerInfo
}

// CallerInfo 是外部调用方的父进程名、pid 与工作目录；json 标签与桌面弹窗那份
// DesktopApprovalItem 快照一致（camelCase），供前端直接反序列化。
type CallerInfo struct {
	ParentProcess string `json:"parentProcess"`
	Pid           int64  `json:"pid"`
	WorkingDir    string `json:"workingDir"`
}

// ExternalApprovals 是桌面端全局审批弹窗背后的待审批队列（外部调用：握手 token、stdin
// 不是 TTY）。执行者只管入队与撤下，4 分钟的挂起上限由执行者计时（spec 决策 12）。
type ExternalApprovals interface {
	// Enqueue 由执行者调用：把请求放上队列，返回应答 channel（true = 批准）。
	Enqueue(ctx context.Context, a ExternalApproval) (<-chan bool, error)
	// Answer 由桌面弹窗调用；关闭弹窗等同 Answer(id, false)。请求不在队列里（已撤下、
	// 已答过）时返回错误。
	Answer(requestID string, allow bool) error
	// Withdraw 由执行者在超时或调用方断开时调用：撤下请求，此后的 Answer 一律无效。
	Withdraw(requestID string)
}

// chatApprovals 是生产用的会话审批网关：每次现取 chat_svc 单例。
type chatApprovals struct{}

func (chatApprovals) BeginToolApproval(ctx context.Context, sessionID int64, blk *blocks.ToolApprovalBlock) (<-chan bool, error) {
	return chat_svc.Chat().BeginToolApproval(ctx, sessionID, blk)
}

func (chatApprovals) FinishToolApproval(ctx context.Context, sessionID int64, requestID, status, result string) error {
	return chat_svc.Chat().FinishToolApproval(ctx, sessionID, requestID, status, result)
}
