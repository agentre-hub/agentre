import * as React from "react";

import { useUiTranslation } from "../../i18n";
import { cn } from "../../lib/utils";
import { SuggestionPopover } from "../suggestion-popover";

import type { SlashCommand } from "./types";
import type { SlashMenuState } from "./use-slash-menu";

// SlashPopover 是 / 命令下拉的视觉层。位置走 fixed,以光标视口坐标为锚点;
// 出现在光标上方(优先 top 方向以避免遮挡正在键入的字符)。
//
// 键盘选中(Up/Down/Enter)在 useSlashMenu 里处理,本组件只负责:
//   - 渲染候选列表
//   - 高亮 selectedIndex 项
//   - 鼠标 hover → 把 selectedIndex 更新到该项
//   - 鼠标点击 → 触发 onPick
//
// 鼠标 hover 与键盘高亮共享同一个 selectedIndex,避免出现两个高亮态打架。
export function SlashPopover({
  state,
  onPick,
  onHover,
  onDismiss,
  editorElement,
}: {
  state: SlashMenuState;
  onPick: (cmd: SlashCommand) => void;
  onHover: (idx: number) => void;
  onDismiss?: () => void;
  editorElement?: HTMLElement | null;
}): React.ReactElement | null {
  const { t } = useUiTranslation();

  return (
    <SuggestionPopover
      open={state.open}
      anchorRect={state.anchorRect}
      selectedIndex={state.selectedIndex}
      itemCount={state.items.length}
      ariaLabel={t("slashCommands.aria")}
      testId="command-suggestions"
      onDismiss={onDismiss}
      editorElement={editorElement}
    >
      {(activeRef) =>
        state.items.map((cmd, idx) => {
          const active = idx === state.selectedIndex;
          return (
            <button
              key={`${cmd.trigger}:${cmd.name}`}
              type="button"
              role="option"
              ref={active ? activeRef : undefined}
              aria-selected={active}
              onMouseMove={() => onHover(idx)}
              onMouseDown={(e) => {
                // mousedown 而非 click —— 避免编辑器先 blur 再 click,弹层早就关了。
                e.preventDefault();
                onPick(cmd);
              }}
              className={cn(
                "flex w-full cursor-pointer items-center justify-between gap-3 rounded-sm px-2 py-1.5 text-left text-xs",
                active
                  ? "bg-accent text-accent-foreground"
                  : "text-foreground hover:bg-accent/60",
              )}
            >
              <span className="font-mono font-medium">{cmd.label}</span>
              {cmd.description ? (
                <span className="truncate text-muted-foreground">
                  {cmd.description}
                </span>
              ) : null}
            </button>
          );
        })
      }
    </SuggestionPopover>
  );
}
