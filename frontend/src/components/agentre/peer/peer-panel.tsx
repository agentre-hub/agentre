// frontend/src/components/agentre/peer/peer-panel.tsx
//
// 远端桌面会话的 Peer Tab（R19，决策 13）。attach/pull/live 与发送/回答/权限由
// peer-session-store 编排；转录复用桌面端自己的 ChatTranscript / ChatComposer。
// 对端的未决提问 / 工具授权就是转录里共享包的那两张卡（与本机对话同一张）；它们
// 的答复经本面板自己那份动作端口走 peer 绑定（peer-ports.ts），AlreadyHandled 与
// 没送到都在提示条上如实说。关闭 Tab 只 detach（R19：不删除对端会话）。

import * as React from "react";
import { Loader2, MonitorUp, TriangleAlert, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  FailureDetail,
  TranscriptPortsProvider,
  cn,
  isAskExpiredError,
} from "@agentre-hub/agentre-ui";

import {
  ChatComposer,
  ChatTranscript,
  type ChatComposerHandle,
  type ChatComposerSubmit,
} from "../chat";
import {
  peerKeyOf,
  usePeerSessionsStore,
} from "../../../stores/peer-session-store";
import {
  PEER_CARD_SESSION_ID,
  usePeerTranscriptPorts,
  type PeerReplyOutcome,
} from "./peer-ports";
import type { PeerChatMessage } from "./peer-transcript";
import { classifyPeerSendFailure } from "./send-failure";
import type { chat_svc } from "../../../../wailsjs/go/models";

export type PeerPanelProps = {
  fingerprint: string;
  /** 对端那条对话的全局身份（uuid），不是本机 chat_sessions.id。 */
  conversationId: string;
  title?: string;
  deviceName: string;
  active: boolean;
  onClose: () => void;
};

// peerMessageToChatMessage 把归约出来的共享 DTO 贴回 ChatTranscript 的入参类型。
//
// 两者形状逐字相同（dto.ts 就是照着 Wails 生成的 chat_svc.ChatMessage 手写的，
// transcript-dto-contract 那条编译期守卫钉着这件事），所以这里只是换个类型名。
//
// 归约器已经按 Peer Tab 的口径把消息上的 sessionId 填成 0（本机没有这条会话，见
// peer-transcript.ts），所以这里**原样交还同一个引用** —— 转录行缓存以消息对象为
// WeakMap 键，在这里复制一份会让上游增量投影省下的那次重建白做。
function peerMessageToChatMessage(m: PeerChatMessage): chat_svc.ChatMessage {
  return m as unknown as chat_svc.ChatMessage;
}

