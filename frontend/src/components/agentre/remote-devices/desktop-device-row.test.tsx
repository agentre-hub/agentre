import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";

vi.mock("../../../../wailsjs/go/app/App", () => ({
  PeerListSessions: vi.fn(),
}));

import { PeerListSessions } from "../../../../wailsjs/go/app/App";
import { DESKTOP_SESSIONS_PAGE_SIZE } from "./desktop-device-row";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import type { server_svc } from "../../../../wailsjs/go/models";
import { DesktopDeviceRow } from "./desktop-device-row";

// conv 是这些用例里第 n 条对话的身份（uuid 字符串）——设备页列出的会话按它寻址。
const conv = (n: number) =>
  `0198f4c1-a000-7c0d-8b21-${String(n).padStart(12, "0")}`;

const mockList = PeerListSessions as unknown as ReturnType<typeof vi.fn>;

const runningDesktop = (
  over: Partial<server_svc.Device> = {},
): server_svc.Device => ({
  id: 2,
  name: "MacBook Pro",
  kind: "desktop",
  platform: "darwin",
  version: "v0.1.0",
  fingerprint: "sha256:desktop-b",
  status: 1,
  online: true,
  lastSeenAt: 1_700_000_000_000,
  isThisDevice: false,
  ...over,
});

beforeEach(() => {
  mockList.mockReset();
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
});

