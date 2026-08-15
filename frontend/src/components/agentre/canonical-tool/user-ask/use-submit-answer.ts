import * as React from "react";
import { useTranscriptPorts } from "@agentre-ai/agentre-ui";

import type { AskAnswerDTO } from "../types";

type SubmitAnswerInput = {
  sessionId: number;
  requestId: string;
  answers?: AskAnswerDTO[];
  skipped?: boolean;
};

// useSubmitAnswer 把「回答提问」这个动作端口包成一个朴素的 async 调用。
// 端口只能在渲染期从 context 取,所以这里是 hook 而不是自由函数;调用方拿到的
// 提交函数在 ports 不变时保持同一性。
// 后端 UserAskResolved 事件直接更新 store,前端不做 optimistic 分支;
// 失败由端口 reject + 调用方自己处理。
export function useSubmitAnswer() {
  const ports = useTranscriptPorts();

  return React.useCallback(
    async (opts: SubmitAnswerInput): Promise<void> => {
      await ports.answerUserQuestion({
        sessionId: opts.sessionId,
        requestId: opts.requestId,
        answers: opts.answers,
        skipped: !!opts.skipped,
      });
    },
    [ports],
  );
}
