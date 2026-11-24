import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  Button,
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
  cn,
} from "@agentre-hub/agentre-ui";
import {
  TOOL_WINDOW_IDS,
  useToolWindowLayout,
  type SlotId,
} from "@/stores/tool-window-layout-store";

import type { ToolWindowDefinition } from "./types";

type Props = {
  windows: ToolWindowDefinition[];
};

/**
 * 对话页最右边常驻的竖向图标条（spec「图标条」）：按槽位分组——右上一组、右下
 * 一组放在上端，底部两组放在下端。点击图标显示 / 隐藏对应的工具窗；同一组里的
 * 顺序固定为工具窗的注册顺序（TOOL_WINDOW_IDS），不随点击跳动。
 */
function ToolWindowStripImpl({ windows }: Props) {
  const { t } = useTranslation();
  const slotOf = useToolWindowLayout((s) => s.slotOf);

  const groupOf = (slot: SlotId) =>
    TOOL_WINDOW_IDS.filter((id) => slotOf[id] === slot)
      .map((id) => windows.find((w) => w.id === id))
      .filter((w): w is ToolWindowDefinition => w !== undefined);

  const top = [
    { slot: "rightTop" as const, label: t("toolWindows.slots.rightTop") },
    { slot: "rightBottom" as const, label: t("toolWindows.slots.rightBottom") },
  ]
    .map((g) => ({ ...g, items: groupOf(g.slot) }))
    .filter((g) => g.items.length > 0);
  const bottom = [
    { slot: "bottomLeft" as const, label: t("toolWindows.slots.bottomLeft") },
    { slot: "bottomRight" as const, label: t("toolWindows.slots.bottomRight") },
  ]
    .map((g) => ({ ...g, items: groupOf(g.slot) }))
    .filter((g) => g.items.length > 0);

  // 自带 Provider：图标条不依赖宿主在外面包一层才能出悬停提示。
  return (
    <TooltipProvider delayDuration={300}>
      <div
        role="toolbar"
        aria-orientation="vertical"
        aria-label={t("toolWindows.strip")}
        data-testid="tool-window-strip"
        onKeyDown={handleStripKeyDown}
        className="flex w-[38px] shrink-0 flex-col items-center gap-1 pt-1 pb-2"
      >
        {top.map((g, i) => (
          <React.Fragment key={g.slot}>
            {i > 0 ? <GroupGap /> : null}
            <StripGroup label={g.label} items={g.items} />
          </React.Fragment>
        ))}
        <div className="flex-1" />
        {bottom.map((g, i) => (
          <React.Fragment key={g.slot}>
            {i > 0 ? <GroupGap /> : null}
            <StripGroup label={g.label} items={g.items} />
          </React.Fragment>
        ))}
      </div>
    </TooltipProvider>
  );
}

export const ToolWindowStrip = React.memo(ToolWindowStripImpl);

/** 组与组之间的分隔：一小段空白加一枚短点，不画贯穿的结构线（design.md §3.4）。 */
function GroupGap() {
  return (
    <span
      aria-hidden="true"
      className="my-1 h-px w-4 shrink-0 rounded-full bg-muted-foreground/30"
    />
  );
}

function StripGroup({
  label,
  items,
}: {
  label: string;
  items: ToolWindowDefinition[];
}) {
  return (
    <div
      role="group"
      aria-label={label}
      className="flex flex-col items-center gap-1"
    >
      {items.map((w) => (
        <StripButton key={w.id} window={w} />
      ))}
    </div>
  );
}

function StripButton({ window: w }: { window: ToolWindowDefinition }) {
  const shown = useToolWindowLayout((s) => s.shown[s.slotOf[w.id]] === w.id);
  const toggle = useToolWindowLayout((s) => s.toggle);
  const badgeId = React.useId();
  const Icon = w.icon;
  const badge = w.count != null && w.count > 0 ? w.count : null;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={w.label}
          aria-pressed={shown}
          aria-describedby={badge !== null ? badgeId : undefined}
          data-tool-window-button={w.id}
          onClick={() => toggle(w.id)}
          className={cn(
            "relative size-[30px] rounded-[7px] text-sidebar-icon",
            shown
              ? "bg-button-fill text-sidebar-icon-active hover:bg-button-fill-hover hover:text-sidebar-icon-active"
              : "hover:bg-rail-accent",
          )}
        >
          <Icon className="size-4" aria-hidden="true" />
          {badge !== null ? (
            <span
              id={badgeId}
              data-testid="tool-window-badge"
              className="absolute -top-px -right-[3px] h-3.5 min-w-3.5 rounded-full bg-primary px-[3px] text-center text-3xs/3.5 font-semibold text-primary-foreground tabular-nums"
            >
              {badge}
            </span>
          ) : null}
        </Button>
      </TooltipTrigger>
      <TooltipContent side="left">{w.label}</TooltipContent>
    </Tooltip>
  );
}

/** 工具栏内 ↑/↓ 在图标之间移动焦点（Home / End 到两端）。 */
function handleStripKeyDown(event: React.KeyboardEvent<HTMLElement>) {
  const buttons = Array.from(
    event.currentTarget.querySelectorAll<HTMLElement>(
      "[data-tool-window-button]",
    ),
  );
  if (buttons.length === 0) return;
  const current = buttons.indexOf(document.activeElement as HTMLElement);
  let next: number;
  switch (event.key) {
    case "ArrowDown":
      next = current < 0 ? 0 : (current + 1) % buttons.length;
      break;
    case "ArrowUp":
      next =
        current < 0
          ? buttons.length - 1
          : (current - 1 + buttons.length) % buttons.length;
      break;
    case "Home":
      next = 0;
      break;
    case "End":
      next = buttons.length - 1;
      break;
    default:
      return;
  }
  event.preventDefault();
  buttons[next].focus();
}
