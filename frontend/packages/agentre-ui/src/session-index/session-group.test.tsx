import { fireEvent, render, screen, within } from "@testing-library/react";
import * as React from "react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { PopoverContent } from "../ui/popover";
import { SessionGroup } from "./session-group";
import type { SessionRowModel } from "./types";

function unreadSession(id: number): SessionRowModel {
  return {
    id: String(id),
    status: "waiting",
    title: `unread-${id}`,
    trailingLabel: "未读",
    attentionRank: "unread",
  };
}

function needsAttentionSession(id: number): SessionRowModel {
  return {
    id: String(id),
    status: "waiting",
    title: `approve-${id}`,
    trailingLabel: "审批",
    attentionRank: "needs_attention",
  };
}

function selectedSession(id: number): SessionRowModel {
  return {
    id: String(id),
    status: "idle",
    title: `selected-${id}`,
    trailingLabel: "1m ago",
    attentionRank: "selected",
  };
}

function ordinarySession(id: number): SessionRowModel {
  return {
    id: String(id),
    status: "idle",
    title: `idle-${id}`,
    trailingLabel: "5m ago",
  };
}

function queryBubble(container: HTMLElement): HTMLElement | null {
  return container.querySelector('[data-slot="agent-attention-bubble"]');
}

describe("SessionGroup attention bubble in expanded state", () => {
  it("keeps unread sessions in the attention bubble when expanded (so they can pick up ⌘N chip)", () => {
    const unread = unreadSession(1);
    const idle = ordinarySession(2);

    const { container } = render(
      <SessionGroup
        defaultExpanded
        sessions={[unread, idle]}
        attentionSessions={[unread]}
        renderHeader={() => <div data-testid="header" />}
      />,
    );

    const bubble = queryBubble(container);
    expect(bubble).not.toBeNull();
    expect(within(bubble!).getByText("unread-1")).toBeTruthy();
    // 下方常规列表通过 attentionIds 去重，所以未读不会同时出现在两处。
    expect(within(bubble!).queryByText("idle-2")).toBeNull();
    // idle 仍然在下方常规列表里出现。
    expect(screen.getByText("idle-2")).toBeTruthy();
  });

  it("still filters selected sessions out of the bubble when expanded (selected stays in the regular list at its natural position)", () => {
    const selected = selectedSession(7);
    const idle = ordinarySession(8);

    const { container } = render(
      <SessionGroup
        defaultExpanded
        sessions={[idle, selected]}
        attentionSessions={[selected]}
        renderHeader={() => <div data-testid="header" />}
      />,
    );

    // 没有任何 BubbleRank 留在 bubble 里 → bubble 元素本身不渲染。
    expect(queryBubble(container)).toBeNull();
    // selected + ordinary 都出现在下方常规列表。
    expect(screen.getByText("selected-7")).toBeTruthy();
    expect(screen.getByText("idle-8")).toBeTruthy();
  });

  it("keeps all BubbleRank entries (including unread + selected) in the bubble when collapsed", () => {
    const unread = unreadSession(1);
    const selected = selectedSession(2);
    const needs = needsAttentionSession(3);

    const { container } = render(
      <SessionGroup
        defaultExpanded={false}
        sessions={[unread, selected, needs]}
        attentionSessions={[needs, unread, selected]}
        renderHeader={() => <div data-testid="header" />}
      />,
    );

    const bubble = queryBubble(container);
    expect(bubble).not.toBeNull();
    expect(within(bubble!).getByText("unread-1")).toBeTruthy();
    expect(within(bubble!).getByText("selected-2")).toBeTruthy();
    expect(within(bubble!).getByText("approve-3")).toBeTruthy();
  });

  it("Given a collapsed group has collapsed-only attention, Then it renders that bubble without affecting expanded state", () => {
    const own = unreadSession(1);
    const child = needsAttentionSession(2);

    const { container } = render(
      <SessionGroup
        defaultExpanded={false}
        sessions={[own]}
        attentionSessions={[own]}
        collapsedAttentionSessions={[own, child]}
        renderHeader={() => <div data-testid="header" />}
      />,
    );

    const bubble = queryBubble(container);
    expect(bubble).not.toBeNull();
    expect(within(bubble!).getByText("unread-1")).toBeTruthy();
    expect(within(bubble!).getByText("approve-2")).toBeTruthy();
  });
});

