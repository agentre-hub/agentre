package remote_device_svc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// UpgradeCallTimeout 是这一次调用等应答的预算。
//
// 它必须与 protorpc.DefaultCallTimeout(60 秒,给的是「问一句答一句」的方法)分开:
// daemon 的受理判定把解析发布、下载、校验、替换**全部**跑完才应答(见
// handlers.SelfUpdateHandlers.Update 的顺序注释)——这正是 DOWNLOAD_FAILED /
// NOT_WRITABLE / ALREADY_LATEST 能作为这次调用确定性结果的原因。换一个几十 MB 的
// 二进制不会在 60 秒里跑完,让兜底生效等于每一次真能成功的升级都在本端超时:下面那
// 条 RemoteDeviceTimeout 分支把它报成失败,而那台机器照样升完重启,界面从此停在一个
// 假的失败上。
//
// 取 5 分钟,与设备行那扇「升级中」的窗口同宽(use-device-upgrade 的 TIMEOUT_MS),
// 也与 server 那一侧的 mirror_svc.upgradeCallTimeout 相同 —— 两个宿主对同一个 RPC
// 给同一个预算。不用 protorpc.WithoutCallTimeout:一次由用户点出来的动作不该无限期
// 挂着,一个说得清的上限比「一直等」更好交代。
const UpgradeCallTimeout = 5 * time.Minute

// UpgradeRejectReason 是远程一键升级调用没被受理的原因,取值与
// agentrewire.AgentredSelfUpdateRejectReason 一一对应(daemon 侧的
// handlers.SelfUpdateRejectReason 也是同一张表的第三份复述)。用字符串常量而不是
// 直接透传 wire 枚举,是为了让前端(经 wailsjs 生成的 TS)不必知道 protobuf 编号。
type UpgradeRejectReason string

const (
	// UpgradeRejectNone 表示这次调用被受理,没有拒绝原因。
	UpgradeRejectNone UpgradeRejectReason = ""
	// UpgradeRejectActiveTurns 这台机器上还有对话在跑,且请求没有带 force。
	UpgradeRejectActiveTurns UpgradeRejectReason = "active_turns"
	// UpgradeRejectInProgress 同一台机器上已经有一次升级在跑。
	UpgradeRejectInProgress UpgradeRejectReason = "in_progress"
	// UpgradeRejectNotWritable 目标安装路径不可写。
	UpgradeRejectNotWritable UpgradeRejectReason = "not_writable"
	// UpgradeRejectAlreadyLatest 这个通道上已经是最新版本。
	UpgradeRejectAlreadyLatest UpgradeRejectReason = "already_latest"
	// UpgradeRejectDownloadFailed 解析发布、下载或校验失败。
	UpgradeRejectDownloadFailed UpgradeRejectReason = "download_failed"
)

// upgradeRejectReasons 把 wire 枚举翻成这一层的字符串常量。
var upgradeRejectReasons = map[agentrewire.AgentredSelfUpdateRejectReason]UpgradeRejectReason{
	agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_UNSPECIFIED:     UpgradeRejectNone,
	agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_ACTIVE_TURNS:    UpgradeRejectActiveTurns,
	agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_IN_PROGRESS:     UpgradeRejectInProgress,
	agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_NOT_WRITABLE:    UpgradeRejectNotWritable,
	agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_ALREADY_LATEST:  UpgradeRejectAlreadyLatest,
	agentrewire.AgentredSelfUpdateRejectReason_AGENTRED_SELF_UPDATE_REJECT_REASON_DOWNLOAD_FAILED: UpgradeRejectDownloadFailed,
}

// UpgradeResult 是一次远程一键升级调用的受理结果,字段与
// agentrewire.AgentredSelfUpdateResponse 一一对应。
type UpgradeResult struct {
	Accepted bool `json:"accepted"`
	// RejectReason 为空串表示 Accepted。
	RejectReason UpgradeRejectReason `json:"rejectReason,omitempty"`
	// Message 是拒绝原因的人话版本,逐字来自 daemon(与 `agentred update`
	// 命令行同一句话 —— 决策 22),调用方原样展示而不是自己另编一套措辞。
	Message string `json:"message,omitempty"`
	// ActiveTurns 只在 RejectReason 是 active_turns 时非零。
	ActiveTurns int32 `json:"activeTurns,omitempty"`
	// TargetVersion 是 daemon 解析出来准备安装的版本;受理时非空,部分拒绝原因
	// (如 already_latest)也会带上。
	TargetVersion string `json:"targetVersion,omitempty"`
}

// Upgrade 借一条到 deviceID 的连接,发起远程一键升级 RPC。它只负责发这一次调用
// 并把应答翻成 UpgradeResult——升级中→成功/超时失败的推断留给调用方(桌面端从
// 版本号变化判定,spec「远程一键升级」)。
func (s *service) Upgrade(ctx context.Context, deviceID int64, channel string, force bool) (*UpgradeResult, error) {
	if s.pool == nil {
		return nil, errors.New("remote device connection pool unavailable")
	}
	lease, err := s.pool.Borrow(ctx, deviceID)
	if err != nil {
		return nil, mapSyncBorrowError(ctx, err)
	}
	defer lease.Release()

	// 期限在这里设,而不是留给 protorpc 的兜底:调用方自己设过 deadline 时兜底不生效
	// (兜底是地板不是天花板),所以「这一次调用该等多久」必须由知道它在等什么的这一层
	// 说出来。
	ctx, cancel := context.WithTimeout(ctx, UpgradeCallTimeout)
	defer cancel()
	resp, err := lease.SelfUpdate(ctx, &agentrewire.AgentredSelfUpdateRequest{Channel: channel, Force: force})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, i18n.NewError(ctx, code.RemoteDeviceTimeout)
		}
		return nil, fmt.Errorf("remote agentred.self_update: %w", err)
	}
	return &UpgradeResult{
		Accepted:      resp.GetAccepted(),
		RejectReason:  upgradeRejectReasons[resp.GetRejectReason()],
		Message:       resp.GetMessage(),
		ActiveTurns:   resp.GetActiveTurns(),
		TargetVersion: resp.GetTargetVersion(),
	}, nil
}
