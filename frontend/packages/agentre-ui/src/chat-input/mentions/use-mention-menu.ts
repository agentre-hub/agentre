import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { Node as PMNode } from "@tiptap/pm/model";
import type { Editor } from "@tiptap/react";

import { MENTION_NODE_NAME } from "./mention-node";
import { rankMentionItems } from "./rank-items";
import { detectAtTrigger } from "./trigger";
import type { MentionItem, MentionMenuState, MentionSources } from "./types";

// ProseMirror 的 textBetween 默认给非文本 leaf 节点 0 个字符,但每个 leaf 在文档里
// 仍占 1 个位置 —— 于是「字符串下标」与「文档位置」按前面 leaf 的个数错位,
// deleteRange 会多吃掉前一个 chip(实测:3 个 mention 会吃掉中间那个)。
// 给每个 leaf 恰好 1 个字符即可对齐:hardBreak → "\n"(语义即换行,允许其后触发),
// 其它 atom(如 mention chip)→ "￼"(不可见占位,视作词内字符,不触发)。
// 注:slash-commands/use-slash-menu.ts 与 chat-input/mentions/use-mention-menu.ts
// 各自持有一份同样的 helper —— 两模块刻意不互相依赖。
function leafText(node: PMNode): string {
  return node.type.name === "hardBreak" ? "\n" : "￼";
}

function normalizeQuery(query: string): string {
  return query.trim().toLowerCase();
}

export function useMentionMenu({
  editor,
  sources,
  onPick,
}: {
  editor: Editor | null;
  sources: MentionSources;
  onPick?: (item: MentionItem) => void;
}): {
  state: MentionMenuState;
  onKeyDown: (event: KeyboardEvent) => boolean;
  pick: (item: MentionItem) => void;
  setSelectedIndex: (idx: number) => void;
  close: () => void;
} {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const [anchorRect, setAnchorRect] = useState<
    MentionMenuState["anchorRect"] | null
  >(null);
  const [selectedIndex, setSelectedIndex] = useState(0);

  // rankMentionItems 分组评分并保持 agents 在前、projects 在后。
  const items = useMemo(
    () => rankMentionItems(sources, query),
    [sources, query],
  );
  const sourceCount = sources.agents.length + sources.projects.length;
  const normalizedQuery = normalizeQuery(query);
  const previousNormalizedQuery = useRef(normalizedQuery);

  useEffect(() => {
    if (previousNormalizedQuery.current !== normalizedQuery) {
      previousNormalizedQuery.current = normalizedQuery;
      setSelectedIndex(0);
      return;
    }
    if (selectedIndex >= items.length) {
      setSelectedIndex(items.length > 0 ? items.length - 1 : 0);
    }
  }, [items.length, normalizedQuery, selectedIndex]);

  const close = useCallback(() => {
    setOpen(false);
    setAnchorRect(null);
    setQuery("");
    setSelectedIndex(0);
  }, []);

  useEffect(() => {
    if (!editor) return;
    const recompute = () => {
      if (sourceCount === 0) {
        if (open) close();
        return;
      }
      const { $from, empty } = editor.state.selection;
      if (!empty) {
        if (open) close();
        return;
      }
      const before = $from.parent.textBetween(
        0,
        $from.parentOffset,
        undefined,
        leafText,
      );
      const hit = detectAtTrigger(before);
      if (!hit) {
        if (open) close();
        return;
      }
      const triggerPos = $from.start() + hit.startOffset;
      let rect: MentionMenuState["anchorRect"];
      try {
        const c = editor.view.coordsAtPos(triggerPos);
        rect = { left: c.left, top: c.top, bottom: c.bottom };
      } catch {
        rect = null;
      }
      setQuery(hit.query);
      setAnchorRect(rect);
      setOpen(true);
    };
    editor.on("update", recompute);
    editor.on("selectionUpdate", recompute);
    return () => {
      editor.off("update", recompute);
      editor.off("selectionUpdate", recompute);
    };
  }, [editor, sourceCount, open, close]);

  const confirm = useCallback(
    (item: MentionItem) => {
      if (editor) {
        const { $from } = editor.state.selection;
        const before = $from.parent.textBetween(
          0,
          $from.parentOffset,
          undefined,
          leafText,
        );
        const hit = detectAtTrigger(before);
        if (hit) {
          const from = $from.start() + hit.startOffset;
          const to = $from.pos;
          editor
            .chain()
            .focus()
            .deleteRange({ from, to })
            .insertContent({
              type: MENTION_NODE_NAME,
              attrs: {
                kind: item.kind,
                refId: item.refId,
                label: item.label,
                path: item.path ?? "",
                color: item.color ?? "",
              },
            })
            .insertContent(" ")
            .run();
        }
      }
      close();
      onPick?.(item);
    },
    [close, editor, onPick],
  );

  const onKeyDown = useCallback(
    (event: KeyboardEvent): boolean => {
      if (!open || items.length === 0) return false;
      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          setSelectedIndex((i) => (i + 1) % items.length);
          return true;
        case "ArrowUp":
          event.preventDefault();
          setSelectedIndex((i) => (i - 1 + items.length) % items.length);
          return true;
        case "Enter":
        case "Tab": {
          event.preventDefault();
          const item = items[selectedIndex] ?? items[0];
          if (item) confirm(item);
          return true;
        }
        case "Escape":
          event.preventDefault();
          close();
          return true;
        default:
          return false;
      }
    },
    [open, items, selectedIndex, confirm, close],
  );

  const state: MentionMenuState = useMemo(
    () => ({ open, anchorRect, items, selectedIndex, query }),
    [open, anchorRect, items, selectedIndex, query],
  );

  return { state, onKeyDown, pick: confirm, setSelectedIndex, close };
}
