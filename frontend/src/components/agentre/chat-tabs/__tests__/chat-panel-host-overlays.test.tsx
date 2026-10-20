import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";

import { ChatPanelHost } from "../chat-panel-host";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionIndexStore } from "@/stores/session-index-store";

// 面板换成只挂真实后台任务按钮的桩：可见性信号从真实宿主（ChatPanelHost →
// HostedPanel → PanelShownContext）一路传到真实按钮，中间不经手写的隐藏祖先。
vi.mock("../../chat-panel", async () => {
  const { BackgroundTasksChip } =
    await import("../../background-tasks/background-tasks-chip");
  return {
    ChatPanel: ({ sessionId }: { sessionId: number }) => (
      <div data-testid={`chat-panel-${sessionId}`}>
        <BackgroundTasksChip
          tasks={[
            {
              toolUseId: `tu-${sessionId}`,
              kind: "local_bash",
              description: `sleep ${sessionId}`,
              status: "running",
            },
          ]}
        />
      </div>
    ),
    pruneChatPanelScrollState: vi.fn(),
  };
});

// 共享包 PopoverContent 关闭时带 animate-out 退场动画，Radix Presence 会把正在
// 关闭的弹层留在页面上直到动画结束。jsdom 不加载 tw-animate-css，需要时
// (keepClosingPanel) 按同样的 data-state 补上退场动画名，让关闭中的弹层像真机
// 一样多停留一会儿。
let exitAnimation: HTMLStyleElement;
const keepClosingPanel = () => document.head.appendChild(exitAnimation);
beforeEach(() => {
  exitAnimation = document.createElement("style");
  exitAnimation.textContent =
    '[role="dialog"][data-state="closed"] { animation-name: exit; }';
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
  useChatAgentsStore.getState().__reset();
  useSessionIndexStore.getState().__reset();
});
afterEach(() => {
  exitAnimation.remove();
});

const openPanel = () =>
  document.querySelector('[role="dialog"][data-state="open"]');

// 悬停打开要等一小段延迟（150ms）；多等一截确认迟到的「打开」也没有发生。
const pastOpenDelay = () =>
  act(() => new Promise((resolve) => setTimeout(resolve, 300)));

const host = (shown: boolean) => (
  <MemoryRouter initialEntries={["/chat"]}>
    <ChatPanelHost shown={shown} />
  </MemoryRouter>
);

describe("ChatPanelHost — background-task overlays when the panel is hidden", () => {
  // 真机 V12：悬停开着弹层时点图标栏 Issues。App 把对话区 display:none，按钮锚点
  // 没了，正在退场的弹层被挪到窗口左上角，正好压在指针停着的图标栏上；指针「进入」
  // 它不能再排一次打开，否则弹层就留在别的页面上。
  it("Given the panel is hovered open, When the host hides the chat area and the pointer lands on the closing panel, Then it does not open again", async () => {
    keepClosingPanel();
    useChatTabsStore.getState().openSessionInNewTab(1);
    const { rerender } = render(host(true));
    const chip = screen.getByRole("button", { name: /background tasks/i });

    await userEvent.hover(chip);
    await pastOpenDelay();
    expect(openPanel()).not.toBeNull();

    rerender(host(false));
    const closing = document.querySelector(
      '[role="dialog"][data-state="closed"]',
    ) as HTMLElement;
    expect(closing).not.toBeNull();

    await userEvent.hover(closing);
    await pastOpenDelay();
    expect(openPanel()).toBeNull();

    // 回到对话页时也不能自己冒出来：那次「打开」根本不该被记下。
    rerender(host(true));
    await pastOpenDelay();
    expect(openPanel()).toBeNull();
  });

  // 弹层开着时 ⌘ 数字键切到另一个 Tab（快捷键走的就是 setActive）：焦点留在弹层里、
  // 没有外部点击，Radix 不会自己关，靠宿主告知面板已藏起来收掉它。
  it("Given the popover is open in one tab, When another tab becomes active, Then that popover closes", async () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    useChatTabsStore.getState().openSessionInNewTab(2);
    const [first, second] = useChatTabsStore.getState().tabs;
    useChatTabsStore.getState().setActive(second.id);
    render(host(true));
    const chip = screen
      .getByTestId("chat-panel-2")
      .querySelector("button") as HTMLButtonElement;

    await userEvent.click(chip);
    expect(screen.getByRole("region", { name: "Commands" })).toBeVisible();

    act(() => useChatTabsStore.getState().setActive(first.id));

    expect(screen.queryByRole("region", { name: "Commands" })).toBeNull();
  });
});
