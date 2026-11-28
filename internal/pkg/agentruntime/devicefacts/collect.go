// Package devicefacts 采集一台设备「现在能干什么」的事实,并决定何时把它交给账号
// server(规格「设备与设备情况上报 › 上报的内容」)。桌面端与 agentred 共用这一份:
// 采什么、怎么判登录态、什么时候报,两个宿主只各自接上自己的来源与上报口。
//
// 它是叶子:不碰数据库、不 import service / repository。宿主把引擎配置、凭据有无、
// 本机路径以函数交进来(Sources),上报口同样是一个函数(NewReporter 的 upload)。
//
// **事实里没有任何凭据、token 或环境变量值。** 凭据只以布尔出现;登录态检测只看
// CLI 自己的凭据在不在,从不读凭据文件的正文。
package devicefacts

import (
	"context"
	"fmt"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// 有设备事实的引擎配置类型。其余类型(Claude Code、Codex 等)在这台设备上能不能跑,
// 已经由运行时那一项回答;builtin 与 OpenClaw 只能在桌面端运行是运行时自身的限制
// (规格决策 12),不是要探测的事实。
const (
	runtimeACP      = "acp"
	runtimeHermes   = "hermes"
	runtimeOpenClaw = "openclaw"
)

// Engine 是一份要在本机核对的账号引擎配置。
type Engine struct {
	SyncID string
	// Runtime 是引擎配置的运行时类型。
	Runtime string
	// ACPCommand 只对 acp 有意义:外部 ACP Agent 的可执行文件。
	ACPCommand string
	// HermesURL 只对 hermes 有意义:Hermes 凭据按 serve 地址存放。
	HermesURL string
	// OpenClawGatewayURL 只对 openclaw 有意义:上次连接按网关地址记。
	OpenClawGatewayURL string
}

// Sources 是一台设备的事实来源。Runtimes 与 Engines 必填;其余为 nil 表示这个宿主
// 没有这一类(agentred 没有本机项目路径)。
type Sources struct {
	// Runtimes 探四种已知运行时(通常是 RuntimeProbe.Probe)。
	Runtimes func(ctx context.Context) []syncwire.RuntimeFact
	// Engines 列出账号下的活引擎配置。
	Engines func(ctx context.Context) ([]Engine, error)
	// ResolveCommand 判一条 ACP 命令在这台设备上能否解析。
	ResolveCommand func(command string) bool
	// HasCredential 判这台设备上存没存这份 Hermes / OpenClaw 配置的凭据。
	HasCredential func(ctx context.Context, e Engine) bool
	// LastConnection 这台设备最近一次连这份配置端点的结果与时刻(ConnectionLog.Last)。
	LastConnection func(e Engine) (status string, at int64)
	// SkillPacks 列出某个运行时(用找到的那个可执行文件)已装的技能包 ID。
	SkillPacks func(ctx context.Context, runtime, cliPath string) ([]string, error)
	// LocalPaths 列出这台设备上配了本机路径的项目。
	LocalPaths func(ctx context.Context) ([]syncwire.LocalPathItem, error)
	Now        func() time.Time
}

// Collect 采一份完整的设备情况。
//
// 读不出引擎配置或本机路径时报错,而不是交出一份空情况:那份空情况上报之后,server
// 与派发函数会把它读成「这台设备上没有任何 ACP 能解析、没有任何项目」,把会话从一台
// 本来跑得了的设备上挪走。调用方据此保留上一份。技能包列不出来只少那一组 —— 缺包
// 不阻塞派发(规格决策 9)。
func Collect(ctx context.Context, src Sources) (syncwire.DeviceFactsReport, error) {
	now := time.Now
	if src.Now != nil {
		now = src.Now
	}
	report := syncwire.DeviceFactsReport{
		Runtimes:       []syncwire.RuntimeFact{},
		ACPEngines:     []syncwire.ACPEngineFact{},
		ServiceEngines: []syncwire.ServiceEngineFact{},
		SkillPacks:     []syncwire.RuntimeSkillPacks{},
		LocalPaths:     []syncwire.LocalPathItem{},
	}

	engines, err := src.Engines(ctx)
	if err != nil {
		return syncwire.DeviceFactsReport{}, fmt.Errorf("list engine profiles: %w", err)
	}
	if src.LocalPaths != nil {
		paths, err := src.LocalPaths(ctx)
		if err != nil {
			return syncwire.DeviceFactsReport{}, fmt.Errorf("list local project paths: %w", err)
		}
		report.LocalPaths = append(report.LocalPaths, paths...)
	}

	report.Runtimes = append(report.Runtimes, src.Runtimes(ctx)...)
	for _, e := range engines {
		switch e.Runtime {
		case runtimeACP:
			resolvable := src.ResolveCommand != nil && src.ResolveCommand(e.ACPCommand)
			report.ACPEngines = append(report.ACPEngines, syncwire.ACPEngineFact{EngineSyncID: e.SyncID, Resolvable: resolvable})
		case runtimeHermes, runtimeOpenClaw:
			present := src.HasCredential != nil && src.HasCredential(ctx, e)
			status, at := syncwire.ConnectionNever, int64(0)
			if src.LastConnection != nil {
				status, at = src.LastConnection(e)
			}
			report.ServiceEngines = append(report.ServiceEngines, syncwire.ServiceEngineFact{
				EngineSyncID: e.SyncID, Runtime: e.Runtime, CredentialPresent: present,
				LastConnection: status, LastConnectionAt: at,
			})
		}
	}
	report.SkillPacks = append(report.SkillPacks, collectSkillPacks(ctx, src, report.Runtimes)...)
	report.CollectedAt = now().UnixMilli()
	return report, nil
}

func collectSkillPacks(ctx context.Context, src Sources, runtimes []syncwire.RuntimeFact) []syncwire.RuntimeSkillPacks {
	if src.SkillPacks == nil {
		return nil
	}
	var out []syncwire.RuntimeSkillPacks
	for _, rt := range runtimes {
		if !rt.Found {
			continue
		}
		ids, err := src.SkillPacks(ctx, rt.Runtime, rt.Path)
		if err != nil {
			logger.Ctx(ctx).Debug("devicefacts.Collect: skill pack discovery failed, omitting runtime",
				zap.String("runtime", rt.Runtime), zap.Error(err))
			continue
		}
		if len(ids) == 0 {
			continue
		}
		out = append(out, syncwire.RuntimeSkillPacks{Runtime: rt.Runtime, PackIDs: ids})
	}
	return out
}
