import * as React from "react";
import { useTranslation } from "react-i18next";

import { ResizableSidebar } from "@agentre-hub/agentre-ui";
import {
  useToolWindowLayout,
  type SlotId,
} from "@/stores/tool-window-layout-store";

import { ToolWindowFrame } from "./tool-window-frame";
import { ToolWindowStrip } from "./tool-window-strip";
import type { ToolWindowDefinition } from "./types";

type Props = {
  /** 已注册的工具窗；没注册的工具窗不出图标，它所在的槽位按空槽处理。 */
  windows: ToolWindowDefinition[];
  /**
   * 新建会话提示页（尚未发出首条消息）不显示图标条与工具窗（spec「新建会话
   * 提示页」）。隐藏时编辑区仍渲染在同一个位置，里面的输入框不会因此重新挂载。
   */
  hidden?: boolean;
  /** 编辑区：对话内容页（以及暂时还在它右边的文件预览列）。 */
  children: React.ReactNode;
};

/**
 * 对话页的工具窗骨架（spec「工具窗布局」）：编辑区在左，下方是底部左 / 底部右
 * 两个槽位；右侧是右上 / 右下两个槽位，最右边是常驻图标条。每个槽位同一时刻
 * 最多显示一个工具窗；同侧两个槽位都有工具窗时分屏（右侧上下分、底部左右分）。
 *
 * 编辑区永远是这棵树里的同一个位置——槽位与图标条只作为它的兄弟出现或消失。
 */
export function ToolWindowLayout({ windows, hidden = false, children }: Props) {
  return (
    <div className="flex min-h-0 min-w-0 flex-1">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        <div className="flex min-h-0 min-w-0 flex-1">{children}</div>
        {hidden ? null : <BottomSlots windows={windows} />}
      </div>
      {hidden ? null : <RightSlots windows={windows} />}
      {hidden ? null : <ToolWindowStrip windows={windows} />}
    </div>
  );
}

/** 某个槽位此刻要渲染的工具窗；显示的是没注册的工具窗时按空槽处理。 */
function useSlotWindow(
  windows: ToolWindowDefinition[],
  slot: SlotId,
): ToolWindowDefinition | null {
  const id = useToolWindowLayout((s) => s.shown[slot]);
  if (id === null) return null;
  return windows.find((w) => w.id === id) ?? null;
}

const RightSlots = React.memo(function RightSlots({
  windows,
}: {
  windows: ToolWindowDefinition[];
}) {
  const { t } = useTranslation();
  const top = useSlotWindow(windows, "rightTop");
  const bottom = useSlotWindow(windows, "rightBottom");
  if (top === null && bottom === null) return null;
  return (
    <ResizableSidebar
      // 沿用旧右侧栏的宽度记忆：升级后右侧区域还是用户拖出来的那个宽度。
      persistenceKey="chat-context"
      ariaLabel={t("toolWindows.rightArea")}
      edge="left"
      defaultWidth={240}
      surface="chrome"
      className="h-full"
    >
      <div className="flex min-h-0 flex-1 flex-col gap-2 pb-2">
        {top !== null ? <ToolWindowFrame key={top.id} window={top} /> : null}
        {bottom !== null ? (
          <ToolWindowFrame key={bottom.id} window={bottom} />
        ) : null}
      </div>
    </ResizableSidebar>
  );
});

const BottomSlots = React.memo(function BottomSlots({
  windows,
}: {
  windows: ToolWindowDefinition[];
}) {
  const { t } = useTranslation();
  const left = useSlotWindow(windows, "bottomLeft");
  const right = useSlotWindow(windows, "bottomRight");
  if (left === null && right === null) return null;
  return (
    <aside
      aria-label={t("toolWindows.bottomArea")}
      className="flex h-60 shrink-0 gap-2 pr-2 pb-2"
    >
      {left !== null ? <ToolWindowFrame key={left.id} window={left} /> : null}
      {right !== null ? (
        <ToolWindowFrame key={right.id} window={right} />
      ) : null}
    </aside>
  );
});