describe("SessionGroup session row context menu", () => {
  it("renders a right-click context menu on a session row and fires the bound handlers", async () => {
    const onOpenInNewTab = vi.fn();
    const onRenameSession = vi.fn();
    const onDeleteSession = vi.fn();
    const idle = ordinarySession(2);
    const user = userEvent.setup({ pointerEventsCheck: 0 });

    render(
      <SessionGroup
        defaultExpanded
        sessions={[idle]}
        renderHeader={() => <div data-testid="header" />}
        onOpenInNewTab={onOpenInNewTab}
        onRenameSession={onRenameSession}
        onDeleteSession={onDeleteSession}
      />,
    );

    const row = screen.getByRole("button", { name: /idle-2/ });

    // 改名：右键 → 菜单 → 点「改名」→ handler 收到 (sessionId, title)。
    await user.pointer({ keys: "[MouseRight]", target: row });
    fireEvent.click(await screen.findByRole("menuitem", { name: "Rename" }));
    expect(onRenameSession).toHaveBeenCalledWith(2, "idle-2");

    // 新标签打开。
    await user.pointer({ keys: "[MouseRight]", target: row });
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "Open in new tab" }),
    );
    expect(onOpenInNewTab).toHaveBeenCalledWith(2);

    // 删除。
    await user.pointer({ keys: "[MouseRight]", target: row });
    fireEvent.click(await screen.findByRole("menuitem", { name: "Delete" }));
    expect(onDeleteSession).toHaveBeenCalledWith(2);
  });

  it("does not render a context menu when no handlers are provided (projects page keeps old behavior)", async () => {
    const idle = ordinarySession(2);
    const user = userEvent.setup({ pointerEventsCheck: 0 });

    render(
      <SessionGroup
        defaultExpanded
        sessions={[idle]}
        renderHeader={() => <div data-testid="header" />}
      />,
    );

    const row = screen.getByRole("button", { name: /idle-2/ });
    await user.pointer({ keys: "[MouseRight]", target: row });
    expect(screen.queryByRole("menuitem", { name: "Rename" })).toBeNull();
  });
});

describe("SessionGroup 「查看全部 N」溢出弹层", () => {
  function OverflowProbe({ onMount }: { onMount: () => void }) {
    // 弹层内容（桌面端是 SessionsPopover）在挂载时拉取会话列表：
    // 挂载次数 = 拉取次数。
    React.useEffect(() => {
      onMount();
    }, [onMount]);
    return <PopoverContent>overflow</PopoverContent>;
  }

  function renderGroup(onMount: () => void) {
    return render(
      <SessionGroup
        defaultExpanded
        sessions={[ordinarySession(1)]}
        totalSessions={12}
        renderSessionsPopover={() => <OverflowProbe onMount={onMount} />}
        renderHeader={({ toggle }) => (
          <button type="button" onClick={toggle}>
            header
          </button>
        )}
      />,
    );
  }

  function viewAllTrigger(): HTMLElement {
    return screen.getByRole("button", { name: /View all|查看全部/ });
  }

  it("Given 一个带「查看全部 N」的组, When 还没点开溢出入口, Then 弹层内容不该挂载（挂载即拉取，会把列表钉死在组渲染的那一刻）", () => {
    const mounted = vi.fn();
    renderGroup(mounted);

    expect(mounted).not.toHaveBeenCalled();
  });

  it("Given 弹层已经开过一次, When 关掉再打开, Then 内容重新挂载（每次打开都拿最新的一页，而不是复用上次那份）", async () => {
    const user = userEvent.setup();
    const mounted = vi.fn();
    renderGroup(mounted);

    await user.click(viewAllTrigger());
    expect(mounted).toHaveBeenCalledTimes(1);

    await user.keyboard("{Escape}");
    await user.click(viewAllTrigger());
    expect(mounted).toHaveBeenCalledTimes(2);
  });
});
