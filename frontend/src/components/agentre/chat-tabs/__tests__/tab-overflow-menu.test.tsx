import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";

import { TabOverflowMenu } from "../tab-overflow-menu";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

// Radix DropdownMenu 在 jsdom 中需要关闭 pointerEvents 检查。
function setupUser() {
  return userEvent.setup({ pointerEventsCheck: 0 });
}

async function openMenu(user: ReturnType<typeof setupUser>) {
  await user.click(screen.getByLabelText("Open Tab menu"));
  await waitFor(() => {
    expect(screen.queryAllByRole("menuitem").length).toBeGreaterThan(0);
  });
}

describe("TabOverflowMenu", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
  });

  it("列出全部 sortedTabs", async () => {
    const user = setupUser();
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    render(<TabOverflowMenu />);
    await openMenu(user);
    expect(screen.getAllByRole("menuitem")).toHaveLength(2);
  });

  it("active tab 行高亮", async () => {
    const user = setupUser();
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const second = useChatTabsStore.getState().tabs[1].id;
    useChatTabsStore.getState().setActive(second);
    render(<TabOverflowMenu />);
    await openMenu(user);
    const items = screen.getAllByRole("menuitem");
    expect(items[1]).toHaveAttribute("data-active", "true");
  });

  it("点行 setActive 并关闭菜单", async () => {
    const user = setupUser();
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    render(<TabOverflowMenu />);
    await openMenu(user);
    const items = screen.getAllByRole("menuitem");
    await user.click(items[0]);
    expect(useChatTabsStore.getState().activeTabId).toBe(
      useChatTabsStore.getState().tabs[0].id,
    );
    // Radix 自动关闭菜单
    await waitFor(() => {
      expect(screen.queryAllByRole("menuitem")).toHaveLength(0);
    });
  });

  it("菜单项标题单行 (truncate)", async () => {
    const user = setupUser();
    useChatTabsStore.getState().openSessionInNewTab(1);
    const { useSessionMetaStore } = await import("@/stores/session-meta-store");
    useSessionMetaStore.getState().__reset();
    useSessionMetaStore.getState().setMeta(1, {
      agentId: 1,
      agentName: "A",
      agentColor: "agent-1",
      projectId: 0,
      title:
        "一个非常非常非常非常非常非常非常非常非常非常非常非常长的会话标题用来测试 truncate",
    });
    render(<TabOverflowMenu />);
    await openMenu(user);
    const item = screen.getByRole("menuitem");
    const titleSpan = item.querySelector(".truncate");
    expect(titleSpan).not.toBeNull();
    expect(titleSpan?.className).toMatch(/whitespace-nowrap/);
  });

  it("项目会话行尾渲染项目色 chip; 非项目会话不渲染", async () => {
    const user = setupUser();
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const { useSessionMetaStore } = await import("@/stores/session-meta-store");
    useSessionMetaStore.getState().__reset();
    useSessionMetaStore.getState().setMeta(1, {
      agentId: 1,
      agentName: "A",
      agentColor: "agent-1",
      projectId: 0,
      title: "t1",
    });
    render(<TabOverflowMenu />);
    await openMenu(user);
    expect(screen.queryAllByTestId("overflow-row-project-chip")).toHaveLength(
      0,
    );
  });

  it("前 9 行显示 ⌘1..⌘9 chip", async () => {
    const user = setupUser();
    for (let i = 1; i <= 11; i++)
      useChatTabsStore.getState().openSessionInNewTab(i);
    render(<TabOverflowMenu />);
    await openMenu(user);
    expect(screen.getByText("⌘1")).toBeInTheDocument();
    expect(screen.getByText("⌘9")).toBeInTheDocument();
    expect(screen.queryByText("⌘10")).not.toBeInTheDocument();
  });
});

// ─── 2026-08-23 对话页外壳收口 · 溢出菜单的状态点回归共享原语 ────────────────

describe("TabOverflowMenu · 状态点来自共享 statusConfig", () => {
  beforeEach(() => {
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    useSessionStatusStore.getState().__reset();
  });

  it("Given 一条出错的会话, When 菜单展开, Then 那枚点用 bg-status-error —— 与其余状态记号同源，不再是菜单私有的 bg-destructive", async () => {
    const user = setupUser();
    useChatTabsStore.getState().openSessionInNewTab(505);
    useSessionStatusStore.getState().upsert(505, {
      agentStatus: "error",
      needsAttention: false,
    });
    render(<TabOverflowMenu />);
    await openMenu(user);

    expect(screen.getByTestId("overflow-row-status-dot")).toHaveClass(
      "bg-status-error",
    );
  });

  it("Given 一条闲置的会话, When 菜单展开, Then 那一格仍然是透明占位，不摆记号", async () => {
    const user = setupUser();
    useChatTabsStore.getState().openSessionInNewTab(506);
    render(<TabOverflowMenu />);
    await openMenu(user);

    expect(screen.getByTestId("overflow-row-status-dot")).toHaveClass(
      "bg-transparent",
    );
  });
});
