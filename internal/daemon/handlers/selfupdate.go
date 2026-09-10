package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/service/update_svc"
)

// 远程自更新 RPC 复用 update_svc 的解析/替换机器 —— 与 `agentred update` 走的是
// 同一套判定,这一层只加「谁能触发它、并发时怎么排队」这两件本机独有的事。
// 应答只回受理结果(spec「远程一键升级」一节):accepted 之前的每一步都在这次调用
// 里跑完 —— 有更新、目标可写、下载校验都过了才叫「受理」,这样 DOWNLOAD_FAILED /
// NOT_WRITABLE / ALREADY_LATEST 才能作为这次调用的确定性结果、而不是日后才知道的
// 异步进度。

// SelfUpdateRejectReason 是一次远程自更新调用没被受理的原因,与
// agentrewire.AgentredSelfUpdateRejectReason 一一对应;protobuf_registry.go 负责
// 两者之间的翻译,这一层不必知道协议编号。
type SelfUpdateRejectReason int

const (
	// SelfUpdateRejectNone 表示这次调用被受理,没有拒绝原因。
	SelfUpdateRejectNone SelfUpdateRejectReason = iota
	// SelfUpdateRejectActiveTurns 这台机器上还有对话在跑,且请求没有带 force。
	SelfUpdateRejectActiveTurns
	// SelfUpdateRejectInProgress 同一台机器上已经有一次升级在跑。
	SelfUpdateRejectInProgress
	// SelfUpdateRejectNotWritable 目标安装路径不可写。
	SelfUpdateRejectNotWritable
	// SelfUpdateRejectAlreadyLatest 这个通道上已经是最新版本。
	SelfUpdateRejectAlreadyLatest
	// SelfUpdateRejectDownloadFailed 解析发布、下载或校验失败。
	SelfUpdateRejectDownloadFailed
)

// SelfUpdateParams 是一次远程自更新调用的入参,来自 AgentredSelfUpdateRequest。
type SelfUpdateParams struct {
	// Channel 目标通道,空串按 update_svc.NormalizeChannel 的默认值(stable)解读。
	Channel string
	// Force 越过活跃轮次闸门,必须由调用方显式声明(决策 8)。
	Force bool
}

// SelfUpdateResult 是一次调用的受理结果,字段与 AgentredSelfUpdateResponse 一一
// 对应,protobuf_registry.go 负责翻译成 wire 消息。
type SelfUpdateResult struct {
	Accepted      bool
	RejectReason  SelfUpdateRejectReason
	Message       string
	ActiveTurns   int64
	TargetVersion string
}

// SelfUpdateDeps 是 SelfUpdateHandlers 的显式构造入参。Resolve / Apply 与
// cmd/agentred/update.go 的 updateCommandDeps 同形:函数字段而不是接口,是因为它们
// 包着 update_svc 的真实网络 I/O,测试要的是替身而不是要不要 mock 一整个客户端。
type SelfUpdateDeps struct {
	// ActiveTurns 数这台机器此刻有几条会话在跑(update_svc.GuardActiveTurns 的输入)。
	ActiveTurns SelfUpdateActiveTurnsPort
	// Resolve 解析目标通道的最新发布,默认等价于 update_svc.ResolveAgentredRelease
	// 并套上 AGENTRED_RELEASE_BASE_URL(与命令行同一个换源变量)。
	Resolve func(ctx context.Context, opts update_svc.AgentredReleaseOptions) (*update_svc.AgentredRelease, error)
	// Apply 下载、校验、替换,默认等价于 update_svc.ApplyAgentredUpdate。
	Apply func(ctx context.Context, release *update_svc.AgentredRelease, opts update_svc.ApplyAgentredUpdateOptions) error
	// Restart 在替换成功之后调用,让这台 daemon 被监管者重新拉起来(决策 7)。
	// 它在一个独立的 goroutine 里异步触发 —— 生产实现最终会终止进程,必须等这次
	// RPC 的应答先被 protorpc 写出去,调用方才可能读到 accepted=true;调用时机因此
	// 只保证「受理之后」,不保证具体早晚。测试注入的替身只需要记一次调用,不会真的
	// 退出进程。nil 表示不触发(测试未显式关心这一步时的默认值)。
	Restart func()
}

