import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  cn,
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@agentre-hub/agentre-ui";
import {
  selectRegionState,
  useToolWindowLayout,
} from "@/stores/tool-window-layout-store";
import type { ToolWindowRegion } from "@/stores/tool-window-layout-store";

import {
  SESSION_LIST_TOGGLE_ID,
  TOOL_WINDOW_REGION_TOGGLE_IDS,
} from "../shortcuts/registry";
import { useShortcutLabel } from "../shortcuts/use-shortcut-label";

type ToggleRegion = "sessionList" | ToolWindowRegion;

// 外框 18x18 + 分隔线；区域「显示中」时该区域矩形用 currentColor 填实。
const FILL: Record<ToggleRegion, React.ReactElement> = {
  sessionList: <rect x="3" y="3" width="6" height="18" rx="2" />,
  bottom: <rect x="3" y="15" width="18" height="6" rx="2" />,
  right: <rect x="15" y="3" width="6" height="18" rx="2" />,
};
const DIVIDER: Record<ToggleRegion, string> = {
  sessionList: "M9 3v18",
  bottom: "M3 15h18",
  right: "M15 3v18",
};

function RegionIcon({ region, on }: { region: ToggleRegion; on: boolean }) {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {on ? (
        <g data-filled="" fill="currentColor" stroke="none" opacity={0.9}>
          {FILL[region]}
        </g>
      ) : null}
      <rect width="18" height="18" x="3" y="3" rx="2" />
      <path d={DIVIDER[region]} />
    </svg>
  );
}

function LayoutToggle({
  region,
  shown,
  disabled = false,
  label,
  tooltip,
  shortcutId,
  onClick,
}: {
  region: ToggleRegion;
  shown: boolean;
  disabled?: boolean;
  label: string;
  tooltip?: string;
  shortcutId: string;
  onClick: () => void;
}) {
  const shortcut = useShortcutLabel(shortcutId);
  return (
    <Tooltip>
      {/* 禁用的 button 不触发悬停，提示挂在外包的 span 上。 */}
      <TooltipTrigger asChild>
        <span className="inline-flex">
          <button
            type="button"
            data-testid={`layout-toggle-${region}`}
            aria-label={label}
            aria-pressed={shown}
            disabled={disabled}
            onClick={onClick}
            className={cn(
              "wails-no-drag inline-flex size-7 cursor-pointer items-center justify-center rounded-md text-muted-foreground outline-none transition-colors hover:bg-rail-accent hover:text-accent-foreground focus-visible:focus-outline disabled:cursor-default disabled:opacity-40 disabled:hover:bg-transparent",
              shown && "text-foreground",
            )}
          >
            <RegionIcon region={region} on={shown} />
          </button>
        </span>
      </TooltipTrigger>
      <TooltipContent>
        {tooltip ?? label}
        {tooltip === undefined && shortcut ? (
          <kbd className="ml-1.5 font-mono opacity-70">{shortcut}</kbd>
        ) : null}
      </TooltipContent>
    </Tooltip>
  );
}

function RegionToggle({ region }: { region: ToolWindowRegion }) {
  const { t } = useTranslation();
  const state = useToolWindowLayout(selectRegionState(region));
  // 新建会话提示页不显示图标条与工具窗：屏幕上这个区域什么都没有，⌘J / ⌥⌘B 此时也
  // 不生效，开关跟着读为未显示、不可点。
  const stripShown = useToolWindowLayout((s) => s.stripMounts > 0);
  const shown = stripShown && state.shown;
  const available = stripShown && state.available;
  const toggleRegion = useToolWindowLayout((s) => s.toggleRegion);
  const name =
    region === "bottom"
      ? shown
        ? t("app.layoutToggles.bottom.hide")
        : t("app.layoutToggles.bottom.show")
      : shown
        ? t("app.layoutToggles.right.hide")
        : t("app.layoutToggles.right.show");
  const empty =
    region === "bottom"
      ? t("app.layoutToggles.bottom.empty")
      : t("app.layoutToggles.right.empty");
  return (
    <LayoutToggle
      region={region}
      shown={shown}
      disabled={!available}
      label={name}
      tooltip={available ? undefined : empty}
      shortcutId={TOOL_WINDOW_REGION_TOGGLE_IDS[region]}
      onClick={() => toggleRegion(region)}
    />
  );
}

/**
 * 标题栏右端的三枚布局开关（会话列表 / 底部 / 右侧工具窗）。点击等同对应快捷键；
 * 图标里填实的区域 = 当前显示中。不属于拖动区（wails-no-drag）。
 */
export function LayoutToggles() {
  const { t } = useTranslation();
  const collapsed = useToolWindowLayout((s) => s.sessionListCollapsed);
  const toggleSessionList = useToolWindowLayout((s) => s.toggleSessionList);
  return (
    <TooltipProvider delayDuration={300}>
      <div className="wails-no-drag flex shrink-0 items-center gap-0.5">
        <LayoutToggle
          region="sessionList"
          shown={!collapsed}
          label={
            collapsed
              ? t("app.layoutToggles.sessionList.expand")
              : t("app.layoutToggles.sessionList.collapse")
          }
          shortcutId={SESSION_LIST_TOGGLE_ID}
          onClick={toggleSessionList}
        />
        <RegionToggle region="bottom" />
        <RegionToggle region="right" />
      </div>
    </TooltipProvider>
  );
}
