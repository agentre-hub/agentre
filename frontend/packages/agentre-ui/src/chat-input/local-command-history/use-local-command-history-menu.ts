import {
  useCallback,
  useLayoutEffect,
  useRef,
  useState,
  type FocusEvent as ReactFocusEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type RefObject,
} from "react";

import type { Editor } from "@tiptap/react";
import { toast } from "sonner";

import { useUiTranslation } from "../../i18n";
import { normalizeSuggestionQuery } from "../../lib/suggestion-score";
import { extractPlainText } from "../content";
import type { ProseMirrorLikeNode, TipTapDocNode } from "../types";
import { useOptionalLocalCommandHistoryAccess } from "./access";
import { rankLocalCommandHistory } from "./rank";
import type {
  LocalCommandHistoryEntry,
  LocalCommandHistoryMenuState,
  LocalCommandHistoryScope,
} from "./types";

function closedState(query = ""): LocalCommandHistoryMenuState {
  return {
    open: false,
    anchorRect: null,
    items: [],
    selectedIndex: 0,
    clearFocused: false,
    query,
  };
}

function commandQuery(
  text: string,
): { bangIndex: number; query: string } | null {
  const leadingWhitespace = text.length - text.trimStart().length;
  if (text[leadingWhitespace] !== "!") return null;
  return {
    bangIndex: leadingWhitespace,
    query: text.slice(leadingWhitespace + 1),
  };
}

function plainTextDoc(content: string): TipTapDocNode {
  return {
    type: "doc",
    content: content
      .split("\n")
      .map((line) =>
        line
          ? { type: "paragraph", content: [{ type: "text", text: line }] }
          : { type: "paragraph" },
      ),
  };
}

function sameRect(
  left: LocalCommandHistoryMenuState["anchorRect"],
  right: LocalCommandHistoryMenuState["anchorRect"],
): boolean {
  if (left === right) return true;
  if (!left || !right) return false;
  return (
    left.left === right.left &&
    left.top === right.top &&
    left.bottom === right.bottom
  );
}

function focusNextAfterEditor(editorElement: HTMLElement): void {
  const focusable = Array.from(
    document.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  );
  const editorIndex = focusable.indexOf(editorElement);
  focusable[editorIndex + 1]?.focus();
}

