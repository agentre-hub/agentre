import * as React from "react";
import { Minus } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@agentre-hub/agentre-ui";
import { useToolWindowLayout } from "@/stores/tool-window-layout-store";

import type { ToolWindowDefinition } from "./types";

type Props = {
  window: ToolWindowDefinition;
};

/**
 * 一个工具窗的外壳（spec「工具窗标题栏」）：带名称的区域，标题栏显示名称（带数量
 * 的工具窗跟着数字），「隐藏」平时不占眼，悬停或焦点落进来时出现；焦点在工具窗
 * 里时整圈描边。内容超出可视高度时在外壳内滚动。
 *
 * 被图标条点开时它自己把焦点拿进来（store 的 focusRequest），启动时恢复显示的
 * 工具窗不抢输入框的焦点。
 */
function ToolWindowFrameImpl({ window: w }: Props) {
  const { t } = useTranslation();
  const hide = useToolWindowLayout((s) => s.hide);
  const focusRequested = useToolWindowLayout((s) => s.focusRequest === w.id);
  const clearFocusRequest = useToolWindowLayout((s) => s.clearFocusRequest);
  const ref = React.useRef<HTMLElement>(null);

  React.useEffect(() => {
    if (!focusRequested) return;
    ref.current?.focus();
    clearFocusRequest();
  }, [focusRequested, clearFocusRequest]);

  return (
    <section
      ref={ref}
      aria-label={w.label}
      tabIndex={-1}
      data-tool-window={w.id}
      className="group/tool-window flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-[10px] bg-sheet outline-none focus-within:focus-outline-inset"
    >
      <div className="flex h-8 shrink-0 items-center gap-1 pr-1 pl-3">
        <h3 className="flex min-w-0 flex-1 items-baseline gap-1.5 text-xs font-semibold text-foreground">
          <span className="truncate">{w.label}</span>
          {w.count != null ? (
            <span className="shrink-0 font-mono text-3xs font-normal text-muted-foreground">
              {w.count}
            </span>
          ) : null}
        </h3>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={t("toolWindows.hide")}
          title={t("toolWindows.hide")}
          onClick={() => hide(w.id)}
          className="opacity-0 group-focus-within/tool-window:opacity-100 group-hover/tool-window:opacity-100 focus-visible:opacity-100"
        >
          <Minus aria-hidden="true" />
        </Button>
      </div>
      <div className="flex min-h-0 flex-1 flex-col overflow-auto">
        {w.content}
      </div>
    </section>
  );
}

export const ToolWindowFrame = React.memo(ToolWindowFrameImpl);
