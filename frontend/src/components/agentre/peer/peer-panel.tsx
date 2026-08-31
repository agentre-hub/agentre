// frontend/src/components/agentre/peer/peer-panel.tsx
//
// 远端桌面会话的 Peer Tab（R19，决策 13）。attach/pull/live 与发送/回答/权限由
// peer-session-store 编排；转录复用桌面端自己的 ChatTranscript / ChatComposer，
// ask_user_question / tool_permission_request 归约成的待决策在此自绘可操作卡片
// （提交走 peer 绑定，AlreadyHandled 时如实显示「已被处理」并刷新）。关闭 Tab 只
// detach（R19：不删除对端会话）。

import * as React from "react";
import { Loader2, MonitorUp, TriangleAlert, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

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
import type { PeerDecision, PeerChatMessage } from "./peer-transcript";
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
// 归约器已经按 Peer Tab 的口径把 sessionId 填成 0（本机没有这条会话，见
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
  const detach = usePeerSessionsStore((s) => s.detach);
  const steer = usePeerSessionsStore((s) => s.steer);
  const submitAnswer = usePeerSessionsStore((s) => s.submitAnswer);
  const submitToolPermission = usePeerSessionsStore(
    (s) => s.submitToolPermission,
  );
  const composerRef = React.useRef<ChatComposerHandle>(null);
  const [notice, setNotice] = React.useState<{
    text: string;
    kind: "error" | "info";
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
  const decisions = session?.transcript.decisions ?? [];
  const status = session?.status ?? "attaching";
  const sending = session?.sending ?? false;

  const handleSubmit = React.useCallback(
    (message: ChatComposerSubmit) => {
      const text = message.text.trim();
      if (!text) return;
      setNotice(null);
      void (async () => {
        const ok = await steer(fingerprint, conversationId, text);
        if (!ok) {
          composerRef.current?.restoreDraft(text, message.images ?? []);
          setNotice({
            kind: "error",
            text: t("peerPanel.sendFailed"),
          });
        }
      })();
    },
    [steer, fingerprint, conversationId, t],
  );

  const handleAnswer = React.useCallback(
    async (decision: PeerDecision & { kind: "ask" }) => {
      const res = await submitAnswer({
        fingerprint,
        conversationId,
        requestId: decision.requestId,
        answers: [],
        skipped: true,
      });
      if ("error" in res) {
        setNotice({ kind: "error", text: res.error });
      } else if (res.alreadyHandled) {
        setNotice({ kind: "info", text: t("peerPanel.alreadyHandled") });
      }
    },
    [submitAnswer, fingerprint, conversationId, t],
  );

  const handlePermission = React.useCallback(
    async (decision: PeerDecision & { kind: "permission" }, allow: boolean) => {
      const res = await submitToolPermission({
        fingerprint,
        conversationId,
        requestId: decision.requestId,
        allow,
      });
      if ("error" in res) {
        setNotice({ kind: "error", text: res.error });
      } else if (res.alreadyHandled) {
        setNotice({ kind: "info", text: t("peerPanel.alreadyHandled") });
      }
    },
    [submitToolPermission, fingerprint, conversationId, t],
  );

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col bg-background">
      <header className="flex items-center gap-2 border-b border-border px-4 py-2 text-xs text-muted-foreground">
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
            <span className="inline-flex items-center gap-1 text-destructive">
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
            "mx-4 mt-2 rounded-md border px-3 py-2 text-xs",
            notice.kind === "error"
              ? "border-destructive bg-destructive-soft text-destructive"
              : "border-status-waiting bg-status-waiting-bg text-foreground",
          )}
          data-testid="peer-notice"
        >
          {notice.text}
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
          <div className="text-xs text-muted-foreground">{session?.error}</div>
        </div>
      ) : (
        <div className="min-h-0 flex-1">
          <ChatTranscript
            agentName={title || deviceName}
            agentColor="agent-3"
            cwd={undefined}
            sessionId={0}
            active={active}
            messages={messages}
            streaming={
              session?.transcript.waitingForInput ? false : status === "ready"
            }
          />
        </div>
      )}

      {decisions.length > 0 ? (
        <div className="flex flex-col gap-2 px-4 pb-2">
          {decisions.map((d) =>
            d.kind === "ask" ? (
              <AskDecisionCard
                key={d.requestId}
                decision={d}
                onAnswer={() => void handleAnswer(d)}
              />
            ) : (
              <PermissionDecisionCard
                key={d.requestId}
                decision={d}
                onDecide={(allow) => void handlePermission(d, allow)}
              />
            ),
          )}
        </div>
      ) : null}

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

function AskDecisionCard({
  decision,
  onAnswer,
}: {
  decision: PeerDecision & { kind: "ask" };
  onAnswer: () => void;
}) {
  const { t } = useTranslation();
  if (decision.answered) {
    return (
      <div
        className="rounded-md border border-border bg-card px-3 py-2 text-xs text-muted-foreground"
        data-testid="peer-ask-handled"
      >
        {decision.skipped
          ? t("peerPanel.decision.askSkipped")
          : t("peerPanel.decision.askAnswered")}
      </div>
    );
  }
  return (
    <div
      className="flex items-center justify-between gap-2 rounded-md border border-status-waiting bg-status-waiting-bg px-3 py-2 text-xs"
      data-testid="peer-ask-card"
    >
      <span className="min-w-0 flex-1 truncate">
        {decision.questions[0]?.question ?? t("peerPanel.decision.askTitle")}
      </span>
      <Button type="button" variant="outline" size="sm" onClick={onAnswer}>
        {t("peerPanel.decision.answer")}
      </Button>
    </div>
  );
}

function PermissionDecisionCard({
  decision,
  onDecide,
}: {
  decision: PeerDecision & { kind: "permission" };
  onDecide: (allow: boolean) => void;
}) {
  const { t } = useTranslation();
  if (decision.resolved) {
    return (
      <div
        className="rounded-md border border-border bg-card px-3 py-2 text-xs text-muted-foreground"
        data-testid="peer-permission-handled"
      >
        {decision.allowed
          ? t("peerPanel.decision.permissionAllowed")
          : t("peerPanel.decision.permissionDenied")}
      </div>
    );
  }
  return (
    <div
      className="flex items-center justify-between gap-2 rounded-md border border-status-waiting bg-status-waiting-bg px-3 py-2 text-xs"
      data-testid="peer-permission-card"
    >
      <span className="min-w-0 flex-1 truncate">
        {t("peerPanel.decision.permissionTitle", { tool: decision.toolName })}
      </span>
      <div className="flex shrink-0 items-center gap-1.5">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onDecide(true)}
        >
          {t("peerPanel.decision.allow")}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => onDecide(false)}
        >
          {t("peerPanel.decision.deny")}
        </Button>
      </div>
    </div>
  );
}