// SelfUpdateHandlers 实现远程自更新 RPC 的受理判定。它是 Daemon 级、单例的
// (由 protobuf_registry.go 构造一次并在闭包里持有):并发互斥要覆盖同一台机器上的
// 所有调用,per-connection 的实例会让第二条连接上的第二次调用绕过第一条连接上还没
// 完成的那一次。
type SelfUpdateHandlers struct {
	deps SelfUpdateDeps
	mu   sync.Mutex
}

// NewSelfUpdateHandlers 组装远程自更新 handler。
func NewSelfUpdateHandlers(deps SelfUpdateDeps) *SelfUpdateHandlers {
	return &SelfUpdateHandlers{deps: deps}
}

// Update 判定这次远程自更新调用是受理还是拒绝,受理时在返回之前就已经把升级做完
// (下载、校验、替换),只是还没触发重启。
//
// 顺序是刻意的:
//  1. 并发闸门最先判——不管调用方此刻处在哪一步,第二次调用都必须立刻拿到
//     IN_PROGRESS,而不是排队等着一起跑或者双写同一个目标文件。
//  2. 活跃轮次闸门——不越过它,一个字节都不该下载(与命令行同一条纪律)。
//  3. 解析发布——已经是最新就没有必要再往下走。
//  4. 下载、校验、替换——这一步本身失败时不可写与其它失败要分得清楚,好让界面按
//     人话原因分别呈现(spec 决策 22)。
func (h *SelfUpdateHandlers) Update(ctx context.Context, params SelfUpdateParams) (SelfUpdateResult, error) {
	if !h.mu.TryLock() {
		return SelfUpdateResult{
			RejectReason: SelfUpdateRejectInProgress,
			Message:      "an agentred upgrade is already in progress on this machine",
		}, nil
	}
	// 闸门开到哪一刻,由这一次到底有没有安排重启决定(见下面 Restart 处)。默认开着:
	// 每一条没有受理的出路都必须把它还回去,否则一次失败的升级会把机器永久锁死。
	holdUntilRestart := false
	defer func() {
		if !holdUntilRestart {
			h.mu.Unlock()
		}
	}()

	count, err := h.deps.ActiveTurns.CountRunning(ctx)
	if err != nil {
		return SelfUpdateResult{}, fmt.Errorf("count running conversations: %w", err)
	}
	if guardErr := update_svc.GuardActiveTurns(count, params.Force); guardErr != nil {
		var active *update_svc.ActiveTurnsError
		if !errors.As(guardErr, &active) {
			// GuardActiveTurns 只在拒绝时才返回错误,而它唯一的错误类型就是
			// ActiveTurnsError;走到这里说明它的实现变了,按未知失败处理而不是崩溃。
			return SelfUpdateResult{RejectReason: SelfUpdateRejectActiveTurns, Message: guardErr.Error()}, nil
		}
		return SelfUpdateResult{
			RejectReason: SelfUpdateRejectActiveTurns,
			Message:      guardErr.Error(),
			ActiveTurns:  active.Count,
		}, nil
	}

	release, err := h.deps.Resolve(ctx, update_svc.AgentredReleaseOptions{Channel: params.Channel})
	if err != nil {
		return SelfUpdateResult{RejectReason: SelfUpdateRejectDownloadFailed, Message: err.Error()}, nil
	}
	if !release.HasUpdate {
		return SelfUpdateResult{
			RejectReason:  SelfUpdateRejectAlreadyLatest,
			Message:       fmt.Sprintf("agentred is already up to date on the %s channel.", release.Channel),
			TargetVersion: release.LatestVersion,
		}, nil
	}

	if err := h.deps.Apply(ctx, release, update_svc.ApplyAgentredUpdateOptions{}); err != nil {
		reason := SelfUpdateRejectDownloadFailed
		var notWritable *update_svc.TargetNotWritableError
		if errors.As(err, &notWritable) {
			reason = SelfUpdateRejectNotWritable
		}
		return SelfUpdateResult{RejectReason: reason, Message: err.Error(), TargetVersion: release.LatestVersion}, nil
	}

	if h.deps.Restart != nil {
		// 闸门不在这里松:二进制已经换掉,进程还在等着退出重启(Restart 是异步的,它给
		// 应答留出被写回连接的时间)。这段时间这台机器仍然处在「一次升级正在进行中」
		// —— 松开它,窗口里的第二次调用会重新解析发布(还在跑的进程报的仍是旧版本,
		// HasUpdate 因而仍为真)、重新下载并再替换一遍,而第一次安排的那次退出会在中途
		// 把进程杀掉,连 ApplyAgentredUpdate 清理下载与解压目录的 defer 都跑不到。
		//
		// 一直持有是对的:这个进程接下来只会退出,而重新拉起来的是一个全新的
		// SelfUpdateHandlers。没有安排重启时(Restart 为 nil,未接线的装配)则照常松开
		// —— 那时并没有什么在等着生效,永久上锁只会让这台机器再也升不了级。
		holdUntilRestart = true
		go h.deps.Restart()
	}
	return SelfUpdateResult{Accepted: true, TargetVersion: release.LatestVersion}, nil
}

