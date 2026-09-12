import type { Editor } from "@tiptap/react";
import type { EditorView } from "@tiptap/pm/view";

import { buildEditorDocFromMessage } from "./content";
import type { AIChatInputDraft, InputHistoryNavigationOptions } from "./types";

// IME 守卫：组词中（包括 Safari/Chromium 触发 keyCode===229 的兜底）一律放行给浏览器。
// 注：keyCode 在标准里已废弃但仍是部分浏览器 IME 组词阶段唯一可靠的信号（Safari 中文输入法），
// 不能简单移除。这里显式断言一下避免 ts 死板的 deprecation 提示。
export function shouldIgnoreEditorShortcut(
  view: EditorView,
  event: KeyboardEvent,
): boolean {
  const legacyKeyCode = (event as KeyboardEvent & { keyCode?: number }).keyCode;
  return view.composing || event.isComposing || legacyKeyCode === 229;
}

export function shouldStartInputHistory(editor: Editor) {
  const { selection } = editor.state;
  return selection.empty && selection.from === 1;
}

export function shouldContinueInputHistory(editor: Editor) {
  return editor.state.selection.empty;
}

export function getInputHistoryNavigationState({
  direction,
  currentText,
  historyIndex,
  userMessageHistory,
  canStartHistory,
  canContinueHistory,
}: InputHistoryNavigationOptions) {
  const currentHistoryMessage =
    historyIndex >= 0 ? userMessageHistory[historyIndex] : null;
  const isBrowsingHistory =
    currentHistoryMessage != null && currentText === currentHistoryMessage;
  const canNavigate = isBrowsingHistory ? canContinueHistory : canStartHistory;

  if (!canNavigate) return null;
  if (direction === "up" && userMessageHistory.length === 0) return null;
  if (direction === "down" && (!isBrowsingHistory || historyIndex < 0))
    return null;

  const nextHistoryIndex =
    direction === "up"
      ? Math.min(historyIndex + 1, userMessageHistory.length - 1)
      : historyIndex - 1;
  const nextMessage =
    nextHistoryIndex >= 0 ? userMessageHistory[nextHistoryIndex] : "";

  return { nextHistoryIndex, nextMessage };
}

export function applyInputHistoryMessage(
  editor: Editor,
  nextMessage: string | AIChatInputDraft,
) {
  editor.commands.setContent(buildEditorDocFromMessage(nextMessage));
  editor.commands.focus("end");
}
