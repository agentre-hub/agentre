package transcriptimport

// execute.go 是这一族里唯一**写库**的一条路径:把一份磁盘转录落成一条归**这台
// 机器**执行的会话。
//
// 为什么是机器在导,而不是浏览器:agentre-server 从不拥有会话,它只镜像会话 ——
// 「新对话」的既定形状是浏览器铸号、机器执行、内容经 SESSION_LIST / SESSION_PULL
// 流上去。导入照同一条形状走,导出来的会话因此和别的会话一模一样地镜像上去,
// 不需要第二条通路。
//
// 落库顺序是「先建身份行 → 清同号残留 → 再逐轮落转录 → 失败则连身份行一起撤掉」:
//   - 转录(消息行 + 块行)按**本机会话主键**挂靠(规格 2026-09-05 决策 9),所以身份行
//     必须先在库里,回放才有地方落。从前的顺序(身份行最后写)服务的是「身份行 = 导入
//     完整」这个锚点,现在由失败路径上的撤销顶上:回放失败就把这一条整个删掉,库里
//     不会留下一条看着已经导完、实际只有半截转录的会话。
//   - 同号残留(上一次导入写到一半、进程没了)在回放之前清掉,两次回放因此不会首尾
//     相接叠成一份双倍长的转录。
//
// 不开事务是有意的:把整份转录裹进一个事务会让这条长写锁住整个库,而这台 daemon 上
// 别的会话正在实时落它们自己的转录。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	runtimewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	pkgimport "github.com/agentre-hub/agentre/internal/pkg/transcriptimport"
	"github.com/agentre-hub/agentre/internal/pkg/transcriptimport/wire"
)

// SessionStore 是会话身份行在执行侧要用到的那三件:按对端判重、按号查占用、建行。
// 按 ISP 在消费方声明 —— daemon 那份实现同时满足 handlers 的读写端口,这里只取本
// 路径真正调用的那几个方法。
type SessionStore interface {
	Find(ctx context.Context, peerFingerprint, peerSessionID string) (*handlers.SessionRecord, error)
	List(ctx context.Context, peerFingerprint, keyword string, offset, limit int) ([]handlers.SessionRecord, error)
	Start(ctx context.Context, rec handlers.SessionRecord) error
}

// SessionDeleter 删掉一条会话的身份行(同 handlers.SessionDeletePort)。本路径只在
// 回放失败的撤销上用它,见文件头的落库顺序。
type SessionDeleter interface {
	Delete(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error)
}

// Transcript 落库这条对话的转录(同 handlers.TranscriptPort)。回放出的每一轮都从
// 这里进库 —— 本包不另开第二条落块路径,块怎么攒出来同样归共用的那只累积器。
type Transcript interface {
	StartTurn(ctx context.Context, conversationID, userText string) (user, assistant *transcript_entity.Message, err error)
	FinishTurn(ctx context.Context, m *transcript_entity.Message) error
}

// TranscriptPurger 清空某会话的全部转录(同 handlers.TranscriptPurgePort)。本路径
// 用它清「同号残留」,以及回放失败时撤掉这一次写进去的东西,见文件头的落库顺序。
type TranscriptPurger interface {
	DeleteAll(ctx context.Context, peerFingerprint, peerSessionID string) (int64, error)
}

// Execute 在这台机器上执行一次导入。
func (h *Handlers) Execute(ctx context.Context, params wire.ExecuteParams) (*wire.ExecuteResult, error) {
	if err := handlers.ErrInvalidConversationID(params.ConversationID); err != nil {
		return nil, err
	}
	if h.sessions == nil || h.transcript == nil {
		// 没接存储就没有「执行」可言。静默回一个空结果会让调用方以为导完了,
		// 而库里一行都没有。
		logger.Ctx(ctx).Error("daemon.transcriptimport.Execute: storage not wired",
			zap.String("conversationId", params.ConversationID))
		return nil, rpcerror.ErrInternal
	}
	peer, err := handlers.ResolveSessionPeer(ctx, params.PeerFingerprint, h.loggedInAccountID)
	if err != nil {
		return nil, err
	}
	source, err := h.openTranscript(ctx, params.Backend, params.Locator)
	if err != nil {
		return nil, err
	}
	defer closeTranscript(ctx, source)
	meta := source.Meta()

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
	if err := h.clearLeftoverTranscript(ctx, peer, peerSessionID); err != nil {
		return nil, err
	}
	replay := &replayCounters{}
	turnErr := source.Turns(ctx, func(turn pkgimport.Turn) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return h.importTurn(ctx, peerSessionID, turn, replay)
	})
	if turnErr != nil {
		// 这里就是这条链路上「能判定它失败了」的那一层:上面只剩 RPC 壳。
		// 半截转录连同它的身份行一起撤掉:留着会被下一次同号导入判成「已导过」,
		// 那半截再也补不齐(见文件头的落库顺序)。
		h.rollbackFailedImport(ctx, peer, peerSessionID)
		logger.Ctx(ctx).Error("daemon.transcriptimport.Execute: replay failed",
			zap.String("backendType", params.Backend), zap.String("conversationId", params.ConversationID),
			zap.Int("turns", replay.turns), zap.Error(turnErr))
		return nil, fmt.Errorf("transcriptimport: replay turns: %w", turnErr)
	}
	logger.Ctx(ctx).Info("daemon.transcriptimport.Execute: imported",
		zap.String("backendType", params.Backend), zap.String("conversationId", params.ConversationID),
		zap.String("providerSessionId", meta.ProviderSessionID), zap.Int("turns", replay.turns),
		zap.Int("droppedImages", replay.droppedImages))
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
		// 整份(limit=0):这里判的是「这条对话是不是已经导过」,漏一页就会重复导入。
		rows, err := h.sessions.List(ctx, peer, "", 0, 0)
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

