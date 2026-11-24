import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it } from "vitest";

import { useToolWindowLayout } from "@/stores/tool-window-layout-store";

import { SessionListSidebar } from "../session-list-sidebar";
import { useChatRailClick } from "../use-chat-rail-click";

function RailHarness() {
  const onClick = useChatRailClick();
  const location = useLocation();
  return (
    <>
      <button type="button" onClick={onClick}>
        Chat
      </button>
      <span data-testid="path">{location.pathname}</span>
    </>
  );
}

describe("会话列表收起 / 展开", () => {
  beforeEach(() => {
    useToolWindowLayout.setState({ sessionListCollapsed: false });
  });

  it("Given 会话列表展开, Then 它显示；收起后不显示但不卸载（搜索词等状态保留）", () => {
    render(
      <SessionListSidebar persistenceKey="chat" ariaLabel="Session index">
        <input aria-label="Search" defaultValue="hello" />
      </SessionListSidebar>,
    );
    const sidebar = screen.getByRole("complementary", {
      name: "Session index",
    });
    const input = screen.getByRole("textbox", { name: "Search" });
    expect(sidebar).not.toHaveClass("hidden");

    act(() => useToolWindowLayout.getState().toggleSessionList());
    expect(sidebar).toHaveClass("hidden");
    expect(input).toBeInTheDocument();
    expect(input).toHaveValue("hello");

    act(() => useToolWindowLayout.getState().toggleSessionList());
    expect(sidebar).not.toHaveClass("hidden");
    expect(screen.getByRole("textbox", { name: "Search" })).toBe(input);
  });

  it("Given 已在对话页, When 再次点击左侧「对话」图标, Then 会话列表收起；再点一次展开", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/chat"]}>
        <RailHarness />
      </MemoryRouter>,
    );

    await user.click(screen.getByRole("button", { name: "Chat" }));
    expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(true);
    await user.click(screen.getByRole("button", { name: "Chat" }));
    expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(false);
  });

  it("Given 在别的页面, When 点击「对话」图标, Then 只是回到对话页，不改收起状态", async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter initialEntries={["/issues"]}>
        <RailHarness />
      </MemoryRouter>,
    );

    await user.click(screen.getByRole("button", { name: "Chat" }));
    expect(screen.getByTestId("path")).toHaveTextContent("/chat");
    expect(useToolWindowLayout.getState().sessionListCollapsed).toBe(false);
  });
});
