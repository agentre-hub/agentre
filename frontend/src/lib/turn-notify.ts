import type { TFunction } from "i18next";

import { classifyTurnFailure, turnFailureFact } from "@agentre-hub/agentre-ui";

import type { chat_svc } from "../../wailsjs/go/models";
import type { AgentStatus } from "../stores/types";
import type { DoneEvent } from "../stores/session-status-store";
import type { NotificationSettings } from "../stores/notification-settings-store";

export type NotifyKind = "done" | "error" | "waiting";

// 通知正文里附带的摘要上限；系统通知/toast 都只够显示两三行，截断到此长度。
const SNIPPET_MAX = 120;

// clip 把多行/多空白压成单行并截断超长部分（末位用省略号顶到 SNIPPET_MAX）。
function clip(s: string): string {
  const oneLine = s.replace(/\s+/g, " ").trim();
  return oneLine.length > SNIPPET_MAX
    ? oneLine.slice(0, SNIPPET_MAX - 1) + "…"
    : oneLine;
}

// messageSnippet 取 agent 末条回复里最后一个非空 text 块作摘要（结论通常在末尾）。
function messageSnippet(message: chat_svc.ChatMessage | undefined): string {
  const blocks = message?.blocks;
  if (!blocks?.length) return "";
  for (let i = blocks.length - 1; i >= 0; i--) {
    const b = blocks[i];
    if (b.type === "text" && typeof b.text === "string" && b.text.trim()) {
      return clip(b.text);
    }
  }
  return "";
}

// notifyBody 在状态词后拼一段动态摘要：done 取 agent 末条回复（agent 原始输出，不翻译），
// error 取分类后的中文事实句（不贴 CLI 原文）；取不到（无消息 / 仅工具调用 / 无错误文案 /
// waiting）时退回纯状态词。uiT 须绑在 AGENTRE_UI_NAMESPACE 上。
export function notifyBody(
  kind: NotifyKind,
  event: DoneEvent | null,
  t: TFunction,
  uiT: TFunction,
): string {
  const base = t(`notify.body.${kind}`);
  let detail = "";
  if (kind === "done" && event?.kind === "done") {
    detail = messageSnippet(event.message);
  } else if (kind === "error" && event?.kind === "error") {
    const errText = event.error ?? event.message?.errorText;
    detail = errText ? turnFailureFact(classifyTurnFailure(errText), uiT) : "";
  }
  return detail ? `${base} · ${detail}` : base;
}

export type NotifyDeps = {
  isWindowFocused: () => boolean;
  getActiveSessionId: () => number | null;
  getSettings: () => NotificationSettings;
  getSessionTitle: (sessionId: number) => string | undefined;
  // getDoneEvent 取该 session 最近一次结束事件，供正文摘取 agent 末条回复 / 错误文案。
  getDoneEvent: (sessionId: number) => DoneEvent | null;
  showSystemNotification: (
    sessionId: number,
    title: string,
    body: string,
  ) => void;
  showToast: (
    sessionId: number,
    kind: NotifyKind,
    title: string,
    body: string,
  ) => void;
  t: TFunction;
  // 绑在 AGENTRE_UI_NAMESPACE 上的 t，取共享包的失败事实句。
  uiT: TFunction;
};

// classifyTransition 把一次 agentStatus 转换归类为通知类型；仅在「离开 running」时触发。
// 用户自己点停止（lastDoneEventKind==="aborted"）不通知。
export function classifyTransition(
  prev: AgentStatus | undefined,
  next: AgentStatus,
  lastDoneEventKind: DoneEvent["kind"] | undefined,
): NotifyKind | null {
  if (prev !== "running") return null;
  if (next === "error") return "error";
  if (next === "waiting") return "waiting";
  if (next === "idle") return lastDoneEventKind === "aborted" ? null : "done";
  return null;
}

// maybeNotify 在门槛通过时，按设置触发各启用渠道。
// onlyWhenUnfocused 开（默认）：仅窗口失焦时通知；关：失焦 或 非当前会话 时通知。
export function maybeNotify(
  sessionId: number,
  kind: NotifyKind,
  deps: NotifyDeps,
): void {
  const s = deps.getSettings();
  if (!s.enabled) return;
  const focused = deps.isWindowFocused();
  const suppress = s.onlyWhenUnfocused
    ? focused
    : focused && deps.getActiveSessionId() === sessionId;
  if (suppress) return;

  const title = deps.getSessionTitle(sessionId) ?? deps.t("notify.app");
  const body = notifyBody(kind, deps.getDoneEvent(sessionId), deps.t, deps.uiT);
  if (s.system) deps.showSystemNotification(sessionId, title, body);
  if (s.toast) deps.showToast(sessionId, kind, title, body);
}
