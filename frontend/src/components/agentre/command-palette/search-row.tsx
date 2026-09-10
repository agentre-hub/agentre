// 命令面板里的一行搜索结果(标题 + 副标题 + 快捷键提示 + 选中态)。
// 原先与整个面板挤在一个 879 行的文件里。

import * as React from "react";
import { Command as CommandPrimitive } from "cmdk";
import { Search, Terminal, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import { type ProjectFlat } from "@/hooks/use-project-list";
import { clearLastContext } from "@/stores/new-chat-context-persistence";
import { useNewChatContextStore } from "@/stores/new-chat-context-store";

import { COMMAND_PREFIX, type PaletteMode } from "./mode";

import { cycleProjectShortcut } from "./context-bar";

export type SearchRowProps = {
  query: string;
  mode: PaletteMode;
  payload: string;
  projects: ProjectFlat[];
  onQueryChange: (v: string) => void;
  onClose: () => void;
};

export function SearchRow({
  query,
  mode,
  payload,
  projects,
  onQueryChange,
  onClose,
}: SearchRowProps) {
  const { t } = useTranslation();
  // Input value 在 command 模式下显示 payload（不含 prefix），保证光标位置干净；
  // onValueChange 反向加回 prefix 写到 query state。底层 query 始终以 prefix 开头 → parseMode 单一真相。
  const inputValue = mode === "command" ? payload : query;
  const handleChange = React.useCallback(
    (v: string) => {
      if (mode === "command") {
        onQueryChange(COMMAND_PREFIX + (v.startsWith(" ") ? v : ` ${v}`));
      } else {
        onQueryChange(v);
      }
    },
    [mode, onQueryChange],
  );

  // 命令模式 + Input payload 空时按 Backspace 的优先级：
  //   1) 有 projectContext → 先清 context（保留命令模式）+ 清 localStorage
  //   2) 无 projectContext → 退出命令模式（删 chip）
  // 多按一次 Backspace 才退出 = 防误删 + 与 chip 操作惯例对齐。
  //
  // Tab 在命令模式下生效（默认模式不消费，走 native）：
  //   Tab → 循环切下一个项目
  //   Shift+Tab → 循环切上一个项目
  // 焦点始终留在 Input；preventDefault + stopPropagation 防止浏览器 native focus
  // 切换以及 cmdk 在 Command 根上消费 Tab。
  const handleKeyDown = React.useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (mode !== "command") return;
      if (e.key === "Backspace" && payload.length === 0) {
        e.preventDefault();
        const ctxStore = useNewChatContextStore.getState();
        if (ctxStore.projectContext) {
          ctxStore.setContext(null);
          clearLastContext();
        } else {
          onQueryChange("");
        }
        return;
      }
      if (e.key === "Tab") {
        e.preventDefault();
        e.stopPropagation();
        cycleProjectShortcut(projects, e.shiftKey ? -1 : 1);
      }
    },
    [mode, payload, projects, onQueryChange],
  );

  return (
    <div className="flex h-14 shrink-0 items-center gap-3 border-b border-border px-5">
      <Search
        className="size-[18px] shrink-0 text-muted-foreground"
        aria-hidden="true"
      />
      {mode === "command" ? (
        <span
          className="inline-flex h-[22px] shrink-0 items-center gap-1 rounded-sm border border-primary bg-primary/10 px-2 font-mono text-2xs font-semibold text-primary"
          aria-label={t("commandPalette.search.commandMode")}
          title={t("commandPalette.search.commandModeTitle")}
        >
          <Terminal className="size-3" aria-hidden="true" />
          {t("commandPalette.search.command")}
        </span>
      ) : null}
      <CommandPrimitive.Input
        autoFocus
        placeholder={
          mode === "command"
            ? t("commandPalette.search.commandPlaceholder")
            : t("commandPalette.search.sessionPlaceholder")
        }
        value={inputValue}
        onValueChange={handleChange}
        onKeyDown={handleKeyDown}
        className="flex-1 bg-transparent text-base outline-none placeholder:text-muted-foreground"
      />
      {query ? (
        <button
          type="button"
          aria-label={t("commandPalette.search.clear")}
          title={t("commandPalette.search.clear")}
          onClick={() => onQueryChange("")}
          className="inline-flex size-[22px] shrink-0 items-center justify-center rounded-sm bg-secondary text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <X className="size-3" aria-hidden="true" />
        </button>
      ) : null}
      <button
        type="button"
        onClick={onClose}
        className="inline-flex h-5 shrink-0 items-center justify-center rounded-sm border border-border bg-secondary px-1.5 font-mono text-2xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        aria-label={t("commandPalette.close")}
      >
        Esc
      </button>
    </div>
  );
}
