// Package transcriptimport 是 agentred daemon 端 transcriptimport.* RPC 的 handler
// 实现:发现候选、打开元信息、分页取回轮次(本文件),以及在本机执行一次导入
// (execute.go —— 这一族里唯一写库的一条路径)。
//
// **不写第二套解析**(spec 决策 17):daemon 与桌面是同一个 Go 模块,
// internal/daemon/runtime_imports.go 已经 blank import 了三个 runtime 包,它们在
// init() 里把自己的磁盘读取器 Register 进 internal/pkg/transcriptimport 的注册表。
// 本包只是那份读取器的一层 RPC 壳 —— 方言知识一份,两端行为不会分叉。
//
// 只读的这三个方法有两条形状约束:
//   - **无跨调用句柄**。Open / Turns 每次自己开、自己关,daemon 不持有转录状态。
//     句柄能省下重复解析,但代价是断线后要靠超时回收,而这条路上的收益换不来那个
//     复杂度。
//   - **分页而不是全量**。读取器的 Turns 是推式流,本包也只把「这一页」攒在内存里:
//     够了就从 yield 返回哨兵停掉回放,不把整份转录解完(更不整份塞进一帧 —— 帧
//     上限 16 MiB)。
package transcriptimport

import (
	"context"
	"errors"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	pkgimport "github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
)

// Options 注入测试 hook。生产用 NewHandlers(Options{}) 走注册表快照。
type Options struct {
	// Sources 回答「这台机器上注册了哪些磁盘读取器」;为 nil 时读注册表。
	Sources func() []pkgimport.Source
	// MaxPageBytes 一页的字节预算(近似值),<=0 用 defaultMaxPageBytes。
	MaxPageBytes int
	// Sessions / Journal / JournalPurge 是**执行侧**(Execute)要用的存储口,
	// 只读的三个方法用不到它们。留空时 Execute 报 internal 而不是静默假装导完了。
	Sessions     SessionStore
	Journal      Journal
	JournalPurge JournalPurger
	// ClaimedAccountID 交出这台 daemon 此刻归属的账号,用来判定调用方有没有资格
	// 把会话落在**点名的 origin**名下(与补齐族、删除族同一个门)。
	ClaimedAccountID func() string
}

// defaultMaxPageBytes 是一页的字节预算。压着 protorpc 的 16 MiB 帧上限留足余量:
// 一轮里塞着几 MB 的工具结果并不罕见,只按轮数分页会让某一页整帧发不出去,而
// 发不出去的表现是整条导入失败,不是少一页。
const defaultMaxPageBytes = 4 << 20

// Handlers 持有 transcriptimport RPC 方法集合。
type Handlers struct {
	sources          func() []pkgimport.Source
	maxPageBytes     int
	sessions         SessionStore
	journal          Journal
	journalPurge     JournalPurger
	claimedAccountID func() string
}

// NewHandlers 构造 Handlers,未填字段使用安全默认值。
func NewHandlers(opts Options) *Handlers {
	h := &Handlers{
		sources: opts.Sources, maxPageBytes: opts.MaxPageBytes,
		sessions: opts.Sessions, journal: opts.Journal, journalPurge: opts.JournalPurge,
		claimedAccountID: opts.ClaimedAccountID,
	}
	if h.sources == nil {
		h.sources = pkgimport.Sources
	}
	if h.maxPageBytes <= 0 {
		h.maxPageBytes = defaultMaxPageBytes
	}
	return h
}

// Scan 按后端分别扫描:**任一后端失败不影响其余后端出结果**(spec「发现、去重与
// 来源」)。一档读不动就在它自己那一档报 unavailable + 原因,其余照常 ok ——
// 整体报错的话,装了三个 CLI 的机器少装一个就什么都看不见。
func (h *Handlers) Scan(ctx context.Context, params wire.ScanParams) (*wire.ScanResult, error) {
	out := &wire.ScanResult{Backends: []wire.BackendScan{}}
	for _, src := range h.scanTargets(params.Backends) {
		if src.source == nil {
			out.Backends = append(out.Backends, wire.BackendScan{
				Backend: src.backend,
				Status:  wire.StatusUnavailable,
				Reason:  wire.ErrBackendUnavailable.Error(),
			})
			continue
		}
		got, err := src.source.Scan(ctx, params.Filter)
		if err != nil {
			// 隐私红线:只记后端与失败原因,不记转录内容。
			logger.Ctx(ctx).Warn("daemon.transcriptimport.Scan: backend scan failed",
				zap.String("backendType", src.backend), zap.Error(err))
			out.Backends = append(out.Backends, wire.BackendScan{
				Backend: src.backend, Status: wire.StatusUnavailable, Reason: err.Error(),
			})
			continue
		}
		out.Backends = append(out.Backends, wire.BackendScan{
			Backend: src.backend, Status: wire.StatusOK, Candidates: got,
		})
	}
	return out, nil
}

// Open 取一份转录的元信息,取完立刻关掉。
func (h *Handlers) Open(ctx context.Context, params wire.OpenParams) (*wire.OpenResult, error) {
	tr, err := h.openTranscript(ctx, params.Backend, params.Locator)
	if err != nil {
		return nil, err
	}
	defer closeTranscript(ctx, tr)
	return &wire.OpenResult{Meta: tr.Meta()}, nil
}

// errPageFull 是「这一页够了」的内部哨兵:契约里 yield 返回非 nil 就立刻停止回放,
// 分页正是靠它不把整份转录解完。
var errPageFull = errors.New("transcriptimport: page budget reached")