// clearLeftoverTranscript 清掉同号的残留转录(上一次导入写到一半留下的)。
func (h *Handlers) clearLeftoverTranscript(ctx context.Context, peer, peerSessionID string) error {
	if h.transcriptPurge == nil {
		return nil
	}
	removed, err := h.transcriptPurge.DeleteAll(ctx, peer, peerSessionID)
	if err != nil {
		return fmt.Errorf("transcriptimport: clear leftover transcript: %w", err)
	}
	if removed > 0 {
		logger.Ctx(ctx).Warn("daemon.transcriptimport.Execute: cleared a leftover transcript",
			zap.String("peerSessionId", peerSessionID), zap.Int64("rows", removed))
	}
	return nil
}

// rollbackFailedImport 撤掉这一次写进去的东西:先清转录,再删身份行。尽力而为 ——
// 撤不掉只记日志,原本那个回放错误才是调用方该看到的。
func (h *Handlers) rollbackFailedImport(ctx context.Context, peer, peerSessionID string) {
	if err := h.clearLeftoverTranscript(ctx, peer, peerSessionID); err != nil {
		logger.Ctx(ctx).Warn("daemon.transcriptimport.Execute: rollback transcript failed",
			zap.String("peerSessionId", peerSessionID), zap.Error(err))
	}
	if h.sessionDelete == nil {
		return
	}
	if _, err := h.sessionDelete.Delete(ctx, peer, peerSessionID); err != nil {
		logger.Ctx(ctx).Warn("daemon.transcriptimport.Execute: rollback session row failed",
			zap.String("peerSessionId", peerSessionID), zap.Error(err))
	}
}

// importTurn 把一轮落成与跑一轮**同形**的转录:用户那一行 + 一条 assistant 消息,
// 正文由共用的累积器就着这一轮的事件攒出来(决策 2)。
//
// 不另造一套:同一段内容在两个宿主上必须由同一行代码写进库 —— 导入这条路走的是
// 与实时那一轮完全一样的 dispatcher + accumulator + 消息仓储。
func (h *Handlers) importTurn(
	ctx context.Context, peerSessionID string, t pkgimport.Turn, counters *replayCounters,
) error {
	_, msg, err := h.transcript.StartTurn(ctx, peerSessionID, t.UserText)
	if err != nil {
		return fmt.Errorf("transcriptimport: start turn: %w", err)
	}
	if msg == nil {
		return errors.New("transcriptimport: session row is missing")
	}
	// 用户附的图过不去:这一行只落文本。如实计数,收尾那行日志报出来,不假装导全了。
	counters.droppedImages += len(t.UserImages)

	dispatcher := transcript.NewTurnDispatcher(transcript.Adapters{})
	acc := turn.New()
	turnCtx := &turn.TurnContext{Waits: turn.NewWaitTracker()}
	for _, event := range t.Events {
		if err := dispatcher.Apply(ctx, event, acc, discardEmitter{}, nil, turnCtx); err != nil {
			return fmt.Errorf("transcriptimport: apply event: %w", err)
		}
	}
	if err := msg.SetBlocks(acc.Finalize()); err != nil {
		return fmt.Errorf("transcriptimport: encode blocks: %w", err)
	}
	msg.Model = t.Model
	msg.ErrorText = t.ErrorText
	msg.ForkAnchor = t.ForkAnchor
	if u := t.Usage; u != nil {
		msg.PromptTokens = u.PromptTokens
		msg.CompletionTokens = u.CompletionTokens
		msg.CachedTokens = u.CachedTokens
		msg.CacheCreationTokens = u.CacheCreationTokens
		msg.ReasoningTokens = u.ReasoningTokens
		msg.TotalInputTokens = u.PromptTokens + u.CachedTokens + u.CacheCreationTokens
	}
	if err := h.transcript.FinishTurn(ctx, msg); err != nil {
		return fmt.Errorf("transcriptimport: finish turn: %w", err)
	}
	counters.turns++
	return nil
}

// discardEmitter 是 dispatcher 要的那个发射器的空位:导入不推送任何东西。
type discardEmitter struct{}

func (discardEmitter) Emit(context.Context, string, any) {}
