// 行视图装配:把宿主的头像、cwd、回调包成 render context,并把单个虚拟行渲染出来。
//
// 两件事合成一处,因为它们共用同一批入参(回调 / live 内容契约 / cwd),拆成两个 hook
// 只会让这批入参在组件里出现两遍:
//   1. `renderCtx` —— 交给共享包的 <TranscriptRenderContext.Provider>。头像节点由宿主
//      给(包里的 MessageRow / ChatMessage 不认识桌面端的 16 色 agent 调色板与
//      icon-registry,只借包的 MESSAGE_AVATAR_CLASS 对齐头像列尺寸);六个回调先过
//      useTranscriptCallbacks 的稳定代理(useEvent 模式),只读调用方不传的按存在性门控。
//   2. `renderRowView` —— 单个虚拟行的内容。非 live 行的 live* prop 全部收敛到稳定空值,
//      让 TranscriptRowView 的 React.memo shallow 比较恒命中 —— 每个流式 chunk 只有
//      live 消息(和指示器宿主末行)重渲。
import * as React from "react";

import {
  MESSAGE_AVATAR_CLASS,
  TranscriptRowView,
  type PlanActionStream,
  type TranscriptRenderContextValue,
  type TranscriptRow,
} from "@agentre-hub/agentre-ui";

import { useTranscriptCallbacks } from "../../use-transcript-callbacks";
import { AgentAvatar } from "../../primitives";
import type { AgentColor } from "../../types";
// 只借组件的 live 内容契约(它比包的 LiveRowContent 多带 liveRetry / liveTurn);
// type-only 导入,不会和 ../../transcript 成运行时环。
import type { TranscriptLiveContent } from "../transcript";

export function useTranscriptRowView({
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
}: {
  agentName: string;
  agentColor: AgentColor;
  /** 会话的工作目录,用于工具卡片把 cwd 内路径展示为相对路径。 */
  cwd?: string;
  /** 当前 chat session id —— AskUserQuestionCard 提交答案时要带它去 Wails 绑定。 */
  sessionId?: number;
  /** Stable mounted chat tab key for UI drafts that survive route/tab remounts. */
  tabStateKey?: string;
  onRerun?: (messageId: number) => void;
  onContinue?: (messageId: number) => void;
  onEdit?: (messageId: number) => void;
  onPlanActionStarted?: (stream: PlanActionStream, userText: string) => void;
  onStopSubagent?: (toolUseId: string) => void;
  onStopLocalCommand?: (terminalId: string) => void | Promise<void>;
  /** 各消息此刻的流式内容,按 messageId 索引;表里有 key 的消息才在流式中。 */
  liveByMessageId?: ReadonlyMap<number, TranscriptLiveContent>;
  /** 生成指示器(三个点)的宿主消息 id。 */
  lastAssistantId: number | null;
  // 这四个在组件那一侧已经取过默认值(streaming/liveCompacting/reconnecting=false、
  // fallbackModel=""),到这里就是实打实的值 —— 行组件要的是 boolean 而不是 undefined。
  streaming: boolean;
  liveCompacting: boolean;
  reconnecting: boolean;
  fallbackModel: string;
}): {
  renderCtx: TranscriptRenderContextValue;
  renderRowView: (row: TranscriptRow) => React.ReactElement;
} {
  // 六个回调的稳定代理(useEvent 模式)住在 useTranscriptCallbacks 里。
  const {
    stableOnRerun,
    stableOnContinue,
    stableOnEdit,
    stableOnPlanActionStarted,
    stableOnStopSubagent,
    stableOnStopLocalCommand,
    hasStopLocalCommand,
  } = useTranscriptCallbacks({
    onRerun,
    onContinue,
    onEdit,
    onPlanActionStarted,
    onStopSubagent,
    onStopLocalCommand,
  });

  const renderCtx = React.useMemo<TranscriptRenderContextValue>(
    () => ({
      agentName,
      // 头像节点由宿主给：包里的 MessageRow / ChatMessage 不认识桌面端的 16 色
      // agent 调色板与 icon-registry，只借包的 MESSAGE_AVATAR_CLASS 对齐头像列尺寸。
      agentAvatar: (
        <AgentAvatar
          name={agentName}
          initials={agentName.charAt(0)}
          color={agentColor}
          size="md"
          className={MESSAGE_AVATAR_CLASS}
        />
      ),
      cwd,
      // 只读调用方不传 onEdit/onRerun 时，上游 ref 为 undefined；
      // 此处有条件地传入稳定代理，让行视图能用 ctx?.onEdit 作存在性门控。
      onEdit: onEdit ? stableOnEdit : undefined,
      onContinue: onContinue ? stableOnContinue : undefined,
      onPlanActionStarted: stableOnPlanActionStarted,
      onStopLocalCommand: hasStopLocalCommand
        ? stableOnStopLocalCommand
        : undefined,
      onStopSubagent: onStopSubagent ? stableOnStopSubagent : undefined,
      onRerun: onRerun ? stableOnRerun : undefined,
      sessionId: sessionId ?? 0,
      tabStateKey,
    }),
    [
      agentColor,
      agentName,
      cwd,
      hasStopLocalCommand,
      onEdit,
      onContinue,
      onRerun,
      onStopSubagent,
      sessionId,
      stableOnEdit,
      stableOnContinue,
      stableOnPlanActionStarted,
      stableOnStopLocalCommand,
      stableOnStopSubagent,
      stableOnRerun,
      tabStateKey,
    ],
  );

  // renderRowView:单个虚拟行的内容。非 live 行的 live* prop 全部收敛到稳定空值,
  // 让 TranscriptRowView 的 React.memo shallow 比较恒命中 —— 每个流式 chunk 只有
  // live 消息(和指示器宿主末行)重渲。
  const renderRowView = React.useCallback(
    (row: TranscriptRow) => {
      // 本行所属消息此刻的流式内容(没有 = 该消息不在流式中)。多条流并存时各查各的。
      const live = liveByMessageId?.get(row.messageId);
      const isLiveTail = row.isLastOfMessage && live != null;
      const showIndicator =
        row.isLastOfMessage &&
        streaming &&
        lastAssistantId != null &&
        row.messageId === lastAssistantId;
      return (
        <TranscriptRowView
          row={row}
          liveTail={isLiveTail ? (live?.liveTail ?? "") : ""}
          liveBlocks={isLiveTail ? live?.liveBlocks : undefined}
          liveRetry={isLiveTail ? (live?.liveRetry ?? null) : null}
          showIndicator={showIndicator}
          compacting={showIndicator && isLiveTail && liveCompacting}
          reconnecting={showIndicator && reconnecting}
          liveTurn={isLiveTail ? (live?.liveTurn ?? null) : null}
          fallbackModel={fallbackModel}
        />
      );
    },
    [
      fallbackModel,
      lastAssistantId,
      liveByMessageId,
      liveCompacting,
      reconnecting,
      streaming,
    ],
  );

  return { renderCtx, renderRowView };
}