// Turns 取 [StartIndex, StartIndex+MaxTurns) 这一页。
//
// HasMore 靠**多探一轮**得出:够这一页之后再收到一轮就说明后面还有,然后立刻停。
// 不靠「这一页满了」推断 —— 按字节预算提前收的页同样不满,但后面还有。
func (h *Handlers) Turns(ctx context.Context, params wire.TurnsParams) (*wire.TurnsResult, error) {
	limit := params.MaxTurns
	if limit <= 0 {
		limit = wire.DefaultMaxTurns
	}
	if limit > wire.MaxTurnsPerPage {
		limit = wire.MaxTurnsPerPage
	}
	start := params.StartIndex
	if start < 0 {
		start = 0
	}

	tr, err := h.openTranscript(ctx, params.Backend, params.Locator)
	if err != nil {
		return nil, err
	}
	defer closeTranscript(ctx, tr)

	out := &wire.TurnsResult{Turns: []pkgimport.Turn{}, NextIndex: start}
	seen := 0
	bytes := 0
	replayErr := tr.Turns(ctx, func(turn pkgimport.Turn) error {
		index := seen
		seen++
		if index < start {
			return nil
		}
		if len(out.Turns) >= limit || bytes >= h.maxPageBytes {
			out.HasMore = true
			return errPageFull
		}
		// Index 一律按回放顺序重排:读取器给的 Index 只在自己那一份里有意义,
		// 而翻页的游标必须与「跳过多少轮」同一把尺子。
		turn.Index = index
		out.Turns = append(out.Turns, turn)
		out.NextIndex = index + 1
		bytes += approxTurnBytes(turn)
		return nil
	})
	if replayErr != nil && !errors.Is(replayErr, errPageFull) {
		return nil, fmt.Errorf("transcriptimport: replay turns: %w", replayErr)
	}
	return out, nil
}

// ── internals ───────────────────────────────────────────────────────────────

type scanTarget struct {
	backend string
	source  pkgimport.Source
}

// scanTargets 定出这次要扫哪几档。请求点名了后端时,**点到但本机没有读取器的那一
// 档也要出现在结果里**(status=unavailable):静默少一档会让调用方以为那台机器上
// 就是没有会话。
func (h *Handlers) scanTargets(backends []string) []scanTarget {
	registered := h.sources()
	if len(backends) == 0 {
		out := make([]scanTarget, 0, len(registered))
		for _, src := range registered {
			if src == nil {
				continue
			}
			out = append(out, scanTarget{backend: string(src.Backend()), source: src})
		}
		return out
	}
	out := make([]scanTarget, 0, len(backends))
	for _, name := range backends {
		out = append(out, scanTarget{backend: name, source: h.sourceFor(agent_backend_entity.BackendType(name))})
	}
	return out
}

func (h *Handlers) sourceFor(backend agent_backend_entity.BackendType) pkgimport.Source {
	for _, src := range h.sources() {
		if src != nil && src.Backend() == backend {
			return src
		}
	}
	return nil
}

// openTranscript 把 {后端, 定位符} 变成一份可回放的转录,并把两种"空"翻成 typed
// sentinel:没有读取器 → ErrBackendUnavailable(可预期的空),打不开 →
// ErrTranscriptOpen(文件已删 / 已损坏 / 定位符越界)。
func (h *Handlers) openTranscript(ctx context.Context, backend, locator string) (pkgimport.Transcript, error) {
	if backend == "" || locator == "" {
		return nil, rpcerror.ErrInvalidParams
	}
	src := h.sourceFor(agent_backend_entity.BackendType(backend))
	if src == nil {
		return nil, wire.ErrBackendUnavailable
	}
	tr, err := src.Open(ctx, pkgimport.Locator(locator))
	if err != nil {
		logger.Ctx(ctx).Warn("daemon.transcriptimport.open: transcript open failed",
			zap.String("backendType", backend), zap.Error(err))
		return nil, fmt.Errorf("%w: %s", wire.ErrTranscriptOpen, err.Error())
	}
	if tr == nil {
		return nil, wire.ErrTranscriptOpen
	}
	return tr, nil
}

func closeTranscript(ctx context.Context, tr pkgimport.Transcript) {
	if err := tr.Close(); err != nil {
		logger.Ctx(ctx).Warn("daemon.transcriptimport.close: transcript close failed", zap.Error(err))
	}
}

// approxTurnBytes 估这一轮上线大约多少字节。只数正文与载荷长度 —— 分页预算要的是
// 量级,不是精确值,为此把整轮序列化一遍反而是本末倒置。
func approxTurnBytes(turn pkgimport.Turn) int {
	total := len(turn.UserText) + len(turn.ErrorText) + len(turn.Model) + len(turn.ForkAnchor)
	for _, img := range turn.UserImages {
		total += len(img.Source.Inline) + len(img.Source.URL)
	}
	for _, ev := range turn.Events {
		total += approxEventBytes(ev)
	}
	return total
}

// approxEventBytes 只认几个真正可能很大的载荷:工具入参与工具结果、正文与思维增量。
// 其余事件是几十字节的元信息,量级上不影响分页判断。
func approxEventBytes(ev agentruntime.Event) int {
	switch value := ev.(type) {
	case agentruntime.TextDelta:
		return len(value.Text)
	case agentruntime.ThinkingDelta:
		return len(value.Text)
	case agentruntime.UserMessageEvent:
		return len(value.Text)
	case agentruntime.ToolCall:
		return len(value.Input) + len(value.Name)
	case agentruntime.ToolResult:
		return len(value.Content) + len(value.Meta)
	}
	return 64
}
