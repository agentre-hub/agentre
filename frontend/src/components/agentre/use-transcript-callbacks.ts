import * as React from "react";

import type { PlanActionStream } from "@agentre-hub/agentre-ui";

type UseTranscriptCallbacksOptions = {
  onRerun?: (messageId: number) => void;
  onContinue?: (messageId: number) => void;
  onEdit?: (messageId: number) => void;
  onPlanActionStarted?: (stream: PlanActionStream, userText: string) => void;
  onStopSubagent?: (toolUseId: string) => void;
  onStopLocalCommand?: (terminalId: string) => void | Promise<void>;
};

type TranscriptCallbacks = {
  stableOnRerun: (id: number) => void;
  stableOnContinue: (id: number) => void;
  stableOnEdit: (id: number) => void;
  stableOnPlanActionStarted: (
    stream: PlanActionStream,
    userText: string,
  ) => void;
  stableOnStopSubagent: (toolUseId: string) => void;
  stableOnStopLocalCommand: (terminalId: string) => void | Promise<void>;
  hasStopLocalCommand: boolean;
};

// useEvent 模式：把 onRerun/onEdit/onPlanActionStarted 包成稳定引用,让行组件的
// React.memo / TranscriptRenderContext 不会被 ChatPanel 传入的 inline lambda 击穿。
// 父侧每次重渲都换新函数,但 ref 内部更新后稳定代理捕获最新值,语义不变。
function useTranscriptCallbacks({
  onRerun,
  onContinue,
  onEdit,
  onPlanActionStarted,
  onStopSubagent,
  onStopLocalCommand,
}: UseTranscriptCallbacksOptions): TranscriptCallbacks {
  const onRerunRef = React.useRef(onRerun);
  const onContinueRef = React.useRef(onContinue);
  const onEditRef = React.useRef(onEdit);
  const onPlanActionStartedRef = React.useRef(onPlanActionStarted);
  const onStopSubagentRef = React.useRef(onStopSubagent);
  const onStopLocalCommandRef = React.useRef(onStopLocalCommand);
  React.useEffect(() => {
    onRerunRef.current = onRerun;
    onContinueRef.current = onContinue;
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
  const stableOnEdit = React.useCallback((id: number) => {
    onEditRef.current?.(id);
  }, []);
  const stableOnPlanActionStarted = React.useCallback(
    (stream: PlanActionStream, userText: string) => {
      onPlanActionStartedRef.current?.(stream, userText);
    },
    [],
  );
  const stableOnStopSubagent = React.useCallback((toolUseId: string) => {
    onStopSubagentRef.current?.(toolUseId);
  }, []);
  const stableOnStopLocalCommand = React.useCallback((terminalId: string) => {
    return onStopLocalCommandRef.current?.(terminalId);
  }, []);
  const hasStopLocalCommand = onStopLocalCommand !== undefined;

  return {
    stableOnRerun,
    stableOnContinue,
    stableOnEdit,
    stableOnPlanActionStarted,
    stableOnStopSubagent,
    stableOnStopLocalCommand,
    hasStopLocalCommand,
  };
}

export { useTranscriptCallbacks };
export type { TranscriptCallbacks };
