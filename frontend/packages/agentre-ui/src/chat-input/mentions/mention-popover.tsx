import * as React from "react";

import { useUiTranslation } from "../../i18n";
import { tokenToCssColor } from "../../lib/agent-color";
import { cn } from "../../lib/utils";

import { SuggestionPopover } from "../suggestion-popover";
import type { MentionItem, MentionMenuState } from "./types";
import type { MentionKind } from "./xml";

// 分组标题。三个键写死成字面量而不是 `mentions.group.${kind}s` 拼接 ——
// 键要能被静态扫出来。
function groupLabel(kind: MentionKind, t: (key: string) => string): string {
  if (kind === "agent") return t("mentions.group.agents");
  if (kind === "project") return t("mentions.group.projects");
  return t("mentions.group.devices");
}

// MentionPopover 视觉层:光标上方 fixed 弹层,agent / project / device 分组渲染。
// 键盘选中在 useMentionMenu 里处理;本组件只渲染 + 鼠标 hover/点击。
// items 已按 agents → projects → devices 排好序,selectedIndex 是扁平下标。
//
// 高度:弹层向上生长,所以上限同时受 MAX_HEIGHT 和「光标上方剩余空间」约束,
// 超出部分滚动。列表限高后,键盘高亮项会跑到滚动区外 —— 故 selectedIndex 变化时
// 把它拉回可视区,分组标题 sticky 以免滚动后丢失「Agents / Projects」上下文。
export function MentionPopover({
  state,
  onPick,
  onHover,
  onDismiss,
  editorElement,
}: {
  state: MentionMenuState;
  onPick: (item: MentionItem) => void;
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
      ariaLabel={t("mentions.aria")}
      onDismiss={onDismiss}
      editorElement={editorElement}
    >
      {(activeRef) =>
        state.items.map((item, idx) => {
          const active = idx === state.selectedIndex;
          const prevKind = idx > 0 ? state.items[idx - 1].kind : null;
          const showHeader = item.kind !== prevKind;
          // 设备没有调色板颜色,它的点表达的是可达性 —— 与设备面板同一套:
          // 在线 --status-running,离线落回弱化色。
          const css =
            item.kind === "device"
              ? item.online
                ? "var(--status-running)"
                : "var(--muted-foreground)"
              : (tokenToCssColor(item.color) ?? "var(--muted-foreground)");
          return (
            // device 的 refId 恒为 0,单靠 kind+refId 会让多台设备撞成同一个 key。
            <React.Fragment key={`${item.kind}-${item.refId}-${item.fp ?? ""}`}>
              {showHeader ? (
                <div className="sticky top-0 z-10 bg-popover px-2 pt-1.5 pb-0.5 text-3xs font-medium uppercase tracking-wide text-muted-foreground">
                  {groupLabel(item.kind, t)}
                </div>
              ) : null}
              <button
                type="button"
                role="option"
                ref={active ? activeRef : undefined}
                aria-selected={active}
                // mousemove 而非 mouseenter —— 键盘翻页让列表在静止的鼠标下滚动时
                // 只有 mouseenter 会触发,那会把选中态抢回鼠标所在行。
                // 指针一动就落到 selectedIndex 上,所以行内不再单独挂 hover 底色:
                // 高亮只有 selectedIndex 一个来源,不会出现键鼠两个高亮态打架。
                onMouseMove={() => onHover(idx)}
                onMouseDown={(e) => {
                  e.preventDefault();
                  onPick(item);
                }}
                style={
                  item.kind === "project"
                    ? { paddingLeft: 8 + (item.depth ?? 0) * 12 }
                    : undefined
                }
                className={cn(
                  // scroll-mt 抵掉 sticky 分组标题的高度,免得滚上来的高亮项藏在标题后面。
                  "flex w-full scroll-mt-6 cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs",
                  active
                    ? "bg-accent text-accent-foreground"
                    : "text-foreground",
                )}
              >
                <span
                  aria-hidden="true"
                  className="size-2 shrink-0 rounded-full"
                  style={{ backgroundColor: css }}
                />
                <span className="min-w-0 flex-1 truncate font-medium">
                  {item.label}
                </span>
                {item.kind === "project" && item.path ? (
                  <span className="ml-auto max-w-[40%] shrink-0 truncate text-muted-foreground">
                    {item.path}
                  </span>
                ) : null}
                {/* 只有离线才出声:在线是常态,一枚绿点已经说完了。反过来,离线单靠
                    灰点在色觉障碍下读不出来,所以那一侧补一句文字。 */}
                {item.kind === "device" && !item.online ? (
                  <span className="ml-auto shrink-0 text-muted-foreground">
                    {t("mentions.device.offline")}
                  </span>
                ) : null}
              </button>
            </React.Fragment>
          );
        })
      }
    </SuggestionPopover>
  );
}
