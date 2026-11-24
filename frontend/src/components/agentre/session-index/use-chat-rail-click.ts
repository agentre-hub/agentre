import * as React from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { useToolWindowLayout } from "@/stores/tool-window-layout-store";

/**
 * 左侧图标栏「对话」按钮的点击：不在对话页时回到对话页；已经在对话页时收起 /
 * 展开会话列表（spec「快捷键」：与 ⌘0 效果相同）。
 */
export function useChatRailClick(): () => void {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const toggleSessionList = useToolWindowLayout((s) => s.toggleSessionList);
  return React.useCallback(() => {
    if (pathname === "/chat") toggleSessionList();
    else navigate("/chat");
  }, [navigate, pathname, toggleSessionList]);
}
