import type * as React from "react";

import { ResizableSidebar } from "@agentre-hub/agentre-ui";
import { useToolWindowLayout } from "@/stores/tool-window-layout-store";

type Props = Omit<React.ComponentProps<typeof ResizableSidebar>, "className">;

/**
 * 对话页左侧的会话列表侧栏，能收起 / 展开（spec 决策 8：它是导航不是工具窗，
 * 只能收起）。收起时只是不显示、不卸载：搜索词、筛选、滚动位置都还在，展开就是
 * 原样。它仍是 outlet 的根，右边的标签页区紧挨着它。
 */
export function SessionListSidebar(props: Props) {
  const collapsed = useToolWindowLayout((s) => s.sessionListCollapsed);
  return (
    <ResizableSidebar {...props} className={collapsed ? "hidden" : undefined} />
  );
}
