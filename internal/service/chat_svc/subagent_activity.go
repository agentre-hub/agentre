package chat_svc

import (
	"context"
	"encoding/json"
	"fmt"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	chatblocks "github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// startSubagentActivityWatcher 为某 claudecode 会话惰性启动一个 watcher goroutine,订阅
// runtime 的「后台 subagent 内部活动流」(run_in_background 的 subagent 在会话**空闲态**
// 自主产出的内部工具调用流),把每轮活动嵌套渲染回发起卡所在的消息并跨消息落库。
// 每会话只起一个(subagentActivityWatchers 去重);底层 SubagentActivity channel 在子进程
// evict / CloseSession 时 close,watcher 随之退出并清去重位。
//
// 并发约束(关键,镜像 startAutonomousWatcher):watcher 在 driveSubagentActivity 里
// **绝不持 chat 会话锁** drain。否则与 pkg/claudecode.Session 常驻 reader 死锁 ——
// 活动事件出口 channel 不被 drain → Session 活跃槽位不释放 → 用户 turn 卡死。
func (s *chatSvc) startSubagentActivityWatcher(sessionID int64, be *agent_backend_entity.AgentBackend, src agentruntime.SubagentActivitySource) {
	if sessionID <= 0 || be == nil || src == nil {
		return
	}
	if _, loaded := s.subagentActivityWatchers.LoadOrStore(sessionID, struct{}{}); loaded {
		return
	}
	beCopy := *be
	go func() {
		defer s.subagentActivityWatchers.Delete(sessionID)
		defer s.clearBgRunningOnSourceClosed(sessionID)
		for act := range src.SubagentActivity(sessionID) {
			s.driveSubagentActivity(context.Background(), sessionID, &beCopy, act)
		}
	}()
}

// driveSubagentActivity 把一轮后台 subagent 内部活动落回**发起消息**(不新建消息):
//  1. 定位发起消息(含 subagent_state{parent_tool_call_id==act.ToolCallID} 的 assistant 消息);
//     找不到 → 抽干 act.Events 返回(别让 Session reader 阻塞)。
//  2. 经会话级旁路 emit StreamSubagentActivityStarted —— 把发起消息 id + per-turn 流名推给
//     前端,让它重开 per-turn 流把活动块嵌套渲染回 AgentSpawnCard。
//  3. 用 dispatcher drain act.Events(ToolCallHandler 已把 ParentToolCallID!="" 路由成
//     NestedToolUseBlock / NestedToolResultBlock,实时 stream 走发起卡的 per-turn 流)。
//  4. 收尾:取本轮新产出的嵌套块(ParentToolCallID==act.ToolCallID),序列化成 StoredBlock JSON
//     + 收集其 id,跨消息 AppendSubagentChildren 进发起消息;然后 emit StreamDone。
//
// 与 driveAutonomousTurn 不同:这是**空闲态后台活动**,不新建消息 / 不取 NextSeq /
// 不翻 session running —— 会话保持 idle。
func (s *chatSvc) driveSubagentActivity(ctx context.Context, sessionID int64, be *agent_backend_entity.AgentBackend, act agentruntime.SubagentActivity) {
	if act.ToolCallID == "" {
		drainAndDiscard(act.Events)
		return
	}

	launchMsg, err := chat_repo.Message().FindAssistantBySubagentToolCallID(ctx, sessionID, act.ToolCallID)
	if err != nil || launchMsg == nil {
		logger.Ctx(ctx).Warn("chat_svc: driveSubagentActivity launch message not found; draining events",
			zap.Int64("sessionId", sessionID), zap.String("toolUseId", act.ToolCallID), zap.Error(err))
		drainAndDiscard(act.Events)
		return
	}

	sess, err := chat_repo.Session().Find(ctx, sessionID)
	if err != nil || sess == nil {
		logger.Ctx(ctx).Warn("chat_svc: driveSubagentActivity load session failed; draining events",
			zap.Int64("sessionId", sessionID), zap.Error(err))
		drainAndDiscard(act.Events)
		return
	}

	stream := StreamName(sessionID, launchMsg.ID)
	logger.Ctx(ctx).Info("chat_svc: subagent activity started",
		zap.Int64("sessionId", sessionID),
		zap.Int64("launchMessageId", launchMsg.ID),
		zap.String("toolUseId", act.ToolCallID))
	// 会话级旁路:让前端定位发起卡并 openStream 订阅 per-turn 流。不插入新 assistant 行
	// (发起消息已存在),区别于 StreamAutonomousStarted。
	s.emitter.Emit(ctx, AutonomousStreamName(sessionID), ChatStreamEvent{
		Kind:            StreamSubagentActivityStarted,
		Stream:          stream,
		LaunchMessageID: launchMsg.ID,
		ToolCallID:      act.ToolCallID,
	})

	// accumulator 只累积本轮活动产出的块,Finalize 即"本次新增";发起消息既有的块不 seed
	// —— 它们已落库,本路径只追加新嵌套子块。**唯一的例外是 subagent_state overlay**:
	// SubagentProgress / SubagentDone 靠 turn.Mutate 命中它才更新+推流,不 seed 的话空闲期
	// 的 task_progress 全被静默丢弃,派遣卡的工具数 / token 永远停在派遣那一刻(sess-2275)。
	acc := turn.New()
	state := seedSubagentState(acc, launchMsg, act.ToolCallID)
	progressBefore := progressOf(state)
	dispEmit := &subagentActivityEmitter{inner: &dispatcherEmitter{svc: s}, sessionID: sessionID}
	turnCtx := s.newTurnContext(launchMsg, sess, stream, be.Type)
	for ev := range act.Events {
		s.publishPeerEvent(sessionID, ev)
		if err := s.dispatcher.Apply(ctx, ev, acc, dispEmit, nil, turnCtx); err != nil {
			logger.Ctx(ctx).Warn("chat_svc: subagent activity dispatcher Apply failed",
				zap.String("eventType", fmt.Sprintf("%T", ev)), zap.Error(err))
		}
	}

	// 取本轮新产出的嵌套块(ParentToolCallID==act.ToolCallID),序列化 + 收集 tool_use id,
	// 跨消息追加进发起消息。finalCtx 去掉 cancel 但保留 DB 句柄 —— 已流出的内容必须落库。
	childBlocks, childIDs := subagentChildBlocks(acc.Finalize(), act.ToolCallID)
	finalCtx := context.WithoutCancel(ctx)
	if len(childBlocks) > 0 {
		childJSON, err := encodeStoredBlocks(childBlocks)
		if err != nil {
			logger.Ctx(finalCtx).Warn("chat_svc: driveSubagentActivity encode child blocks failed",
				zap.Int64("sessionId", sessionID), zap.String("toolUseId", act.ToolCallID), zap.Error(err))
		} else if err := chat_repo.Message().AppendSubagentChildren(finalCtx, sessionID, act.ToolCallID, childJSON, childIDs); err != nil {
			logger.Ctx(finalCtx).Warn("chat_svc: driveSubagentActivity AppendSubagentChildren failed",
				zap.Int64("sessionId", sessionID), zap.String("toolUseId", act.ToolCallID), zap.Error(err))
		}
	}

	// 进度快照:overlay 在本轮被 task_progress 就地 patch 过,定向落回发起消息。空闲活动轮
	// 不重写整条消息(它属于一条早已收尾的旧消息),不落这一步的话重开会话又退回旧数字。
	// 没变化就不写 —— 发起消息 blocks_json 动辄几百 KB,读-改-写不白跑一趟。
	if progress := progressOf(state); progress != progressBefore {
		if err := chat_repo.Message().PatchSubagentProgress(finalCtx, sessionID, act.ToolCallID, progress); err != nil {
			logger.Ctx(finalCtx).Warn("chat_svc.driveSubagentActivity: PatchSubagentProgress failed",
				zap.Int64("sessionId", sessionID), zap.String("toolUseId", act.ToolCallID), zap.Error(err))
		}
	}

	logger.Ctx(finalCtx).Info("chat_svc: subagent activity finalized",
		zap.Int64("sessionId", sessionID),
		zap.Int64("launchMessageId", launchMsg.ID),
		zap.Int("childBlocks", len(childBlocks)))
	s.emitter.Emit(finalCtx, stream, ChatStreamEvent{Kind: StreamDone})
	// 会话级流补发终态兜底:StreamSubagentActivityStarted 是前端拿 per-turn 流名的唯一入口,
	// 零子块的活动轮 started→done 背靠背,前端极可能还没 EventsOn 订阅 per-turn 流就漏掉
	// StreamDone → 发起卡那条 LiveStream 永远留在 store → streaming 卡死。
	// 会话级流常驻订阅,补一发让前端据 LaunchMessageID 兜底 finishStream(幂等)。
	s.emitter.Emit(finalCtx, AutonomousStreamName(sessionID), ChatStreamEvent{
		Kind:            StreamAutonomousFinished,
		LaunchMessageID: launchMsg.ID,
	})
}

// subagentActivityEmitter 包住 dispatcherEmitter,把 subagent_started / progress / done /
// model 额外镜像一份到会话级旁路流。
//
// 为什么要镜像:per-turn 流上的这几个事件,前端只会把 meta 合并进**那条流的 liveBlocks**;
// 而空闲活动轮的派遣卡(Agent 工具的 tool_use 块)早已随发起消息落库,不在任何 liveBlocks
// 里 —— 那一路合并必然落空,卡片头部的工具数 / token 只能等重开会话才刷新。会话级流由
// ChatPanel 常驻订阅,它持有 messages,能就地合并进已落库的那张卡(与后台任务完成时
// completedTask 同时翻 liveBlocks + messages 是同一套做法)。
//
// model 事件虽是 first-wins、一轮只发一次(不像进度那样反复刷新),但同一个「派遣卡不在
// 任何 liveBlocks 里」的问题对它一样成立:模型只在子代理内部首帧到达时产出这一次,若不
// 镜像到会话级流,这仅有的一次机会就会落空在 per-turn 流上,徽标同样要等重开会话才出现。
type subagentActivityEmitter struct {
	inner     turn.Emitter
	sessionID int64
}

func (e *subagentActivityEmitter) Emit(ctx context.Context, stream string, raw any) {
	e.inner.Emit(ctx, stream, raw)
	m, ok := raw.(map[string]any)
	if !ok {
		return
	}
	switch kind, _ := m["kind"].(string); kind {
	case string(StreamSubagentStarted), string(StreamSubagentProgress), string(StreamSubagentDone), string(StreamSubagentModel):
		e.inner.Emit(ctx, AutonomousStreamName(e.sessionID), m)
	}
}

// seedSubagentState 把发起消息里既有的 subagent_state overlay 复制一份进本轮 accumulator,
// 并按 SubagentProgress / SubagentDone 用的 Mutate key 登记,让它们能命中并就地更新进度。
// 复制而非直接引用:GetBlocks 解出的块只是本次解码的副本,后续只有 PatchSubagentProgress
// 落库才算数。返回 nil 表示发起消息里没有这块(消息已被改写 / blocks 损坏)。
func seedSubagentState(acc *turn.Accumulator, launchMsg *chat_entity.Message, toolCallID string) *chatblocks.SubagentStateBlock {
	if launchMsg == nil || toolCallID == "" {
		return nil
	}
	bs, err := launchMsg.GetBlocks()
	if err != nil {
		return nil
	}
	for _, b := range bs {
		var st chatblocks.SubagentStateBlock
		switch v := b.(type) {
		case *chatblocks.SubagentStateBlock:
			st = *v
		case chatblocks.SubagentStateBlock:
			st = v
		default:
			continue
		}
		if st.ParentToolCallID != toolCallID {
			continue
		}
		acc.AddBlock(&st, "subagent_state:"+toolCallID)
		return &st
	}
	return nil
}

// progressOf 取 overlay 的运行时进度快照(含 Model,R6/A9 空闲活动轮解出的实际模型经
// 这份快照跟其它进度字段一起跨轮写回);state 为 nil 时返回零值快照(IsZero,不触发落库)。
func progressOf(state *chatblocks.SubagentStateBlock) chat_repo.SubagentProgress {
	if state == nil {
		return chat_repo.SubagentProgress{}
	}
	return chat_repo.SubagentProgress{
		TotalTokens:  state.TotalTokens,
		ToolUses:     state.ToolUses,
		DurationMs:   state.DurationMs,
		LastToolName: state.LastToolName,
		Model:        state.Model,
	}
}

// subagentChildBlocks 从一轮活动的 Finalize 结果里挑出属于 parentToolCallID 的嵌套块
// (NestedToolUseBlock / NestedToolResultBlock),并收集 NestedToolUseBlock 的 id 作为
// childIDs(对齐 subagent_state.nested_tool_call_ids 反向索引,该数组索引的是 tool_use 块)。
func subagentChildBlocks(finalBlocks []cagoblocks.ContentBlock, parentToolCallID string) ([]cagoblocks.ContentBlock, []string) {
	var children []cagoblocks.ContentBlock
	var ids []string
	for _, b := range finalBlocks {
		switch nb := b.(type) {
		case *chatblocks.NestedToolUseBlock:
			if nb.ParentToolCallID != parentToolCallID {
				continue
			}
			children = append(children, nb)
			if nb.ID != "" {
				ids = append(ids, nb.ID)
			}
		case *chatblocks.NestedToolResultBlock:
			if nb.ParentToolCallID != parentToolCallID {
				continue
			}
			children = append(children, nb)
		}
	}
	return children, ids
}

// encodeStoredBlocks 把 ContentBlock 序列化成与 chat_messages.blocks_json 同构的
// StoredBlock-数组 JSON(信封 {type,data}),供 AppendSubagentChildren 解码追加。
// 与 chat_entity.Message.SetBlocks 走同一 cago EncodeAll 路径,保证 wire 形态一致。
func encodeStoredBlocks(bs []cagoblocks.ContentBlock) (string, error) {
	stored, err := cagoblocks.EncodeAll(bs)
	if err != nil {
		return "", err
	}
	buf, err := json.Marshal(stored)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}
