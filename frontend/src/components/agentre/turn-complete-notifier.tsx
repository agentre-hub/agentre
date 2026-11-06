import * as React from "react";
import { useTranslation } from "react-i18next";

import { useUiTranslation } from "@agentre-hub/agentre-ui";

import { ShowNotification } from "../../../wailsjs/go/app/App";
import { EventsOn, EventsOff } from "../../../wailsjs/runtime/runtime";
import { isWindowFocused } from "../../lib/window-focus";
import {
  classifyTransition,
  maybeNotify,
  type NotifyDeps,
} from "../../lib/turn-notify";
import { useChatTabsStore } from "../../stores/chat-tabs-store";
import { useNotificationSettingsStore } from "../../stores/notification-settings-store";
import { useNotificationToastStore } from "../../stores/notification-toast-store";
import { useSessionStatusStore } from "../../stores/session-status-store";

function activeSessionId(): number | null {
  const st = useChatTabsStore.getState();
  const tab = st.tabs.find((x) => x.id === st.activeTabId);
  return tab?.meta.kind === "session" ? tab.meta.sessionId : null;
}

function sessionTitle(sessionId: number): string | undefined {
  return useChatTabsStore
    .getState()
    .tabs.find(
      (x) => x.meta.kind === "session" && x.meta.sessionId === sessionId,
    )?.title;
}

// TurnCompleteNotifier 常驻 App 根、不渲染任何 UI；订阅 session 状态转换并在合适时机通知。
export function TurnCompleteNotifier(): null {
  const { t } = useTranslation();
  const { t: uiT } = useUiTranslation();

  const deps = React.useMemo<NotifyDeps>(
    () => ({
      isWindowFocused,
      getActiveSessionId: activeSessionId,
      getSettings: () => useNotificationSettingsStore.getState().settings,
      getSessionTitle: sessionTitle,
      getDoneEvent: (sessionId) =>
        useSessionStatusStore.getState().statuses.get(sessionId)
          ?.lastDoneEvent ?? null,
      showSystemNotification: (sessionId, title, body) => {
        ShowNotification({ title, body, sessionId }).catch(() => {});
      },
      showToast: (sessionId, kind, title, body) => {
        useNotificationToastStore
          .getState()
          .push({ sessionId, kind, title, body });
      },
      t,
      uiT,
    }),
    [t, uiT],
  );

  React.useEffect(() => {
    void useNotificationSettingsStore.getState().load();
  }, []);

  React.useEffect(() => {
    EventsOn("notification:click", (sessionId: number) => {
      useChatTabsStore.getState().openSession(sessionId);
    });
    return () => EventsOff("notification:click");
  }, []);

  // 会话被打开（切成当前会话）：它的 waiting / error 通知已经没有要叫你回来的事了。
  React.useEffect(() => {
    let prevActive = activeSessionId();
    return useChatTabsStore.subscribe(() => {
      const active = activeSessionId();
      if (active === prevActive) return;
      prevActive = active;
      if (active !== null) {
        useNotificationToastStore.getState().dismissBySession(active);
      }
    });
  }, []);

  React.useEffect(() => {
    const prev = new Map<number, string>();
    const prevAttention = new Map<number, boolean>();
    for (const [id, v] of useSessionStatusStore.getState().statuses) {
      prev.set(id, v.agentStatus);
      prevAttention.set(id, v.needsAttention);
    }
    const unsub = useSessionStatusStore.subscribe((state) => {
      for (const [id, v] of state.statuses) {
        // attention 清除（审批已处理）或新一轮开始：该会话的旧通知作废。
        const attentionBefore = prevAttention.get(id) ?? false;
        prevAttention.set(id, v.needsAttention);
        const before = prev.get(id);
        const after = v.agentStatus;
        if (
          (attentionBefore && !v.needsAttention) ||
          (after === "running" && before !== "running")
        ) {
          useNotificationToastStore.getState().dismissBySession(id);
        }
        if (before === after) continue;
        prev.set(id, after);
        const kind = classifyTransition(
          before as never,
          after,
          v.lastDoneEvent?.kind,
        );
        if (kind) maybeNotify(id, kind, deps);
      }
    });
    return unsub;
  }, [deps]);

  return null;
}
