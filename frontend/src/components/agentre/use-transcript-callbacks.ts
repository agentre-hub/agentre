import * as React from "react";

import type { PlanActionStream } from "@agentre-hub/agentre-ui";

type UseTranscriptCallbacksOptions = {
  onRerun?: (messageId: number) => void;
  onContinue?: (messageId: number) => void;
  onRetryTurn?: (messageId: number) => void;
  onEdit?: (messageId: number) => void;
  onPlanActionStarted?: (stream: PlanActionStream, userText: string) => void;
  onStopSubagent?: (toolUseId: string) => void | Promise<void>;
  onStopLocalCommand?: (terminalId: string) => void | Promise<void>;
};

type TranscriptCallbacks = {
  stableOnRerun: (id: number) => void;
  stableOnContinue: (id: number) => void;
  stableOnRetryTurn: (id: number) => void;
  stableOnEdit: (id: number) => void;
  stableOnPlanActionStarted: (
    stream: PlanActionStream,
    userText: string,
  ) => void;
  stableOnStopSubagent: (toolUseId: string) => void | Promise<void>;
  stableOnStopLocalCommand: (terminalId: string) => void | Promise<void>;
  hasStopLocalCommand: boolean;
};

// useEvent 模式：把 onRerun/onEdit/onPlanActionStarted 包成稳定引用,让行组件的
// React.memo / TranscriptRenderContext 不会被 ChatPanel 传入的 inline lambda 击穿。
// 父侧每次重渲都换新函数,但 ref 内部更新后稳定代理捕获最新值,语义不变。
function useTranscriptCallbacks({
  onRerun,
  onContinue,
  onRetryTurn,
  onEdit,
  onPlanActionStarted,
  onStopSubagent,
  onStopLocalCommand,
}: UseTranscriptCallbacksOptions): TranscriptCallbacks {
  const onRerunRef = React.useRef(onRerun);
  const onContinueRef = React.useRef(onContinue);
  const onRetryTurnRef = React.useRef(onRetryTurn);
  const onEditRef = React.useRef(onEdit);
  const onPlanActionStartedRef = React.useRef(onPlanActionStarted);
  const onStopSubagentRef = React.useRef(onStopSubagent);
  const onStopLocalCommandRef = React.useRef(onStopLocalCommand);
  React.useEffect(() => {
    onRerunRef.current = onRerun;
    onContinueRef.current = onContinue;
    onRetryTurnRef.current = onRetryTurn;
    onEditRef.current = onEdit;
    onPlanActionStartedRef.current = onPlanActionStarted;
    onStopSubagentRef.current = onStopSubagent;
    onStopLocalCommandRef.current = onStopLocalCommand;
  });
  const stableOnRerun = React.useCallback((id: number) => {
    onRerunRef.current?.(id);
  }, []);
  const stableOnContinue = React.useCallback((id: number) => {
    onContinueRef.current?.(id);
  }, []);
  const stableOnRetryTurn = React.useCallback((id: number) => {
    onRetryTurnRef.current?.(id);
  }, []);
  const stableOnEdit = React.useCallback((id: number) => {
    onEditRef.current?.(id);
  }, []);
  const stableOnPlanActionStarted = React.useCallback(
    (stream: PlanActionStream, userText: string) => {
      onPlanActionStartedRef.current?.(stream, userText);
    },
    [],
  );
  // 原样交还宿主的 Promise:子代理卡靠它的 reject 复原停止键。
  const stableOnStopSubagent = React.useCallback((toolUseId: string) => {
    return onStopSubagentRef.current?.(toolUseId);
  }, []);
  const stableOnStopLocalCommand = React.useCallback((terminalId: string) => {
    return onStopLocalCommandRef.current?.(terminalId);
  }, []);
  const hasStopLocalCommand = onStopLocalCommand !== undefined;

  return {
    stableOnRerun,
    stableOnContinue,
    stableOnRetryTurn,
    stableOnEdit,
    stableOnPlanActionStarted,
    stableOnStopSubagent,
    stableOnStopLocalCommand,
    hasStopLocalCommand,
  };
}

export { useTranscriptCallbacks };
export type { TranscriptCallbacks };
