import { useEffect, useRef } from "react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import type { chat_svc, view } from "../../wailsjs/go/models";

// ChatSessionStatusPatch mirrors backend chat_svc.ChatSessionStatusPatch.
// Type definition unified into @/stores/types (ChatSessionStatusEvent); import + re-export here.
import type {
  ChatSessionStatusEvent,
  SessionConnectionState,
} from "@/stores/types";
export type ChatSessionStatusPatch = ChatSessionStatusEvent;

// ChatStreamUsage mirrors backend chat_svc.ChatStreamUsage. Carried on the
// "usage" kind to live-update the Composer's context-usage progress bar
// in the middle of a turn (each model API call boundary fires one).
export type ChatStreamUsage = {
  messageId?: number;
  promptTokens?: number;
  completionTokens?: number;
  cachedTokens?: number;
  cacheCreationTokens?: number;
  reasoningTokens?: number;
  // totalInputTokens runtime translator 按 family 聚合好的本次 API call 输入总量;
  // 前端按它读「已用上下文」,不做 family-specific 加法。
  totalInputTokens?: number;
  // contextWindow 与 usage 同帧携带，保证任一收到的 usage 快照都有对应分母；
  // 避免独立 session_status 事件在 per-turn 订阅建立前丢失。
  contextWindow?: number;
};

