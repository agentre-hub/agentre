package transcriptimport

// execute.go 是这一族里唯一**写库**的一条路径:把一份磁盘转录落成一条归**这台
// 机器**执行的会话。
//
// 为什么是机器在导,而不是浏览器:agentre-server 从不拥有会话,它只镜像会话 ——
// 「新对话」的既定形状是浏览器铸号、机器执行、内容经 SESSION_LIST / SESSION_PULL
// 流上去。导入照同一条形状走,导出来的会话因此和别的会话一模一样地镜像上去,
// 不需要第二条通路。
//
// 落库顺序刻意是「先清同号残留 → 再逐轮落日志 → 最后写身份行」:
//   - 身份行是判重的锚点,它一旦在库里就代表这条导入**完整**。反过来先写身份行的话,
//     回放中途失败会留下一条看着已经导完、实际只有半截转录的会话,而下一次同号重来
//     会被判成「已导过」直接指回去 —— 那半截转录再也补不齐了。
//   - 中途失败留下的是一段没有主人的日志,同号重来先把它清掉,两次回放因此不会首尾
//     相接叠成一份双倍长的转录。
//
// 不开事务是有意的:通知日志的 Append 每条自行分配 seq(见 notification_repo 的
// appendSQL —— 单条语句里取 MAX(seq)+1 并写入),把整份转录裹进一个事务会让这条
// 长写锁住整个库,而这台 daemon 上别的会话正在实时落它们自己的通知。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	runtimewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	pkgimport "github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
)

// SessionStore 是会话身份行在执行侧要用到的那三件:按对端判重、按号查占用、建行。
// 按 ISP 在消费方声明 —— daemon 那份实现同时满足 handlers 的读写端口,这里只取本
// 路径真正调用的那几个方法。
type SessionStore interface {
	Find(ctx context.Context, peerFingerprint, peerSessionID string) (*handlers.SessionRecord, error)
	List(ctx context.Context, peerFingerprint, keyword string) ([]handlers.SessionRecord, error)
	Start(ctx context.Context, rec handlers.SessionRecord) error
}

// Journal 落库一条「本该发给客户端的通知」并返回库分配的 seq(同
// handlers.JournalPort)。回放出的每一帧都从这里进库,SESSION_PULL 因此原样服务
// 得出 —— 本包不另开第二本日志。
type Journal interface {
	Append(ctx context.Context, peerFingerprint, peerSessionID string, payload []byte) (int64, error)
}

// JournalPurger 清空某会话的全部日志(同 handlers.JournalPurgePort)。本路径只在
// 「同号残留」这一处用它,见文件头的落库顺序。
type JournalPurger interface {
	DeleteAll(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error)
}

// Execute 在这台机器上执行一次导入。
func (h *Handlers) Execute(ctx context.Context, params wire.ExecuteParams) (*wire.ExecuteResult, error) {
	if err := handlers.ErrInvalidConversationID(params.ConversationID); err != nil {
		return nil, err
	}
	if h.sessions == nil || h.journal == nil {
		// 没接存储就没有「执行」可言。静默回一个空结果会让调用方以为导完了,
		// 而库里一行都没有。
		logger.Ctx(ctx).Error("daemon.transcriptimport.Execute: storage not wired",
			zap.String("conversationId", params.ConversationID))
		return nil, rpcerror.ErrInternal
	}
	peer, err := handlers.ResolveSessionPeer(ctx, params.PeerFingerprint, h.claimedAccountID)
	if err != nil {
		return nil, err
	}
	transcript, err := h.openTranscript(ctx, params.Backend, params.Locator)
	if err != nil {
		return nil, err
	}
	defer closeTranscript(ctx, transcript)
	meta := transcript.Meta()

	// 身份列本来就是 TEXT:对话身份原样落进去,从前那一圈 int64↔string 往返消失了。
	peerSessionID := params.ConversationID
	existing, err := h.findImported(ctx, peer, peerSessionID, meta.ProviderSessionID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		// 交回的是**库里那条**的身份,未必等于调用方刚铸的那个(契约见
		// wire.ExecuteResult):这份转录早就导过一次,收敛到已有的那条对话上。
		logger.Ctx(ctx).Info("daemon.transcriptimport.Execute: already imported",
			zap.String("backendType", params.Backend),
			zap.String("conversationId", existing.PeerSessionID),
			zap.String("providerSessionId", meta.ProviderSessionID))
		return &wire.ExecuteResult{
			ConversationID: existing.PeerSessionID, ProviderSessionID: existing.ProviderSessionID,
			Cwd: existing.Cwd, Title: existing.Title, AlreadyImported: true,
		}, nil
	}

	if err := h.clearLeftoverJournal(ctx, peer, peerSessionID); err != nil {
		return nil, err
	}
	replay := &replayCounters{}
	turnErr := transcript.Turns(ctx, func(turn pkgimport.Turn) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return h.journalTurn(ctx, peer, peerSessionID, meta, turn, replay)
	})
	if turnErr != nil {
		// 这里就是这条链路上「能判定它失败了」的那一层:上面只剩 RPC 壳。
		logger.Ctx(ctx).Error("daemon.transcriptimport.Execute: replay failed",
			zap.String("backendType", params.Backend), zap.String("conversationId", params.ConversationID),
			zap.Int("turns", replay.turns), zap.Error(turnErr))
		return nil, fmt.Errorf("transcriptimport: replay turns: %w", turnErr)
	}

	title := strings.TrimSpace(meta.Title)
	record := handlers.SessionRecord{
		PeerFingerprint: peer,
		PeerSessionID:   peerSessionID,
		AgentID:         params.AgentID,
		// 工作目录与 provider 会话身份取转录的:下一轮要在**那个目录**里、对着
		// **那条 provider 会话**续跑,任何一格错位都会让续跑起在别处。
		Cwd:               meta.Cwd,
		BackendType:       params.Backend,
		LifecycleState:    runtimewire.SessionLifecycleIdle,
		Title:             title,
		AgentSyncID:       params.AgentSyncID,
		ProviderSessionID: meta.ProviderSessionID,
	}
	if err := h.sessions.Start(ctx, record); err != nil {
		return nil, fmt.Errorf("transcriptimport: create session: %w", err)
	}
	logger.Ctx(ctx).Info("daemon.transcriptimport.Execute: imported",
		zap.String("backendType", params.Backend), zap.String("conversationId", params.ConversationID),
		zap.String("providerSessionId", meta.ProviderSessionID), zap.Int("turns", replay.turns),
		zap.Int64("latestSeq", replay.seq), zap.Int("droppedImages", replay.droppedImages))
	return &wire.ExecuteResult{
		ConversationID: params.ConversationID, ProviderSessionID: meta.ProviderSessionID,
		Cwd: meta.Cwd, Title: title, Turns: replay.turns,
	}, nil
}