export function useLocalCommandHistoryMenu({
  editor,
  scope,
}: {
  editor: Editor | null;
  scope?: LocalCommandHistoryScope;
}): {
  state: LocalCommandHistoryMenuState;
  onKeyDown: (event: KeyboardEvent) => boolean;
  pick: (entry: LocalCommandHistoryEntry) => void;
  setSelectedIndex: (index: number) => void;
  clearButtonRef: RefObject<HTMLButtonElement | null>;
  clear: () => void;
  dismissCurrent: () => void;
  onClearFocus: () => void;
  onClearBlur: (event: ReactFocusEvent<HTMLButtonElement>) => void;
  onClearKeyDown: (event: ReactKeyboardEvent<HTMLButtonElement>) => void;
} {
  const { t } = useUiTranslation();
  // 宿主没挂 Provider → history 恒为 null,下面每条路径都退化成「菜单永远关着」。
  const history = useOptionalLocalCommandHistoryAccess();
  const [state, setState] = useState<LocalCommandHistoryMenuState>(() =>
    closedState(),
  );
  const stateRef = useRef(state);
  const suppressedQueryRef = useRef<string | null>(null);
  const clearingRef = useRef(false);
  const clearButtonRef = useRef<HTMLButtonElement>(null);
  const deviceId = scope?.deviceId;
  const cwd = scope?.cwd;

  useLayoutEffect(() => {
    stateRef.current = state;
  }, [state]);

  useLayoutEffect(() => {
    suppressedQueryRef.current = null;
    if (!history || !editor || deviceId === undefined || cwd === undefined) {
      setState(closedState());
      return;
    }
    const activeScope: LocalCommandHistoryScope = { deviceId, cwd };
    const activeScopeKey = history.deriveScopeKey(activeScope);

    const recompute = () => {
      const { $from, empty } = editor.state.selection;
      if (!empty) {
        setState(closedState());
        return;
      }

      const text = extractPlainText(
        editor.state.doc as unknown as ProseMirrorLikeNode,
      );
      const hit = commandQuery(text);
      if (!hit) {
        suppressedQueryRef.current = null;
        setState(closedState());
        return;
      }

      if (suppressedQueryRef.current === hit.query) {
        setState(closedState(hit.query));
        return;
      }
      if (suppressedQueryRef.current !== null) {
        suppressedQueryRef.current = null;
      }

      let entries: LocalCommandHistoryEntry[];
      try {
        entries = history.list(activeScope);
      } catch (error) {
        console.warn(
          "[chat-input] failed to read local command history",
          error,
        );
        setState(closedState(hit.query));
        return;
      }
      const items = rankLocalCommandHistory(entries, hit.query);
      if (items.length === 0) {
        setState(closedState(hit.query));
        return;
      }

      let anchorRect: LocalCommandHistoryMenuState["anchorRect"] = null;
      try {
        const rect = editor.view.coordsAtPos($from.pos);
        anchorRect = {
          left: rect.left,
          top: rect.top,
          bottom: rect.bottom,
        };
      } catch {
        setState(closedState(hit.query));
        return;
      }

      setState((previous) => {
        const queryChanged =
          normalizeSuggestionQuery(previous.query) !==
          normalizeSuggestionQuery(hit.query);
        const selectedIndex = queryChanged
          ? 0
          : Math.min(previous.selectedIndex, items.length - 1);
        const clearFocused = queryChanged ? false : previous.clearFocused;
        if (
          previous.open &&
          previous.query === hit.query &&
          previous.selectedIndex === selectedIndex &&
          previous.clearFocused === clearFocused &&
          sameRect(previous.anchorRect, anchorRect) &&
          previous.items.length === items.length &&
          previous.items.every(
            (entry, index) =>
              entry.command === items[index]?.command &&
              entry.lastUsedAt === items[index]?.lastUsedAt,
          )
        ) {
          return previous;
        }
        return {
          open: true,
          anchorRect,
          items,
          selectedIndex,
          clearFocused,
          query: hit.query,
        };
      });
    };

    const unsubscribe = history.subscribe((mutation) => {
      if (mutation.scopeKey !== activeScopeKey || clearingRef.current) return;
      if (mutation.type === "clear" && stateRef.current.open) {
        const text = extractPlainText(
          editor.state.doc as unknown as ProseMirrorLikeNode,
        );
        const hit = commandQuery(text);
        suppressedQueryRef.current = hit?.query ?? null;
      }
      recompute();
    });
    editor.on("update", recompute);
    editor.on("selectionUpdate", recompute);
    recompute();
    return () => {
      unsubscribe();
      editor.off("update", recompute);
      editor.off("selectionUpdate", recompute);
    };
  }, [cwd, deviceId, editor, history]);

  const dismiss = useCallback((query: string) => {
    suppressedQueryRef.current = query;
    setState(closedState(query));
  }, []);

  // 零参版本的 dismiss:供弹层「点击外部关闭」使用,读取当前 state 的 query 做抑制,
  // 保证点击外部后菜单不会因为文本仍以 ! 开头而立刻重新弹出。
  const dismissCurrent = useCallback(() => {
    dismiss(stateRef.current.query);
  }, [dismiss]);

  const pick = useCallback(
    (entry: LocalCommandHistoryEntry) => {
      if (!editor) return;
      const text = extractPlainText(
        editor.state.doc as unknown as ProseMirrorLikeNode,
      );
      const hit = commandQuery(text);
      if (!hit) {
        setState(closedState());
        return;
      }

      const nextText = `${text.slice(0, hit.bangIndex + 1)}${entry.command}`;
      dismiss(entry.command);
      editor.commands.setContent(plainTextDoc(nextText));
      editor.commands.focus("end");
    },
    [dismiss, editor],
  );

  const setSelectedIndex = useCallback((index: number) => {
    setState((previous) => {
      if (previous.items.length === 0) return previous;
      const selectedIndex = Math.max(
        0,
        Math.min(index, previous.items.length - 1),
      );
      return selectedIndex === previous.selectedIndex && !previous.clearFocused
        ? previous
        : { ...previous, selectedIndex, clearFocused: false };
    });
  }, []);

  const focusClear = useCallback(() => {
    setState((previous) =>
      previous.open && !previous.clearFocused
        ? { ...previous, clearFocused: true }
        : previous,
    );
    clearButtonRef.current?.focus();
  }, []);

  const focusEditorOption = useCallback(
    (index: number) => {
      setSelectedIndex(index);
      editor?.commands.focus();
    },
    [editor, setSelectedIndex],
  );

  const clear = useCallback(() => {
    if (!history || deviceId === undefined || cwd === undefined) return;
    let cleared: boolean;
    clearingRef.current = true;
    try {
      cleared = history.clear({ deviceId, cwd });
    } finally {
      clearingRef.current = false;
    }
    if (!cleared) {
      toast.error(t("localCommandHistory.clearFailed"));
      return;
    }
    dismiss(state.query);
    editor?.commands.focus();
  }, [cwd, deviceId, dismiss, editor, history, state.query, t]);

  const onClearFocus = useCallback(() => {
    setState((previous) =>
      previous.open && !previous.clearFocused
        ? { ...previous, clearFocused: true }
        : previous,
    );
  }, []);

  const onClearBlur = useCallback(
    (event: ReactFocusEvent<HTMLButtonElement>) => {
      if (event.relatedTarget === editor?.view.dom) {
        setSelectedIndex(state.selectedIndex);
        return;
      }
      dismiss(state.query);
    },
    [dismiss, editor, setSelectedIndex, state.query, state.selectedIndex],
  );

  const onClearKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLButtonElement>) => {
      switch (event.key) {
        case "Tab":
          event.preventDefault();
          if (event.shiftKey) {
            focusEditorOption(state.selectedIndex);
          } else if (editor) {
            focusNextAfterEditor(editor.view.dom);
          }
          break;
        case "ArrowDown":
          event.preventDefault();
          focusEditorOption(0);
          break;
        case "ArrowUp":
          event.preventDefault();
          focusEditorOption(state.items.length - 1);
          break;
        case "Escape":
          event.preventDefault();
          dismiss(state.query);
          editor?.commands.focus();
          break;
      }
    },
    [
      dismiss,
      editor,
      focusEditorOption,
      state.items.length,
      state.query,
      state.selectedIndex,
    ],
  );

  const onKeyDown = useCallback(
    (event: KeyboardEvent): boolean => {
      if (!state.open || state.items.length === 0) return false;
      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          if (state.selectedIndex === state.items.length - 1) {
            focusClear();
          } else {
            setSelectedIndex(state.selectedIndex + 1);
          }
          return true;
        case "ArrowUp":
          event.preventDefault();
          if (state.selectedIndex === 0) {
            focusClear();
          } else {
            setSelectedIndex(state.selectedIndex - 1);
          }
          return true;
        case "Enter": {
          event.preventDefault();
          const entry = state.items[state.selectedIndex] ?? state.items[0];
          if (entry) pick(entry);
          return true;
        }
        case "Tab": {
          if (
            event.shiftKey ||
            event.metaKey ||
            event.ctrlKey ||
            event.altKey
          ) {
            return false;
          }
          event.preventDefault();
          const entry = state.items[state.selectedIndex] ?? state.items[0];
          if (entry) pick(entry);
          return true;
        }
        case "Escape":
          event.preventDefault();
          dismiss(state.query);
          return true;
        default:
          return false;
      }
    },
    [dismiss, focusClear, pick, setSelectedIndex, state],
  );

  return {
    state,
    onKeyDown,
    pick,
    setSelectedIndex,
    clearButtonRef,
    clear,
    dismissCurrent,
    onClearFocus,
    onClearBlur,
    onClearKeyDown,
  };
}