// ChatStreamEvent mirrors backend chat_svc.ChatStreamEvent. Fields are optional
// because only the ones relevant to a given `kind` are populated.
export type ChatStreamEvent = {
  kind:
    | "chunk"
    | "thinking"
    // output_activity 是纯计时信号（模型开始产出一个输出块，含工具入参这类看不见
    // 的输出），不带任何载荷，只用来记首 token。
    | "output_activity"
    | "tool_use"
    | "tool_result"
    | "steer_consumed"
    | "subagent_started"
    | "subagent_progress"
    | "subagent_done"
    | "subagent_model"
    | "retry"
    | "message_end"
    | "done"
    | "error"
    | "closed"
    | "aborted"
    | "ask_user_question"
    | "plan_update"
    | "tool_permission_request"
    | "exec_approval"
    | "tool_approval"
    | "session_status"
    | "usage"
    | "compact_boundary"
    | "runtime_status"
    | "autonomous_started"
    | "subagent_activity_started"
    | "autonomous_finished"
    | "connection_state";
  delta?: string;
  message?: chat_svc.ChatMessage;
  error?: string;

  // steer_consumed:
  queuedIds?: string[];
  previousAssistantMessage?: chat_svc.ChatMessage;
  userMessages?: chat_svc.ChatMessage[];
  assistantMessage?: chat_svc.ChatMessage;

  // tool_use:
  toolUseId?: string;
  toolName?: string;
  toolInput?: Record<string, unknown>;
  // canonical — runtime translator 算出的统一工具识别投影。前端 CanonicalToolRouter
  // 按 canonical.kind 分发到 canonical-tool/<kind>/card.tsx;不识别走 RawToolCard。
  // tool_use 之外,tool_permission_request 也会携带 canonical (kind=tool.permission
  // 或 plan.approve_request);ChatStreamsHost 需要把它一并落到 store。
  canonical?: view.CanonicalDTO;

  // tool_result:
  toolResult?: string;
  isError?: boolean;
  // tool_result 元数据 (claudecode CLI 顶层 tool_use_result 原样透传):
  // 典型场景 TaskCreate 在这里返回 {"task":{"id":"1"}}, 前端 task-progress 派生层
  // 据此把 TaskCreate ↔ TaskUpdate 关联起来。普通工具帧无该字段时为 undefined。
  toolResultMeta?: Record<string, unknown>;

  // subagent: tool_use / tool_result 携带 parentToolUseId 表示这是 subagent 内部的子调用；
  // subagent_* 事件携带 toolUseId（指向外层 Agent）+ subagent meta，前端按 toolUseId 把
  // meta merge 到对应 ChatBlock 的 subagent 字段。
  parentToolUseId?: string;
  subagentRunId?: string;
  subagent?: Omit<chat_svc.ChatBlockSubagent, "convertValues">;

  // subagent_model: ToolUseID(复用上方字段)关联到对应派遣,model 是子代理内部帧解析出的
  // 实际模型(R2 覆盖 R1 的入参别名,R3 first-wins)。只带这一个字段,不复用上面的整份
  // Subagent 快照 —— 后端故意避免整份快照的浅合并把已有 toolUses/totalTokens/status
  // 覆盖成空值,前端消费时同样只取这个字段去合并(R4)。
  model?: string;

  // ask_user_question: 携带交互问题载荷（初次到达）或答完后的状态切换
  // （Answered=true，前端按 requestId 找到既有 block 更新）。
  askUserQuestion?: chat_svc.ChatBlockAskUserQuestion;

  // tool_permission_request: 携带工具审批载荷（初次到达）或审批后的状态切换
  // （Resolved=true，前端按 requestId 找到既有 block 更新）。
  toolPermission?: chat_svc.ChatBlockToolPermission;

  // exec_approval: OpenClaw Gateway exec approval lifecycle. resolved/expired
  // updates the existing card and does not mean the command/tool finished.
  execApproval?: chat_svc.ChatBlockExecApproval;

  // tool_approval: agent 内置写工具审批。status="pending" 为新卡(appendLiveToolApproval),
  // "approved"|"denied"|"expired" 为决议更新(markToolApprovalResolved,同 requestId)。
  // 这些字段平铺在事件上(不像 toolPermission 走一个嵌套对象),ChatStreamsHost 据此
  // 合成 ToolApprovalData。toolKey 标识来源工具(org / workflow / ...);requestId
  // 同时被 tool_approval 与未来其它按 id 关联的事件共用。
  toolKey?: string;
  requestId?: string;
  status?: string;
  result?: string;

  // session_status: 后端推上来的 session 级 status patch
  // （agentStatus + needsAttention）。ChatStreamsHost 把它写到 LiveStream
  // 上，useChatSession 订阅后覆盖到 ChatSessionDetail，让 toolbar 实时变色。
  sessionStatus?: ChatSessionStatusPatch;

  // retry: 后端/上游的非终态重试通知；本轮 stream 仍继续运行。
  retryAttempt?: number;
  retryMaxAttempts?: number;
  retryMessage?: string;
  retryDetails?: string;
  retryAt?: number;

  // usage: 当前 assistant 消息的 per-call usage 快照。每次模型内部 API call
  // 边界（claudecode 的主 agent assistant 帧 / codex 的 token_count notification）
  // 都推一条，前端 store 写到 LiveStream.liveUsage，Composer 进度条读它实时
  // 刷新「已用上下文」，不必等 done 事件 reload 才看到变化。
  usage?: ChatStreamUsage;

  // compact_boundary: 后端识别到 claudecode CLI 的 system.compact_boundary 帧
  // (manual /compact 或 auto 阈值触发)。带 messageId(boundary 落在哪条 assistant
  // 消息) + seq + pre_tokens + trigger + at。前端 ChatStreamsHost 把它落到当前
  // assistant message 的 blocks 末尾 (Type=compact_boundary block);ChatTranscript
  // 按"最后一个 compact_boundary"切分,折叠之前的旧消息。
  compact?: {
    messageId: number;
    seq: number;
    preTokens?: number;
    trigger?: "auto" | "manual";
    at: number;
  };

  // runtime_status: claudecode CLI 的 system{subtype:"status",status:<非空>} 帧
  // 透传 (compacting 等过渡态)。Compacting 是 chat_svc 已经判定的方便位 —— 前端
  // 直接读 compacting 即可,不必再判字符串。compact 结束信号 = compact_boundary /
  // done / error / aborted / closed,store 在 finishStream + appendLiveCompactBoundary
  // 自动清旗,不依赖 CLI 显式 status:"" 帧。
  runtimeStatus?: {
    status?: string;
    compacting?: boolean;
  };

  // autonomous_started / subagent_activity_started: 经会话级旁路事件
  // "chat:autonomous:<sessionId>" 推上来。
  // autonomous_started — CLI run_in_background 任务完成后自主跑的一轮;
  //   assistantMessage 是新 assistant 行;stream 是该轮 per-turn 事件名。
  //   completedTask: 触发本轮的后台任务身份;前端把对应 tool_use.subagent.status
  //   翻成终态。summary 为退出码摘要文本。
  // subagent_activity_started — 后台 subagent 开始产生内部活动;stream 是该
  //   subagent 的 per-turn 事件名;launchMessageId 是发起此 subagent 的 assistant
  //   消息 id（已在 transcript 中）;toolUseId 是对应的外层 Agent tool_use id。
  //   前端只调 openStream 把活动流绑到发起消息，不插入新消息行，不翻 running。
  // autonomous_finished — 自主轮 / 后台 subagent 活动轮收尾时经会话级流补发的终态兜底;
  //   launchMessageId 是该轮收尾的 assistant / 发起消息 id。会话级流常驻订阅、无
  //   subscribe-after-emit race,前端据此兜底 finishStream 漏掉 per-turn done 的 orphan 流。
  stream?: string;
  trigger?: string;
  completedTask?: { toolUseId: string; status: string; summary?: string };
  launchMessageId?: number;

  // connection_state: 经会话级流 "chat:conn:<sessionId>"(后端
  // chat_svc.ConnStateStreamName)推上来的**通道**状态 —— 本机与执行该会话那台
  // 远端 daemon 之间连没连上,与 agentStatus 正交(重连期间远端仍在跑)。
  // 走会话级流而不是 per-turn 流:断连时 per-turn 流恰好是没人收得到的那条。
  connectionState?: SessionConnectionState;
  // 只有补齐落定(connectionState==="connected")那一发带这两个数:本次补齐按游标
  // 重放了多少条通知(caughtUpCount),以及补完后该会话还有多少个待决策没被回答
  // (pendingDecisions)。
  //
  // caughtUpCount 是**通知**条数,不是用户眼里的条数 —— daemon 对每个 agentruntime
  // 事件都落一行日志(TextDelta / ThinkingDelta / UsageUpdate 全在内),一条长回复
  // 就是上千条。它只用来判「这次重连确实漏掉了东西」;跳转控件上的条数由
  // chat-panel-catchup-state 按转录行数现算。
  caughtUpCount?: number;
  pendingDecisions?: number;
};

