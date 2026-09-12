/**
 * 编辑器的两个热点回调：按键与内容更新。
 *
 * 它们住在 `useEditor` 的选项里，却几乎全靠 ref 读值——编辑器只在挂载时建一次，
 * 把 props 直接闭包进去会永远读到第一版。所以这里收的是一包 ref 而不是值，
 * 组件那侧只剩「哪些 ref 喂给谁」这一件事看得见。
 */
import type { Editor } from "@tiptap/react";
import type { EditorView } from "@tiptap/pm/view";

import { extractPlainText } from "./content";
import {
  applyInputHistoryMessage,
  getInputHistoryNavigationState,
  shouldContinueInputHistory,
  shouldIgnoreEditorShortcut,
  shouldStartInputHistory,
} from "./keyboard";
import type { ProseMirrorLikeNode } from "./types";

/** 只读 `.current` 的 ref 形状：`useRef` 与 `RefObject` 都能直接喂进来。 */
type Cell<T> = { current: T };

export interface ComposerKeyDownContext {
  editor: Editor | null | undefined;
  commandModeRef: Cell<boolean>;
  commandHistoryKeyDownRef: Cell<(event: KeyboardEvent) => boolean>;
  mentionKeyDownRef: Cell<(event: KeyboardEvent) => boolean>;
  slashKeyDownRef: Cell<(event: KeyboardEvent) => boolean>;
  sendOnEnterRef: Cell<boolean>;
  historyRef: Cell<string[]>;
  historyIndexRef: Cell<number>;
  applyingHistoryRef: Cell<boolean>;
  triggerSubmitRef: Cell<() => void>;
}

export function handleComposerKeyDown(
  ctx: ComposerKeyDownContext,
  view: EditorView,
  event: KeyboardEvent,
): boolean {
  const { editor } = ctx;
  if (!editor) return false;
  // 组词中（包括 keyCode===229 的兜底）一律放行给浏览器，
  // 避免 IME 候选回车被当成消息发送。
  if (shouldIgnoreEditorShortcut(view, event)) return false;
  // ! 历史菜单优先消费候选导航/选择；普通模式继续交给 mention/slash。
  if (ctx.commandModeRef.current && ctx.commandHistoryKeyDownRef.current(event))
    return true;
  if (
    !ctx.commandModeRef.current &&
    (ctx.mentionKeyDownRef.current(event) || ctx.slashKeyDownRef.current(event))
  )
    return true;

  const shouldSendOnEnter = ctx.sendOnEnterRef.current;
  const isEnter = event.key === "Enter";
  const mod = event.ctrlKey || event.metaKey;

  if (
    (event.key === "ArrowUp" || event.key === "ArrowDown") &&
    !event.altKey &&
    !event.ctrlKey &&
    !event.metaKey &&
    !event.shiftKey
  ) {
    const currentContent = extractPlainText(
      editor.state.doc as unknown as ProseMirrorLikeNode,
    );
    const nextHistoryState = getInputHistoryNavigationState({
      direction: event.key === "ArrowUp" ? "up" : "down",
      currentText: currentContent,
      historyIndex: ctx.historyIndexRef.current,
      userMessageHistory: ctx.historyRef.current,
      canStartHistory: shouldStartInputHistory(editor),
      canContinueHistory: shouldContinueInputHistory(editor),
    });

    if (nextHistoryState) {
      event.preventDefault();
      ctx.historyIndexRef.current = nextHistoryState.nextHistoryIndex;
      ctx.applyingHistoryRef.current = true;
      applyInputHistoryMessage(editor, nextHistoryState.nextMessage);
      return true;
    }
  }

  if (isEnter && shouldSendOnEnter && !event.shiftKey && !mod) {
    event.preventDefault();
    ctx.triggerSubmitRef.current();
    return true;
  }
  if (isEnter && !shouldSendOnEnter && mod) {
    event.preventDefault();
    ctx.triggerSubmitRef.current();
    return true;
  }
  return false;
}

export interface ComposerUpdateContext {
  applyingHistoryRef: Cell<boolean>;
  historyIndexRef: Cell<number>;
  lastIsEmptyRef: Cell<boolean | null>;
  onEmptyChangeRef: Cell<((empty: boolean) => void) | undefined>;
  commandModeRef: Cell<boolean>;
  onCommandModeChangeRef: Cell<((active: boolean) => void) | undefined>;
}

export function handleComposerUpdate(
  ctx: ComposerUpdateContext,
  ed: Editor,
): void {
  if (ctx.applyingHistoryRef.current) {
    ctx.applyingHistoryRef.current = false;
  } else {
    ctx.historyIndexRef.current = -1;
  }

  const isEmpty = ed.isEmpty;
  if (ctx.lastIsEmptyRef.current !== isEmpty) {
    ctx.lastIsEmptyRef.current = isEmpty;
    ctx.onEmptyChangeRef.current?.(isEmpty);
  }

  // 命令模式检测:首字符是 ! 则进入命令模式,去重避免每次 keystroke 都回调。
  const text = extractPlainText(ed.state.doc as unknown as ProseMirrorLikeNode);
  const inCommandMode = text.trimStart().startsWith("!");
  if (inCommandMode !== ctx.commandModeRef.current) {
    ctx.commandModeRef.current = inCommandMode;
    ctx.onCommandModeChangeRef.current?.(inCommandMode);
  }
}