// DefaultSelfUpdateResolve 是 SelfUpdateDeps.Resolve 的生产实现(供 daemon 包在
// protobuf_registry.go 里装配):解析目标通道的最新发布,并尊重与
// scripts/install.sh、`agentred update` 同名的换源变量。
func DefaultSelfUpdateResolve(ctx context.Context, opts update_svc.AgentredReleaseOptions) (*update_svc.AgentredRelease, error) {
	opts.BaseURL = os.Getenv(update_svc.AgentredReleaseBaseURLEnv)
	return update_svc.ResolveAgentredRelease(ctx, opts)
}

// DefaultSelfUpdateApply 是 SelfUpdateDeps.Apply 的生产实现,与
// cmd/agentred/update.go 的 applyAgentredUpdate 等价:目标路径留空,取当前可执行
// 文件(符号链接已解析)。
func DefaultSelfUpdateApply(ctx context.Context, release *update_svc.AgentredRelease, opts update_svc.ApplyAgentredUpdateOptions) error {
	return update_svc.ApplyAgentredUpdate(ctx, release, opts)
}

// DefaultSelfUpdateRestart 是 SelfUpdateDeps.Restart 的生产实现:等应答有机会被
// protorpc 写出连接之后,让这个进程退出——有监管者(systemd / launchd / 容器的
// restart policy)会把它重新拉起来并带着刚换上的新二进制;没有监管者时它就留在
// 退出状态,这是决策 7 与「远程一键升级」一节写明的已知代价,界面据此按超时失败
// 呈现,而不是这里再去猜有没有监管者。
//
// 这里选择直接退出而不是调用 ServiceManager.Restart:那一套只活在 cmd/agentred
// (裸二进制、短命进程,专门为了从外部重启另一个正在跑的 daemon 而写),daemon 自己
// 正在替换的是自己这个进程,能做的只有退出——退出即是它对「重启」唯一做得到的贡献,
// 装了服务的那一半留给该服务单元自身的重启策略去接。正因为如此,退出码不能是 0:
// 见 SelfUpdateRestartExitCode。
func DefaultSelfUpdateRestart() {
	time.Sleep(defaultSelfUpdateRestartDelay)
	os.Exit(SelfUpdateRestartExitCode)
}

// SelfUpdateRestartExitCode 是 daemon 换完二进制之后退出所用的状态码。
//
// 它必须**非零**。装了服务的那台机器上,监管者是不是把 daemon 拉回来完全取决于这个
// 数:本仓库写给 systemd 的 unit 是 `Restart=on-failure`
// (cmd/agentred/service_systemd.go),Windows 那份计划任务同样只在任务失败后重试 ——
// 干净退出(0)在它们眼里是「这个进程自己不想跑了」,于是一次远程一键升级会把机器停在
// 升级后的第一秒,而 spec 只把「退出之后不会回来」这条代价留给前台裸跑。launchd 的
// KeepAlive 与容器的 restart policy 对退出码不敏感,非零对它们无害。
//
// 取 75(EX_TEMPFAIL,「暂时性失败,请重试」)而不是随手一个 1:这次退出确实不是正常
// 收场,但也不是故障——它要表达的正是「把我重新拉起来」。
// cmd/agentred/selfupdate_restart_test.go 把这个常量与那份 unit 的重启策略钉在一起。
const SelfUpdateRestartExitCode = 75

// defaultSelfUpdateRestartDelay 是退出前的缓冲:protorpc 把应答写回连接是异步的,
// 这段时间给它一个机会真的把 accepted=true 送到对端手上,而不是应答还没出门进程就
// 没了。
const defaultSelfUpdateRestartDelay = 500 * time.Millisecond