describe("DesktopDeviceRow", () => {
  // 展开一台远端桌面端此前把它的**整张**会话表要回来：几千条对话就是几千份摘要过
  // Wails 桥，展开那一下等在解码上。按页要，其余的滚到底再续。
  it("given more sessions than fit one page, loads the next page on demand", async () => {
    mockList
      .mockResolvedValueOnce({
        sessions: [
          {
            conversationId: conv(1),
            title: "first page",
            lifecycleState: "idle",
          },
        ],
        cursor: "20",
        hasMore: true,
        total: 44,
      })
      .mockResolvedValueOnce({
        sessions: [
          {
            conversationId: conv(2),
            title: "second page",
            lifecycleState: "idle",
          },
        ],
        hasMore: false,
      });
    render(
      <MemoryRouter>
        <DesktopDeviceRow device={runningDesktop()} now={1_700_000_100_000} />
      </MemoryRouter>,
    );

    await userEvent.click(screen.getByRole("button", { name: /expand/i }));
    await waitFor(() => expect(screen.getByText("first page")).toBeTruthy());
    expect(screen.queryByText("second page")).toBeNull();

    await userEvent.click(screen.getByTestId("peer-sessions-load-more"));

    await waitFor(() => expect(screen.getByText("second page")).toBeTruthy());
    expect(mockList).toHaveBeenLastCalledWith({
      fingerprint: "sha256:desktop-b",
      cursor: "20",
      limit: DESKTOP_SESSIONS_PAGE_SIZE,
    });
    // 翻完了就不再摆入口。
    expect(screen.queryByTestId("peer-sessions-load-more")).toBeNull();
  });

  it("given a running desktop, expands into its session list and opens a Peer Tab on click", async () => {
    mockList.mockResolvedValue({
      sessions: [
        {
          conversationId: conv(7),
          title: "Ship the release",
          lifecycleState: "running",
          waitingForInput: true,
        },
        {
          conversationId: conv(8),
          title: "",
          lifecycleState: "idle",
          waitingForInput: false,
        },
      ],
    });
    render(
      <MemoryRouter>
        <DesktopDeviceRow device={runningDesktop()} now={1_700_000_100_000} />
      </MemoryRouter>,
    );

    await userEvent.click(screen.getByRole("button", { name: /expand/i }));
    await waitFor(() => {
      expect(screen.getByText("Ship the release")).toBeTruthy();
    });
    expect(mockList).toHaveBeenCalledWith({
      fingerprint: "sha256:desktop-b",
      cursor: "",
      limit: DESKTOP_SESSIONS_PAGE_SIZE,
    });

    await userEvent.click(screen.getByText("Ship the release"));
    const tabs = useChatTabsStore.getState().tabs;
    expect(tabs).toHaveLength(1);
    expect(tabs[0].meta).toMatchObject({
      kind: "peer",
      fingerprint: "sha256:desktop-b",
      conversationId: conv(7),
    });
  });

  // 跑挂的那一轮在这张表上必须看得出来。`failed` 是 wire 上新开的一档
  // （见 remote/wire 的生命周期常量）：桌面端的 AgentStatus="error" 此前编码成
  // `interrupted`，那个值是自锁终态、消费方对它一律不 attach。改成 `failed` 之后，
  // 这里若不认它就会落进 default 分支被冒充成「Idle」——失败静默消失，正是这一轮
  // 要修的那件事本身。
  it("given a session whose last turn failed, shows a failed badge instead of Idle", async () => {
    mockList.mockResolvedValue({
      sessions: [
        {
          conversationId: conv(9),
          title: "Investigate timeout",
          lifecycleState: "failed",
          waitingForInput: false,
        },
      ],
    });
    render(
      <MemoryRouter>
        <DesktopDeviceRow device={runningDesktop()} now={1_700_000_100_000} />
      </MemoryRouter>,
    );

    await userEvent.click(screen.getByRole("button", { name: /expand/i }));
    await waitFor(() => {
      expect(screen.getByText("Investigate timeout")).toBeTruthy();
    });
    expect(screen.getByText("Failed")).toBeTruthy();
    expect(screen.queryByText("Idle")).toBeNull();
  });

  /**
   * 红只留给 `failed`。`interrupted` 说的是「这条会话此刻接不回实时流」，而 daemon
   * 每次启动都按 R10 把全部非终态会话整批标成它——那是重启后的**常态**，不是任何一
   * 次故障。判定住在共享包（`lifecycleToAgentStatus`），本行只是把它画出来；这条钉
   * 的是画法，控制台那边另有一条钉同一个判定。
   */
  it("given an interrupted session, names it but does not paint it as an error", async () => {
    mockList.mockResolvedValue({
      sessions: [
        {
          conversationId: conv(11),
          title: "Refactor the parser",
          lifecycleState: "interrupted",
          waitingForInput: false,
        },
      ],
    });
    render(
      <MemoryRouter>
        <DesktopDeviceRow device={runningDesktop()} now={1_700_000_100_000} />
      </MemoryRouter>,
    );

    await userEvent.click(screen.getByRole("button", { name: /expand/i }));
    const badge = await screen.findByText("Interrupted");
    // 状态说得出来（不冒充成「Idle」），但不带错误色。
    expect(badge.className).not.toContain("status-error");
  });

  it("given a desktop whose Agentre is not running, shows the App-not-running wording and is not expandable", async () => {
    render(
      <MemoryRouter>
        <DesktopDeviceRow
          device={runningDesktop({ online: false })}
          now={1_700_000_100_000}
        />
      </MemoryRouter>,
    );
    expect(screen.getByTestId("desktop-not-running").textContent).toContain(
      "not running",
    );
    // 未运行不可展开：无展开按钮
    expect(screen.queryByRole("button", { name: /expand/i })).toBeNull();
    // 不会发起 PeerListSessions
    expect(mockList).not.toHaveBeenCalled();
  });

  it("single-desktop account: the self desktop row renders as this device (R22 one-device regression)", () => {
    render(
      <MemoryRouter>
        <DesktopDeviceRow
          device={runningDesktop({ isThisDevice: true, online: true })}
          now={1_700_000_100_000}
        />
      </MemoryRouter>,
    );
    expect(screen.getByText("This device")).toBeTruthy();
    // 本机也是正在运行的桌面端，可展开列出会话（但绝不把本机当 peer 派活——那是
    // 聊天侧 effectiveTarget.kind==="local" 保证的）。
    expect(screen.getByRole("button", { name: /expand/i })).toBeTruthy();
  });
});
