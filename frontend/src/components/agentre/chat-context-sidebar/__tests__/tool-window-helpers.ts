import { screen, within } from "@testing-library/react";

import {
  DEFAULT_TOOL_WINDOW_SLOTS,
  selectIsToolWindowShown,
  useToolWindowLayout,
  type ToolWindowId,
} from "@/stores/tool-window-layout-store";

/** 布局回到默认槽位，只显示给出的工具窗。 */
export function showOnly(...ids: ToolWindowId[]) {
  useToolWindowLayout.setState({
    slotOf: { ...DEFAULT_TOOL_WINDOW_SLOTS },
    shown: {
      rightTop: null,
      rightBottom: null,
      bottomLeft: null,
      bottomRight: null,
    },
    focusRequest: null,
  });
  for (const id of ids) useToolWindowLayout.getState().show(id);
}

export function isToolWindowShown(id: ToolWindowId): boolean {
  return selectIsToolWindowShown(id)(useToolWindowLayout.getState());
}

/** 图标条上的某个工具窗图标（英文 locale）。 */
export function stripButton(name: string): HTMLElement {
  return within(
    screen.getByRole("toolbar", { name: "Tool windows" }),
  ).getByRole("button", { name });
}

/** 「变更」工具窗的标题（名称 + 当前档的数量）。 */
export function changesHeading(): HTMLElement {
  return within(screen.getByRole("region", { name: "Changes" })).getAllByRole(
    "heading",
  )[0];
}
