import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { Node as PMNode } from "@tiptap/pm/model";
import type { Editor } from "@tiptap/react";

import { normalizeSuggestionQuery } from "../../lib/suggestion-score";

import { filterByQuery } from "./filter";
import { detectSlashTrigger } from "./trigger";
import type { SlashCommand, SlashExec } from "./types";

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

// SlashMenuState 暴露给消费者 (chat-input) 的运行时状态。
// open=false 时 anchor/items/query 视作未定义,UI 不应渲染弹层。
export type SlashMenuState = {
  open: boolean;
  // 弹层锚定的视口坐标 (px),用 editor.view.coordsAtPos(triggerPos) 拿到。
  anchorRect: { left: number; top: number; bottom: number } | null;
  // 当前候选(按 query 过滤后)。
  items: SlashCommand[];
  // 用户当前高亮的下标;items 变化时会被 clamp 回 0..items.length-1。
  selectedIndex: number;
  query: string;
  trigger: "/" | "$";
};

function sameRect(
  a: SlashMenuState["anchorRect"] | null,
  b: SlashMenuState["anchorRect"] | null,
): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.left === b.left && a.top === b.top && a.bottom === b.bottom;
}

// 默认值必须是模块级常量：写成行内 `[]` 会让每次 render 都产生新身份，下面
// triggers / items 两个 useMemo 跟着变，订阅 editor 的 effect 便每次提交都重跑。
const EMPTY_COMMANDS: SlashCommand[] = [];

export type UseSlashMenuOpts = {
  // 编辑器实例;TipTap useEditor 返回的 Editor。null 时 hook noop。
  editor: Editor | null;
  // 当前会话的 backend 类型;选中时透传给 `SlashCommand.resolve`。
  backendType: string;
  // 当前 backend 下可用的完整命令清单(静态注册表 + skill 命令,宿主已合并过滤)。
  // 清单为什么归宿主见 `./types.ts`。空清单 → 菜单永远不开。
  commands?: SlashCommand[];
  // 用户选中命令时调用;literal_text 由 chat-input 直接把文本填回编辑器(不自动发送),
  // rpc 由 chat-input 转交给宿主。
  onSelect: (cmd: SlashCommand, exec: SlashExec) => void;
};

