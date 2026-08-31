// Package peer_svc 是桌面端**出站**对端（另一台桌面端）的会话级客户端（任务 10，
// R18/R19 的出站半边；入站半边在 internal/peer 的 Inbound）。
//
// 传输层复用 server_svc 经账号中继产出的已握手连接：DialDesktopRelay 把「目标桌面
// 端 App 没运行」映射成 ErrDesktopAppNotRunning（与 agentred 离线是两种说法，R2），
// peer.NewOutbound 再把 wire 会话族（list / run / attach / pull / steer / answer /
// tool-permission）包装成类型化方法。本包持有一台远端桌面端一条常驻中继连接：
// Attach 登记实时订阅后，canonical 事件帧经 Emitter 推给前端（R19 / R6）。
package peer_svc

import (
	"context"
	"encoding/json"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

//go:generate mockgen -source ports.go -destination mock_peer_svc/mock_ports.go

// Dialer 拨到一台具名桌面端。真实现是 server_svc.ServerSvc（DialDesktopRelay）；
// 单测注入直连假对端的 dialer。
type Dialer interface {
	DialDesktopRelay(ctx context.Context, desktopFingerprint, peerFingerprint string) (client.ProtobufConnection, error)
}

// Emitter 把 Attach 后的实时事件帧推给上层（生产是 Wails EventsEmit，单测是 spy）。
type Emitter interface {
	Emit(event PeerEvent)
}

// FingerprintProvider 交出本机设备指纹（R5 硬不变量）。真实现是 remote_device_svc。
type FingerprintProvider interface {
	DeviceFingerprint() (string, error)
}

// AgentLookup 是 peer_svc 需要的 agent 行读取子集（派活时把 agentId 翻成账号级
// AgentSyncID，见 RunFreshRequest）。
type AgentLookup interface {
	Find(ctx context.Context, id int64) (*agent_entity.Agent, error)
}

// ProjectLookup 是 peer_svc 需要的项目行读取子集（派活时把 projectId 翻成这台机器
// 上该项目的 cwd，见 RunFreshRequest）。
type ProjectLookup interface {
	Find(ctx context.Context, id int64) (*project_entity.Project, error)
}

// PeerEvent 是一条推给前端的实时事件帧，带目标指纹供前端按 Peer Tab 路由。
type PeerEvent struct {
	Fingerprint    string          `json:"fingerprint"`
	ConversationID string          `json:"conversationId"`
	Seq            int64           `json:"seq,omitempty"`
	Event          json.RawMessage `json:"event"`
}

// EventName 是 peer_svc → 前端的事件通道名（Wails EventsOn 用同名订阅）。
const EventName = "peer.event"

// RunFreshRequest 是「把一条新对话派到一台具名桌面端」（R18）的入参。peer_svc 把
// agentId / projectId 翻成本机已知的账号级 AgentSyncID 与该项目在本机的 cwd，
// 然后走 wire runtime.run（FreshSession 恒置 true）。返回对端建出的真实会话 id。
type RunFreshRequest struct {
	Fingerprint    string `json:"fingerprint"`
	AgentID        int64  `json:"agentId"`
	ProjectID      int64  `json:"projectId"`
	Title          string `json:"title,omitempty"`
	UserText       string `json:"text"`
	PermissionMode string `json:"permissionMode,omitempty"`
	ProviderKey    string `json:"providerKey,omitempty"`
	ModelKey       string `json:"modelKey,omitempty"`
}

// AttachRequest 是「接入对端一条会话并开始接收实时流」（R19 / R6）的入参。
type AttachRequest struct {
	Fingerprint    string `json:"fingerprint"`
	ConversationID string `json:"conversationId"`
}

// PullRequest 是「按游标拉一页对端会话历史」（R19 / R7）的入参。
type PullRequest struct {
	Fingerprint    string `json:"fingerprint"`
	ConversationID string `json:"conversationId"`
	Cursor         int64  `json:"cursor"`
	Limit          int    `json:"limit,omitempty"`
}

// SteerRequest 是「向已接入的对端会话发一条新消息」（R19 / R9）的入参。
type SteerRequest struct {
	Fingerprint    string `json:"fingerprint"`
	ConversationID string `json:"conversationId"`
	Text           string `json:"text"`
}

// PeerAnswer 是回答提问时的一条答案（与前端 AskAnswerDTO 同形，questionIndex /
// labels / otherText）。
type PeerAnswer struct {
	QuestionIndex int      `json:"questionIndex"`
	Labels        []string `json:"labels"`
	OtherText     string   `json:"otherText,omitempty"`
}

// SubmitAnswerRequest 是「回答对端会话上挂起的用户提问」（R10）的入参。应答携带
// AlreadyHandled：同一待决策已被别的端处理过时如实报告，而非报错或静默成功。
type SubmitAnswerRequest struct {
	Fingerprint    string       `json:"fingerprint"`
	ConversationID string       `json:"conversationId"`
	RequestID      string       `json:"requestId"`
	Answers        []PeerAnswer `json:"answers,omitempty"`
	Skipped        bool         `json:"skipped,omitempty"`
}

// SubmitToolPermissionRequest 是「决定对端会话上挂起的工具权限」（R10）的入参。
type SubmitToolPermissionRequest struct {
	Fingerprint        string `json:"fingerprint"`
	ConversationID     string `json:"conversationId"`
	RequestID          string `json:"requestId"`
	Allow              bool   `json:"allow"`
	AlwaysAllowSession bool   `json:"alwaysAllowSession,omitempty"`
	DenyReason         string `json:"denyReason,omitempty"`
}

// PeerSvc 是桌面端出站对端客户端的窄接口。服务只在内存里持有中继连接与实时订阅，
// 不持久化任何会话内容（硬不变量：server 与对端内容都不落本机盘）。
type PeerSvc interface {
	// ListSessions 列出对端桌面上的全部会话（短连接）。目标 App 未运行时返回
	// server_svc.ErrDesktopAppNotRunning（R2 与「机器离线」区分）。
	ListSessions(ctx context.Context, fingerprint string) (*wire.SessionListResult, error)
	// RunFresh 在远端桌面端上新建一条会话并跑首轮（R18），返回对端真实会话 id。
	RunFresh(ctx context.Context, req RunFreshRequest) (wire.RunAck, error)
	// Attach 接入对端会话并开始接收实时流，返回高水位游标（短连接建立常驻连接）。
	Attach(ctx context.Context, req AttachRequest) (*wire.SessionAttachResult, error)
	// Pull 拉一页游标之后的历史（长连接，会话需已 Attach）。
	Pull(ctx context.Context, req PullRequest) (*wire.SessionPullResult, error)
	// Steer 向已接入的对端会话发一条新消息（长连接）。
	Steer(ctx context.Context, req SteerRequest) error
	// SubmitAnswer 回答对端会话上挂起的提问（长连接），AlreadyHandled 如实上浮。
	SubmitAnswer(ctx context.Context, req SubmitAnswerRequest) (*wire.PeerSessionControlResult, error)
	// SubmitToolPermission 决定对端会话上挂起的工具权限（长连接），语义同 SubmitAnswer。
	SubmitToolPermission(ctx context.Context, req SubmitToolPermissionRequest) (*wire.PeerSessionControlResult, error)
	// Detach 结束本端对一条对端会话的接入（长连接）。只解除本地订阅，不删除对端会话
	// （R19：关闭 Tab 只结束本端接入）。该指纹名下没有其它接入会话时关闭中继连接。
	Detach(ctx context.Context, fingerprint string, conversationID string) error
	// Close 关闭全部对端中继连接（App 退出）。
	Close() error
}

// Default 返回默认实现单例（由 composition root 在 internal/app 注入）。
func Default() PeerSvc { return defaultSvc }

// SetDefault 注入默认实现。
func SetDefault(s PeerSvc) { defaultSvc = s }
