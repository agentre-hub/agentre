// frontend/src/components/agentre/peer/peer-ports.ts
//
// Peer Tab 自己那份转录动作端口。
//
// 转录里的提问卡 / 授权卡是共享包的（与本机对话同一张），按下去调的是
// `TranscriptPorts`。App 顶层注入的那一份把答复交给**本机**会话的 Wails 绑定 ——
// 拿它去答另一台机器上的对话是答错人。所以 Peer Tab 在自己这棵子树上换一份：
//
//   - 回答提问 / 工具授权：走 peer 绑定，按 (fingerprint, conversationId) 寻址。卡片
//     递过来的 sessionId 不参与寻址（见 PEER_CARD_SESSION_ID）。
//   - 其余三种答复（内置写工具审批 / exec 审批 / 计划动作）peer 绑定没有对应的口：
//     如实 reject，卡片走自己的失败态，而不是悄悄发给本机后端。
//   - 其余外壳能力（外链、打开路径、@提及去向 …）沿用桌面端那一份，与换端口之前
//     Peer Tab 从 App 顶层拿到的完全相同。

import * as React from "react";
import type { TranscriptPorts } from "@agentre-hub/agentre-ui";

import { usePeerSessionsStore } from "../../../stores/peer-session-store";
import { useDesktopTranscriptPorts } from "../transcript-ports-desktop";

/**
 * Peer Tab 递给转录的会话号。
 *
 * 它**不是**任何一条会话的身份：本机没有这条会话（归约出来的消息上那一格恒为 0），
 * 对端那条对话的身份是 conversationId，由上面的端口闭包持有。共享包的提问卡 /
 * 授权卡把「sessionId 为假」读作「没有可答的会话」并直接不提交，这个非零的占位只是
 * 告诉它们「有，去调端口」。取负数，是为了它永远撞不上本机 chat_sessions 的主键。
 */
export const PEER_CARD_SESSION_ID = -1;

/** 一次答复的收场里，面板要在提示条上如实说出来的那两种。 */
export type PeerReplyOutcome =
  /** 对端说这条请求已经被别处处理了。答复本身算送达。 */
  | { kind: "alreadyHandled" }
  /** 没送到。error 是机器原话。 */
  | { kind: "failed"; error: string };

type PeerReplyResult = { alreadyHandled: boolean } | { error: string };

export function usePeerTranscriptPorts({
  fingerprint,
  conversationId,
  onOutcome,
}: {
  fingerprint: string;
  conversationId: string;
  onOutcome: (outcome: PeerReplyOutcome) => void;
}): TranscriptPorts {
  const desktop = useDesktopTranscriptPorts();
  // 端口对象的同一性是转录行级 memo 不被击穿的前提，回调因此走 ref。
  const onOutcomeRef = React.useRef(onOutcome);
  React.useEffect(() => {
    onOutcomeRef.current = onOutcome;
  }, [onOutcome]);

  return React.useMemo<TranscriptPorts>(() => {
    // store 的提交口把失败折成 { error } 交回而不是 reject；卡片的失败态（不锁卡、
    // 回滚乐观更新）只认 reject，在这里还原。
    const settle = (res: PeerReplyResult) => {
      if ("error" in res) {
        onOutcomeRef.current({ kind: "failed", error: res.error });
        throw new Error(res.error);
      }
      if (res.alreadyHandled) onOutcomeRef.current({ kind: "alreadyHandled" });
    };
    const unsupported = () =>
      Promise.reject(
        new Error("this reply is not available for a remote desktop session"),
      );

    return {
      ...desktop,

      async answerUserQuestion(input) {
        settle(
          await usePeerSessionsStore.getState().submitAnswer({
            fingerprint,
            conversationId,
            requestId: input.requestId,
            answers: input.answers ?? [],
            skipped: !!input.skipped,
          }),
        );
      },

      async answerToolPermission(input) {
        settle(
          await usePeerSessionsStore.getState().submitToolPermission({
            fingerprint,
            conversationId,
            requestId: input.requestId,
            allow: input.allow,
            alwaysAllowSession: !!input.alwaysAllowSession,
          }),
        );
      },

      answerToolApproval: unsupported,
      resolveExecApproval: unsupported,
      resolvePlanAction: unsupported,
    };
  }, [desktop, fingerprint, conversationId]);
}