// ── internals ───────────────────────────────────────────────────────────────

// replayCounters 攒一次回放的计数,只用于收尾那一行日志与应答 —— 逐轮打日志会把
// 一条 42 轮的会话写成 42 行(observability.md:不在循环里打日志)。
type replayCounters struct {
	turns         int
	seq           int64
	droppedImages int
}

// findImported 回答「这条 provider 会话在这台对端名下是不是已经有会话了」。
//
// 判重锚点是 **provider 会话身份**,不是调用方铸的号:同一条磁盘会话导第二次时
// 调用方多半会铸一个新号,按号判重等于每次都建一条新的。provider 会话身份为空的
// 转录(磁盘上就没有这个 id)判不了重,只能落到「这个号占没占」那一档。
//
// 号被**另一条**会话占着时报 ErrSessionInUse:会话 id 各客户端本地自增、必然重号,
// 直接 Upsert 会把那条会话的身份行改写成一份磁盘转录的元信息。
func (h *Handlers) findImported(
	ctx context.Context, peer, peerSessionID, providerSessionID string,
) (*handlers.SessionRecord, error) {
	if providerSessionID != "" {
		// 判重要看这个对端的**全部**会话:命中与否取决于 provider_session_id,
		// 用关键词收窄只会漏判,于是把同一份磁盘转录导入第二次。
		rows, err := h.sessions.List(ctx, peer, "")
		if err != nil {
			return nil, fmt.Errorf("transcriptimport: list sessions: %w", err)
		}
		for i := range rows {
			if rows[i].ProviderSessionID == providerSessionID {
				return &rows[i], nil
			}
		}
	}
	row, err := h.sessions.Find(ctx, peer, peerSessionID)
	if err != nil {
		return nil, fmt.Errorf("transcriptimport: find session: %w", err)
	}
	if row == nil {
		return nil, nil
	}
	// 走到这里说明按 provider 会话身份没认出它:这个号上坐着的是**别的**会话。
	return nil, fmt.Errorf("%w: %s", wire.ErrSessionInUse, peerSessionID)
}

// clearLeftoverJournal 清掉同号的残留日志(上一次导入写到一半留下的)。
func (h *Handlers) clearLeftoverJournal(ctx context.Context, peer, peerSessionID string) error {
	if h.journalPurge == nil {
		return nil
	}
	removed, err := h.journalPurge.DeleteAll(ctx, peer, peerSessionID)
	if err != nil {
		return fmt.Errorf("transcriptimport: clear leftover journal: %w", err)
	}
	if removed > 0 {
		logger.Ctx(ctx).Warn("daemon.transcriptimport.Execute: cleared a leftover journal",
			zap.String("peerSessionId", peerSessionID), zap.Int64("rows", removed))
	}
	return nil
}