export function useChatStream(
  stream: string | null,
  onEvent: (e: ChatStreamEvent) => void,
): void {
  // onEvent 通常被父组件以 `(ev) => handleEvent(sessionId, ev)` 形式包了一层
  // 内联 arrow，每次 render 引用都变。如果直接进 effect 依赖数组，每次父组件
  // 重渲染都会 EventsOff + EventsOn 抖动；在两次订阅之间到达的 Wails 事件
  // 直接被丢（EventsOff 已清掉 listener，新的 EventsOn 还没绑回来）。
  //
  // ChatStreamsHost 监听 streams Map,而每条 chunk/thinking delta 都会替换
  // streams 引用，turn 中每秒几十次重渲染——steer_consumed 撞进抖动窗口的
  // 概率非常高,表现就是 chip 不消除、user 消息也没插入。
  //
  // 用 ref 持有最新 callback,订阅 effect 只在 stream 变化时跑一次 EventsOn,
  // 内部闭包始终读 cbRef.current,既稳定又不丢事件。ref 的更新放进单独的
  // useEffect 而非 render 阶段:concurrent rendering 下 render 可能被丢弃,
  // commit 后再写 ref 才能保证持有的真是已生效的那次 onEvent。
  const cbRef = useRef(onEvent);
  useEffect(() => {
    cbRef.current = onEvent;
  }, [onEvent]);

  useEffect(() => {
    if (!stream) return;
    const handler = (payload: ChatStreamEvent) => cbRef.current(payload);
    // EventsOn 返回精确卸载函数（按 callback 引用解绑），比 EventsOff(name)
    // 更安全:后者会清掉同名所有 listener,未来若有第二个订阅者会被误伤。
    const off = EventsOn(stream, handler);
    return () => {
      if (typeof off === "function") off();
    };
  }, [stream]);
}