// useSlashMenu 监听 TipTap selection/update 事件,实时检测 slash trigger,维护
// 弹层 open / anchorRect / items / selectedIndex 状态;暴露键盘 navigation /
// confirm / close 给消费者(由 chat-input 在 handleKeyDown 里调用)。
//
// 返回:
//   state           当前弹层状态(open + 候选 + 高亮);UI 渲染入口。
//   onKeyDown       供 editorProps.handleKeyDown 调用;返回 true 表示已消化按键。
//   close           手动关闭弹层(选中命令后 chat-input 也会调一次兜底)。
export function useSlashMenu({
  editor,
  backendType,
  commands = EMPTY_COMMANDS,
  onSelect,
}: UseSlashMenuOpts): {
  state: SlashMenuState;
  onKeyDown: (event: KeyboardEvent) => boolean;
  // pick 是统一的"选中命令"入口,鼠标点击 + 键盘 Enter/Tab 都走这条。
  // 内部负责清掉 / + query 段、关弹层、回调 onSelect。
  pick: (cmd: SlashCommand) => void;
  // setSelectedIndex 让消费者(SlashPopover)在 mouseenter 时同步高亮态,
  // 避免鼠标和键盘两套高亮打架。
  setSelectedIndex: (idx: number) => void;
  close: () => void;
} {
  const [query, setQuery] = useState<string>("");
  const [open, setOpen] = useState(false);
  const [anchorRect, setAnchorRect] = useState<
    SlashMenuState["anchorRect"] | null
  >(null);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [trigger, setTrigger] = useState<"/" | "$">("/");
  // 弹层开关的 ref 镜像 —— 供 recompute 读取最新 open 而不把它塞进 effect 依赖。
  // open 进依赖会让 close() 后的 effect 重跑 + trailing recompute() 立刻把菜单重新打开。
  const openRef = useRef(open);
  useEffect(() => {
    openRef.current = open;
  }, [open]);

  const available = commands;
  const triggers = useMemo(
    () => Array.from(new Set(available.map((command) => command.trigger))),
    [available],
  );
  const items = useMemo(
    () => filterByQuery(available, query, trigger),
    [available, query, trigger],
  );
  const normalizedQuery = normalizeSuggestionQuery(query);
  const previousNormalizedQuery = useRef(normalizedQuery);

  // 规范化查询变化时默认选中最高分；同一查询的编辑器重复事件不抢回高亮。
  // 候选源变化但查询不变时，只把 selectedIndex 拉回有效范围。
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
    setTrigger("/");
    setSelectedIndex(0);
  }, []);

  // 监听编辑器 update / selectionUpdate,刷新触发态。
  useEffect(() => {
    if (!editor) return;
    const recompute = () => {
      if (available.length === 0) {
        if (openRef.current) close();
        return;
      }
      const { state } = editor;
      const { $from, empty } = state.selection;
      if (!empty) {
        if (openRef.current) close();
        return;
      }
      const before = $from.parent.textBetween(
        0,
        $from.parentOffset,
        undefined,
        leafText,
      );
      const hit = detectSlashTrigger(before, triggers);
      if (!hit) {
        if (openRef.current) close();
        return;
      }
      const triggerPos = $from.start() + hit.startOffset;
      let rect: SlashMenuState["anchorRect"];
      try {
        const c = editor.view.coordsAtPos(triggerPos);
        rect = { left: c.left, top: c.top, bottom: c.bottom };
      } catch {
        rect = null;
      }
      setQuery(hit.query);
      setTrigger(hit.trigger);
      // 按值比较：coordsAtPos 每次都返回新对象，直接 set 等于每次 recompute 都
      // 保证一次重渲染；只要 effect 依赖再抖一下就成环（editor 事件 → setState →
      // 重渲染 → effect 重订阅并再 recompute → …）。
      setAnchorRect((prev) => (sameRect(prev, rect) ? prev : rect));
      setOpen(true);
    };
    editor.on("update", recompute);
    editor.on("selectionUpdate", recompute);
    recompute();
    return () => {
      editor.off("update", recompute);
      editor.off("selectionUpdate", recompute);
    };
  }, [editor, available, triggers, close]);

  // 选中命令:用 ProseMirror deleteRange 把 / + query 段从编辑器去掉,再回调 onSelect。
  // literal_text 由 chat-input 把完整命令文本填回编辑器(不自动发送);rpc 由 chat-input
  // 转交给上层调用 handler。这里只负责"清掉 trigger 文本 + 关弹层 + 通知 onSelect"。
  const confirm = useCallback(
    (cmd: SlashCommand) => {
      const exec = cmd.resolve(backendType);
      if (!exec) {
        close();
        return;
      }
      if (editor) {
        const { state } = editor;
        const { $from } = state.selection;
        const before = $from.parent.textBetween(
          0,
          $from.parentOffset,
          undefined,
          leafText,
        );
        const hit = detectSlashTrigger(before, triggers);
        if (hit) {
          const from = $from.start() + hit.startOffset;
          const to = $from.pos;
          editor.chain().focus().deleteRange({ from, to }).run();
        }
      }
      close();
      onSelect(cmd, exec);
    },
    [backendType, close, editor, onSelect, triggers],
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
          const cmd = items[selectedIndex] ?? items[0];
          if (cmd) confirm(cmd);
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

  const state: SlashMenuState = useMemo(
    () => ({ open, anchorRect, items, selectedIndex, query, trigger }),
    [open, anchorRect, items, selectedIndex, query, trigger],
  );

  return { state, onKeyDown, pick: confirm, setSelectedIndex, close };
}