// journalTurn 把一轮落成客户端**本该收到过**的那串通知:
//
//	runtime.event(用户那一行)→ runtime.event(这一轮的事件)→ 用量 / 错误 → Done
//	→ runtime.runResultDone
//
// 形状照的是活着的那一轮自己发出的通知,不是另造一套:补齐侧(remote runtime 的
// 补齐轮、agentre-server 的镜像投影)认的就是这一串,收尾帧 runResultDone 是补齐轮
// 的终点 —— 少了它,客户端那一轮永远不结束。
func (h *Handlers) journalTurn(
	ctx context.Context, peer, peerSessionID string,
	meta pkgimport.Meta, turn pkgimport.Turn, counters *replayCounters,
) error {
	// 用户那一行经 UserMessageEvent 进转录 —— 它是 daemon 到客户端「这一轮是谁开的」
	// 的唯一事实来源。**不带 SourceDevice**:这一轮不是任何在线设备此刻发起的,
	// 填一个指纹会在转录里印出一句「来自 <设备>」。
	if turn.UserText != "" {
		if err := h.journalEvent(ctx, peer, peerSessionID,
			agentruntime.UserMessageEvent{Text: turn.UserText}, counters); err != nil {
			return err
		}
	}
	// 用户附的图过不去:UserMessageEvent 只有文本一格,而事件流里没有第二个能挂在
	// 用户那一行上的载体。如实计数,收尾那行日志报出来,不假装导全了。
	counters.droppedImages += len(turn.UserImages)
	for _, event := range turn.Events {
		if err := h.journalEvent(ctx, peer, peerSessionID, event, counters); err != nil {
			return err
		}
	}
	// 用量既走事件、也进收尾帧:桌面端按事件累加、收尾帧只在没有事件时兜底(见
	// chat_svc 那处注释),而 agentre-server 的转录投影只读事件 —— 两边各取所需,
	// 不会重复计数。
	if turn.Usage != nil {
		if err := h.journalEvent(ctx, peer, peerSessionID,
			agentruntime.UsageUpdate{Usage: turn.Usage}, counters); err != nil {
			return err
		}
	}
	if turn.ErrorText != "" {
		if err := h.journalEvent(ctx, peer, peerSessionID,
			agentruntime.ErrorEvent{Err: errors.New(turn.ErrorText)}, counters); err != nil {
			return err
		}
	}
	if err := h.journalEvent(ctx, peer, peerSessionID, agentruntime.Done{}, counters); err != nil {
		return err
	}
	done := &runtimewire.RunResultDoneFrame{
		ConversationID:    peerSessionID,
		ProviderSessionID: meta.ProviderSessionID,
		Model:             turn.Model,
		UserAnchor:        turn.ForkAnchor,
	}
	if turn.Usage != nil {
		done.Usage = &runtimewire.UsageWire{
			PromptTokens: turn.Usage.PromptTokens, CompletionTokens: turn.Usage.CompletionTokens,
			ReasoningTokens: turn.Usage.ReasoningTokens, CachedTokens: turn.Usage.CachedTokens,
			CacheCreationTokens: turn.Usage.CacheCreationTokens, TotalTokens: turn.Usage.TotalTokens,
		}
	}
	if err := h.journalNotification(ctx, peer, peerSessionID, runtimewire.NotifyRunResultDone, done, counters); err != nil {
		return err
	}
	counters.turns++
	return nil
}

func (h *Handlers) journalEvent(
	ctx context.Context, peer, peerSessionID string,
	event agentruntime.Event, counters *replayCounters,
) error {
	frame := &runtimewire.EventFrame{ConversationID: peerSessionID, Event: event}
	return h.journalNotification(ctx, peer, peerSessionID, runtimewire.NotifyEvent, frame, counters)
}

// journalNotification 把一帧转成 Protobuf 通知并落库。**落库的字节里 seq 是 0**:
// seq 是日志行自己的属性,补齐时由行的 seq 列重新盖上(与 runtime.go 的 emit 同一
// 条纪律)。
func (h *Handlers) journalNotification(
	ctx context.Context, peer, peerSessionID, method string, frame any, counters *replayCounters,
) error {
	notification, err := protowire.WireNotificationToProto(method, frame)
	if err != nil {
		return fmt.Errorf("transcriptimport: convert %s: %w", method, err)
	}
	protowire.SetNotificationSeq(notification, 0)
	payload, err := protowire.EncodeNotification(notification)
	if err != nil {
		return fmt.Errorf("transcriptimport: encode %s: %w", method, err)
	}
	seq, err := h.journal.Append(ctx, peer, peerSessionID, payload)
	if err != nil {
		return fmt.Errorf("transcriptimport: journal %s: %w", method, err)
	}
	counters.seq = seq
	return nil
}
