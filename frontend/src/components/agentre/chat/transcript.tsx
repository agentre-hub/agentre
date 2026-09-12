// 转录区:虚拟滚动、贴底策略、压缩边界折叠、更早消息加载,以及行视图的装配。
//
// 这是 chat 模块的主体。

import * as React from "react";

import {
  TooltipProvider,
  transcriptRowPadClass,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import {
  TranscriptUIStateProvider,
  type LiveRowContent,
  type LiveTurnInput,
  type PlanActionStream,
} from "@agentre-hub/agentre-ui";
import { CompactHistoryFold } from "../compact-history-fold";
import { EarlierMessagesLoader } from "../earlier-messages-loader";
import { TranscriptRenderContext } from "@agentre-hub/agentre-ui";
import type { AgentColor } from "../types";
import type { RetryNotice } from "@/stores/chat-streams-store";
import { chat_svc } from "../../../../wailsjs/go/models";

import { useTranscriptRows } from "./transcript/use-transcript-rows";
import { useTranscriptRowView } from "./transcript/use-transcript-row-view";
import { useTranscriptNavigation } from "./transcript/use-transcript-navigation";
import { useTranscriptVirtualizer } from "./transcript/use-transcript-virtualizer";

// ─── ChatTranscript ──────────────────────────────────────────────────────────

/**
 * TranscriptLiveContent 是一条 assistant 消息此刻的流式内容。在 transcript-rows
 * 的 LiveRowContent 之上多带一个 liveRetry —— 后者只用于行视图的重试提示卡,
 * 不参与行构建。
 */
export type TranscriptLiveContent = LiveRowContent & {
  liveRetry?: RetryNotice | null;
  liveTurn?: LiveTurnInput | null;
};

type ChatTranscriptProps = {
  agentName: string;
  agentColor: AgentColor;
  /** 会话的工作目录，用于工具卡片把 cwd 内路径展示为相对路径。 */
  cwd?: string;
  /** 当前 chat session id —— AskUserQuestionCard 提交答案时要带它去 Wails 绑定。 */
  sessionId?: number;
  /** Transcript 的滚动容器。传入时启用动态高度虚拟列表。 */
  scrollElement?: HTMLElement | null;
  /** scrollElement 挂上前也保持虚拟化路径，避免长对话首帧全量 mount。 */
  virtualize?: boolean;
  /** 当前 tab 是否可见；从隐藏切回时触发虚拟列表重新测量。 */
  active?: boolean;
  messages: chat_svc.ChatMessage[];
  /**
   * 各 assistant 消息各自的流式内容,按 messageId 索引;由 chat-streams-store
   * 跨路由维护(每条 LiveStream 一项)。表里有 key 的消息就在流式中:其 liveBlocks
   * 摆在 persisted blocks 之后、liveTail 之前,整体顺序与真实流入顺序一致。
   *
   * **必须传全表**:一个会话可同时有用户轮 / 自主续轮 / 后台 subagent 活动轮多条流
   * 并存,只传一条(旧的单 liveTargetId 契约)会让其余几条的消息瞬间掉回持久化态。
   * 复用 ChatTranscript 的调用方漏传整表 = 那些消息完全不流式(参见 sess-1950)。
   */
  liveByMessageId?: ReadonlyMap<number, TranscriptLiveContent>;
  /** 用户点某条 assistant 上的「重新生成」时回调，参数是目标 assistant 的消息 id。 */
  onRerun?: (messageId: number) => void;
  /** 错误卡点击「继续」时回调，参数是失败的 assistant 消息 id。 */
  onContinue?: (messageId: number) => void;
  /** 用户点某条 user 消息上的「编辑」时回调，参数是 user 消息 id。 */
  onEdit?: (messageId: number) => void;
  /** stream 是否进行中。true 时在末尾 assistant 内挂 typing 指示器，覆盖首 chunk 前 / 工具返回后的空窗期。 */
  streaming?: boolean;
  /** claudecode CLI 正在跑 /compact 时为 true;末尾 assistant 的 typing indicator 替换为
   *  "正在压缩上下文…" chip,让用户知道这段时间在做什么。compact_boundary 到达自动清空。*/
  liveCompacting?: boolean;
  /** 与执行该会话那台远端 daemon 的通道断了、正在退避重连时为 true;末尾 assistant 的
   *  typing indicator 替换为断连形态,让"网断了"与"agent 在想"一眼可分。连接恢复即换回。
   *  它是运行态之上的修饰,不改 agentStatus。*/
  reconnecting?: boolean;
  onPlanActionStarted?: (stream: PlanActionStream, userText: string) => void;
  /** 停掉某张 AgentSpawn 卡对应的正在运行的子 agent / 后台任务(按 tool_use_id 下发 stop_task)。
   *  仅 backend 支持时由 ChatPanel 传入;未传 = 卡片不显示停止按钮。 */
  onStopSubagent?: (toolUseId: string) => void;
  /** 停掉 ChatPanel 启动并持有生命周期的本地命令；只读调用方不传。 */
  onStopLocalCommand?: (terminalId: string) => void | Promise<void>;
  /** Stable mounted chat tab key for UI drafts that survive route/tab remounts. */
  tabStateKey?: string;
  /** 占位 assistant 的 model 为空时，脚注用会话当前模型。 */
  fallbackModel?: string;
  /**
   * 还有更早的消息只拿到了元数据(正文没随本次加载下发,spec 2026-08-27 决策 6)。
   * 为真时转录顶部给出「取回更早正文」的入口,并在用户滚回顶部时自动去取。
   */
  hasEarlierMessages?: boolean;
  /** 更早那一段的正文正在取回来。 */
  loadingEarlier?: boolean;
  /** 取回更早那一段正文;未传 = 不给入口(只读调用方)。 */
  onLoadEarlier?: () => void;
};

export type ChatTranscriptHandle = {
  scrollToMessage: (messageId: number) => void;
  // 锚点恢复:把锚点行钉到距视口顶 offset px 处,并随虚拟器逐行复测收敛。
  // rowKey(data-row-key)命中时精确钉回该行 —— 行级虚拟化下长消息拆成多行,
  // 只按 messageId 会塌到消息首行;rowKey 失效(行已消失/旧快照)回退消息首行。
  // 返回 false 表示该消息当前不在 displayMessages(被折叠 / 尚未加载),
  // 调用方应回退到像素恢复。
  scrollToAnchor: (
    messageId: number,
    offset: number,
    rowKey?: string,
  ) => boolean;
};

export const ChatTranscript = React.forwardRef<
  ChatTranscriptHandle,
  ChatTranscriptProps
>(function ChatTranscript(
  {
    agentName,
    agentColor,
    cwd,
    sessionId,
    scrollElement,
    virtualize = false,
    active = true,
    messages: allMessages,
    liveByMessageId,
    onContinue,
    onRerun,
    onEdit,
    onPlanActionStarted,
    onStopSubagent,
    onStopLocalCommand,
    tabStateKey,
    streaming = false,
    liveCompacting = false,
    reconnecting = false,
    fallbackModel = "",
    hasEarlierMessages = false,
    loadingEarlier = false,
    onLoadEarlier,
  },
  ref,
) {
  // 行模型(前缀切刀 / 压缩折叠 / 自主轮 id / 行缓存与 live 叠加)整块住在
  // useTranscriptRows 里:四段推导的顺序与理由都跟着搬进了那个文件。
  const {
    rows,
    firstRowIndexByMessageId,
    rowIndexByKey,
    messages,
    folding,
    foldedCount,
    setExpanded,
    lastAssistantId,
  } = useTranscriptRows({ allMessages, liveByMessageId, sessionId });

  // 行视图装配(render context + 单个行的渲染)整块住在 useTranscriptRowView 里,
  // 连同它那条「六个回调的稳定代理」一起 —— 那批代理只有它用。
  const { renderCtx, renderRowView } = useTranscriptRowView({
    agentName,
    agentColor,
    cwd,
    sessionId,
    tabStateKey,
    onRerun,
    onContinue,
    onEdit,
    onPlanActionStarted,
    onStopSubagent,
    onStopLocalCommand,
    liveByMessageId,
    lastAssistantId,
    streaming,
    liveCompacting,
    reconnecting,
    fallbackModel,
  });

  // 虚拟化、贴底与滚动位置还原整块住在 useTranscriptVirtualizer 里 —— 连那两个只被
  // 它用的常量(贴底容差 / overscan)也一起搬了过去。
  const {
    shouldVirtualize,
    virtualizer,
    renderVirtualRows,
    virtualSpacerSize,
  } = useTranscriptVirtualizer({ rows, scrollElement, active, virtualize });

  // 跳转与锚点两条 API 住在 useTranscriptNavigation 里:它自己把两个方法写进 ref 的
  // imperative handle,组件不需要接任何返回值。
  useTranscriptNavigation({
    ref,
    virtualizer,
    scrollElement,
    firstRowIndexByMessageId,
    rowIndexByKey,
    folding,
    messages,
    allMessages,
    setExpanded,
    hasEarlierMessages,
    onLoadEarlier,
  });

  // 行间距:消息末行 pb-7(消息间距),消息内分片行 pb-2.5(block 间距)。padding
  // 打在行 wrapper 上,跟随 measureElement 一起计入行高 —— isLastRowOfMessage 与
  // estimateSize 里 estimateRowSizeWithSpacing 补间距增量共用同一份边界判断,避免
  // 两处"是否消息末行"各算各的而漂移。

  return (
    <TooltipProvider delayDuration={200}>
      <TranscriptUIStateProvider>
        <TranscriptRenderContext.Provider value={renderCtx}>
          {/* 不再加 max-w-4xl —— 内部 ChatMessage 已经 cap 在 max-w-measure,
          这里再叠一层外层 max-w 没有任何收紧效果,只会留出 phantom 空白。 */}
          <div className={shouldVirtualize ? "min-h-full" : "flex flex-col"}>
            {hasEarlierMessages && onLoadEarlier ? (
              <EarlierMessagesLoader
                loading={loadingEarlier}
                onLoad={onLoadEarlier}
              />
            ) : null}
            {folding && foldedCount > 0 ? (
              <CompactHistoryFold
                count={foldedCount}
                onExpand={() => setExpanded(true)}
              />
            ) : null}
            {shouldVirtualize ? (
              <div
                className="relative w-full"
                style={{ height: `${virtualSpacerSize}px` }}
              >
                {renderVirtualRows
                  ? virtualizer.getVirtualItems().map((virtualItem) => {
                      const row = rows[virtualItem.index];
                      if (!row) return null;
                      return (
                        <div
                          key={virtualItem.key}
                          ref={virtualizer.measureElement}
                          data-index={virtualItem.index}
                          data-message-id={row.messageId}
                          data-row-key={row.key}
                          className={cn(
                            "absolute left-0 top-0 w-full",
                            transcriptRowPadClass(rows, virtualItem.index),
                          )}
                          style={{
                            transform: `translateY(${virtualItem.start}px)`,
                          }}
                        >
                          {renderRowView(row)}
                        </div>
                      );
                    })
                  : null}
              </div>
            ) : (
              rows.map((row, index) => (
                <div
                  key={row.key}
                  data-message-id={row.messageId}
                  data-row-key={row.key}
                  className={transcriptRowPadClass(rows, index)}
                >
                  {renderRowView(row)}
                </div>
              ))
            )}
          </div>
        </TranscriptRenderContext.Provider>
      </TranscriptUIStateProvider>
    </TooltipProvider>
  );
});
