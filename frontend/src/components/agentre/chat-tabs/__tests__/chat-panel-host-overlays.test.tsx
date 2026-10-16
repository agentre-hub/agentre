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

// 共享包 HoverCardContent 关闭时带 animate-out 退场动画，Radix Presence 会把正在
// 关闭的预览卡留在页面上直到动画结束。jsdom 不加载 tw-animate-css，这里按同样的
// data-state 补上退场动画名，让关闭中的预览卡像真机一样多停留一会儿。
let exitAnimation: HTMLStyleElement;
beforeEach(() => {
  exitAnimation = document.createElement("style");
  exitAnimation.textContent =
    '[data-testid="background-tasks-peek"][data-state="closed"] { animation-name: exit; }';
  document.head.appendChild(exitAnimation);
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
  useChatAgentsStore.getState().__reset();
  useSessionIndexStore.getState().__reset();
});
afterEach(() => {
  exitAnimation.remove();
});

const openPeek = () =>
  document.querySelector(
    '[data-testid="background-tasks-peek"][data-state="open"]',
  );

// 预览卡悬停打开要等 openDelay（150ms）；多等一截确认迟到的「打开」也没有发生。
const pastOpenDelay = () =>
  act(() => new Promise((resolve) => setTimeout(resolve, 300)));

const host = (shown: boolean) => (
  <MemoryRouter initialEntries={["/chat"]}>
    <ChatPanelHost shown={shown} />
  </MemoryRouter>
);

describe("ChatPanelHost — background-task overlays when the panel is hidden", () => {
  // 真机 V12：点开弹层 → Esc（焦点回按钮，预览卡出现）→ 点图标栏 Issues。App 把对话区
  // display:none，按钮锚点没了，正在退场的预览卡被挪到窗口左上角，正好压在指针停着的
  // 图标栏上；指针「进入」它又让 HoverCard 排了一次延迟打开，预览卡就留在那里。
  it("Given the peek is open after closing the popover with Esc, When the host hides the chat area and the pointer lands on the closing peek, Then the peek does not open again", async () => {
    useChatTabsStore.getState().openSessionInNewTab(1);
    const { rerender } = render(host(true));
    const chip = screen.getByRole("button", { name: /background tasks/i });

    await userEvent.click(chip);
    await userEvent.keyboard("{Escape}");
    await pastOpenDelay();
    expect(openPeek()).not.toBeNull();

    rerender(host(false));
    const closing = screen.getByTestId("background-tasks-peek");
    expect(closing).toHaveAttribute("data-state", "closed");

    await userEvent.hover(closing);
    await pastOpenDelay();
    expect(openPeek()).toBeNull();

    // 回到对话页时也不能自己冒出来：那次「打开」根本不该被记下。
    rerender(host(true));
    expect(openPeek()).toBeNull();
  });

  // 弹层开着时 ⌘ 数字键切到另一个 Tab（快捷键走的就是 setActive）：弹层收起；弹层
  // 关闭后焦点回到按钮排下的延迟打开，也不能在已被藏起的 Tab 上开出预览卡。
  it("Given the popover is open in one tab, When another tab becomes active, Then that popover closes and its peek does not open over the other tab", async () => {
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
    await pastOpenDelay();

    expect(screen.queryByRole("region", { name: "Commands" })).toBeNull();
    expect(openPeek()).toBeNull();
  });
});