export function PeerPanel({
  fingerprint,
  conversationId,
  title,
  deviceName,
  active,
  onClose,
}: PeerPanelProps) {
  const { t } = useTranslation();
  const key = peerKeyOf(fingerprint, conversationId);
  const session = usePeerSessionsStore((s) => s.sessions[key]);
  const attach = usePeerSessionsStore((s) => s.attach);
  const reattach = usePeerSessionsStore((s) => s.reattach);
  const detach = usePeerSessionsStore((s) => s.detach);
  const steer = usePeerSessionsStore((s) => s.steer);
  const run = usePeerSessionsStore((s) => s.run);
  const composerRef = React.useRef<ChatComposerHandle>(null);
  const [notice, setNotice] = React.useState<{
    text: string;
    kind: "error" | "info";
    /** 认不出来的机器原话。证据，不是说给用户听的那一句。 */
    detail?: string;
  } | null>(null);

  React.useEffect(() => {
    void attach({
      fingerprint,
      conversationId,
      title: title ?? "",
      deviceName,
    });
  }, [attach, fingerprint, conversationId, title, deviceName]);

  React.useEffect(() => {
    return () => detach(fingerprint, conversationId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fingerprint, conversationId]);

  const messages = React.useMemo<chat_svc.ChatMessage[]>(
    () =>
      (session?.transcript.messages ?? []).map((m) =>
        peerMessageToChatMessage(m),
      ),
    [session?.transcript.messages],
  );
  const status = session?.status ?? "attaching";
  const sending = session?.sending ?? false;
  // 「对端这条会话此刻在不在跑」——attach 交回的初值 + 实时帧推进（见 store）。
  const turnRunning = session?.lifecycleState === "running";

  const handleSubmit = React.useCallback(
    (message: ChatComposerSubmit) => {
      const text = message.text.trim();
      if (!text) return;
      setNotice(null);
      void (async () => {
        // 插话只插得进**正在进行的轮次**；会话闲着的时候要开新的一轮（与控制台
        // 同一口径）。判据是已接入拿到的会话状态，不是发出去撞了墙再看错误串 ——
        // 后者等于让每条空闲会话的第一次发送必然失败一次。
        const ok = turnRunning
          ? await steer(fingerprint, conversationId, text)
          : await run(fingerprint, conversationId, text);
        if (ok) return;
        // 草稿不能跟着一起丢。
        composerRef.current?.restoreDraft(text, message.images ?? []);
        // 真实原因 store 已经记下了（lastError）；这里把机器原话解析成一句事实。
        const failure = classifyPeerSendFailure(
          usePeerSessionsStore.getState().sessions[key]?.lastError,
        );
        setNotice({
          kind: "error",
          text: t(failure.key),
          detail: failure.detail,
        });
      })();
    },
    [steer, run, turnRunning, fingerprint, conversationId, key, t],
  );

  // 提问卡 / 授权卡的答复收场。卡片自己会画失败态；提示条补的是只有这枚 Tab 知道
  // 的那半句 —— 答复是发给另一台电脑的，它没送到，或那边已经有人处理过了。
  const handleReplyOutcome = React.useCallback(
    (outcome: PeerReplyOutcome) => {
      // 「那条请求已经不在等了」与「已被处理」对用户是同一件事。
      if (
        outcome.kind === "alreadyHandled" ||
        isAskExpiredError(outcome.error)
      ) {
        setNotice({ kind: "info", text: t("peerPanel.alreadyHandled") });
        return;
      }
      setNotice({
        kind: "error",
        text: t("peerPanel.decision.submitFailed"),
        detail: outcome.error || undefined,
      });
    },
    [t],
  );
  const ports = usePeerTranscriptPorts({
    fingerprint,
    conversationId,
    onOutcome: handleReplyOutcome,
  });

  return (
    <main className="content-sheet flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex items-center gap-2 px-4 py-2 text-xs text-muted-foreground">
        <MonitorUp className="size-4" aria-hidden="true" />
        <span className="font-medium text-foreground">
          {title || deviceName}
        </span>
        <span className="text-muted-foreground">
          {t("peerPanel.header.remote", { device: deviceName })}
        </span>
        <span
          className="ml-auto"
          role="status"
          aria-label={
            status === "ready"
              ? t("peerPanel.status.ready")
              : t("peerPanel.status.connecting")
          }
        >
          {status === "attaching" ? (
            <span className="inline-flex items-center gap-1">
              <Loader2 className="size-3 animate-spin" aria-hidden="true" />
              {t("peerPanel.status.connecting")}
            </span>
          ) : status === "error" ? (
            <span className="inline-flex items-center gap-1 text-destructive-text">
              <TriangleAlert className="size-3" aria-hidden="true" />
              {t("peerPanel.status.unavailable")}
            </span>
          ) : (
            t("peerPanel.status.ready")
          )}
        </span>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="size-6"
          aria-label={t("chatTabs.actions.closeTab")}
          onClick={onClose}
        >
          <X className="size-3" aria-hidden="true" />
        </Button>
      </header>

      {notice ? (
        <div
          className={cn(
            "mx-4 mt-2 flex items-start gap-2 rounded-md px-3 py-2 text-xs",
            notice.kind === "error"
              ? "bg-destructive-soft text-destructive-text"
              : "bg-field-fill text-muted-foreground",
          )}
          data-testid="peer-notice"
          data-kind={notice.kind}
        >
          <div className="min-w-0 flex-1">
            {notice.text}
            <FailureDetail detail={notice.detail} className="mt-1" />
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="size-5 shrink-0"
            aria-label={t("common.close")}
            onClick={() => setNotice(null)}
          >
            <X className="size-3" aria-hidden="true" />
          </Button>
        </div>
      ) : null}

      {status === "error" ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 px-8 text-center">
          <TriangleAlert
            className="size-8 text-destructive"
            aria-hidden="true"
          />
          <div className="text-sm font-semibold">
            {t("peerPanel.error.title")}
          </div>
          <div className="max-w-md text-xs text-muted-foreground">
            {t("peerPanel.error.description", { device: deviceName })}
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => void reattach(fingerprint, conversationId)}
          >
            {t("common.retry")}
          </Button>
          <FailureDetail
            detail={session?.error}
            className="max-w-md text-xs text-muted-foreground"
          />
        </div>
      ) : (
        <div className="min-h-0 flex-1">
          <TranscriptPortsProvider ports={ports}>
            <ChatTranscript
              agentName={title || deviceName}
              agentColor="agent-3"
              cwd={undefined}
              sessionId={PEER_CARD_SESSION_ID}
              active={active}
              messages={messages}
              // 生成中指示只在对端那一轮真的在跑、且不是停下来等我的时候出现。
              streaming={turnRunning && !session?.transcript.waitingForInput}
            />
          </TranscriptPortsProvider>
        </div>
      )}

      <ChatComposer
        ref={composerRef}
        onSubmit={handleSubmit}
        placeholder={t("peerPanel.composerPlaceholder")}
        sending={sending}
        disabled={status !== "ready"}
        autoFocusOnMount={false}
        backendType=""
        agentId={0}
        cwd=""
        supportsImageInput={false}
      />
    </main>
  );
}
