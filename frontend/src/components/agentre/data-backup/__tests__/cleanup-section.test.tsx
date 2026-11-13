import "@testing-library/jest-dom/vitest";

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  PreviewChatCleanup: vi.fn(),
  ExecuteChatCleanup: vi.fn(),
  ProjectListTree: vi.fn(),
  ExportData: vi.fn(),
  PreviewImportData: vi.fn(),
  ApplyImportData: vi.fn(),
}));
vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);

const sonnerMocks = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const reloadMock = vi.hoisted(() => vi.fn());
vi.mock("@/stores/sidebar-reload", () => ({
  reloadSidebarSources: reloadMock,
}));

import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useProjectListStore } from "@/stores/project-list-store";

import { DataBackupPanel } from "../data-backup-panel";

const DAY = 24 * 60 * 60 * 1000;

function previewResp(over: Record<string, unknown> = {}) {
  return {
    count: 3,
    retained: 1,
    copies: [
      { kind: "local", name: "", online: false, lanDirect: false, count: 3 },
      { kind: "account", name: "", online: false, lanDirect: false, count: 3 },
      {
        kind: "machine",
        name: "build-box",
        online: true,
        lanDirect: false,
        count: 2,
      },
    ],
    sessionIds: [11, 12, 13],
    subagentSessionIds: [21, 22],
    ...over,
  };
}

function execResp(over: Record<string, unknown> = {}) {
  return {
    deleted: [11, 12, 13],
    failed: [],
    pending: [],
    retained: 0,
    ...over,
  };
}

async function openConfirm() {
  const button = await screen.findByRole("button", {
    name: /Clean up 3 conversations/,
  });
  await waitFor(() => expect(button).toBeEnabled());
  fireEvent.click(button);
  return screen.findByRole("dialog");
}

function openTab(sessionId: number) {
  useChatTabsStore.getState().openSessionInNewTab(sessionId);
}

describe("DataBackupPanel 对话清理区块", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useChatTabsStore.setState({ tabs: [], activeTabId: null });
    useChatAgentsStore.setState({
      agents: [{ id: 7, name: "Claude Code", sessionIds: [] }] as never,
    });
    appMocks.ProjectListTree.mockResolvedValue([
      { project: { id: 5, name: "agentre", path: "/p", color: "", icon: "" } },
    ]);
    useProjectListStore.getState().__reset();
    appMocks.PreviewChatCleanup.mockResolvedValue(previewResp());
    appMocks.ExecuteChatCleanup.mockResolvedValue(execResp());
  });

  it("清理区块排在导入区块之后，且是桌面文案", async () => {
    render(<DataBackupPanel />);
    const headings = screen
      .getAllByRole("heading")
      .map((h) => h.textContent ?? "");
    const imp = headings.indexOf("Import");
    const cleanup = headings.indexOf("Clean up conversation history");
    expect(imp).toBeGreaterThan(-1);
    expect(cleanup).toBeGreaterThan(imp);
    await screen.findByText("3 conversations will be deleted");
  });

  it("时间档换成预览时刻的截止毫秒，全部 = 0；id 数字/字符串互转", async () => {
    const before = Date.now();
    render(<DataBackupPanel />);
    await waitFor(() => expect(appMocks.PreviewChatCleanup).toHaveBeenCalled());
    const first = appMocks.PreviewChatCleanup.mock.calls[0][0];
    expect(first.agentId).toBe(0);
    expect(first.projectId).toBe(0);
    expect(first.beforeMs).toBeGreaterThanOrEqual(before - 90 * DAY);
    expect(first.beforeMs).toBeLessThanOrEqual(Date.now() - 90 * DAY);

    fireEvent.click(screen.getByRole("tab", { name: "All time" }));
    await waitFor(() =>
      expect(appMocks.PreviewChatCleanup).toHaveBeenLastCalledWith({
        beforeMs: 0,
        agentId: 0,
        projectId: 0,
      }),
    );

    fireEvent.click(screen.getByRole("tab", { name: "30 days ago" }));
    await waitFor(() => {
      const last = appMocks.PreviewChatCleanup.mock.lastCall![0];
      expect(last.beforeMs).toBeGreaterThan(Date.now() - 31 * DAY);
      expect(last.beforeMs).toBeLessThanOrEqual(Date.now() - 30 * DAY);
    });

    const select = screen.getByRole("combobox", { name: "Agent" });
    select.focus();
    await userEvent.keyboard("{Enter}");
    await userEvent.click(
      await screen.findByRole("option", { name: "Claude Code" }),
    );
    await waitFor(() =>
      expect(appMocks.PreviewChatCleanup.mock.lastCall![0].agentId).toBe(7),
    );

    const project = screen.getByRole("combobox", { name: "Project" });
    project.focus();
    await userEvent.keyboard("{Enter}");
    await userEvent.click(
      await screen.findByRole("option", { name: "agentre" }),
    );
    await waitFor(() =>
      expect(appMocks.PreviewChatCleanup.mock.lastCall![0].projectId).toBe(5),
    );
  });

  it("确认后执行带上预览的会话 id 与子 Agent id（数字），副本按种类映射", async () => {
    render(<DataBackupPanel />);
    const dialog = await openConfirm();
    expect(within(dialog).getByText(/build-box/)).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: "Delete 3" }));

    await waitFor(() =>
      expect(appMocks.ExecuteChatCleanup).toHaveBeenCalledWith({
        sessionIds: [11, 12, 13],
        subagentSessionIds: [21, 22],
      }),
    );
  });

  it("失败原因码显示本地化文案，不露原始码；重试只送剩下的", async () => {
    appMocks.ExecuteChatCleanup.mockResolvedValueOnce(
      execResp({
        deleted: [11],
        failed: [
          { id: 12, reason: "read_failed" },
          { id: 13, reason: "delete_failed" },
        ],
      }),
    );
    render(<DataBackupPanel />);
    const dialog = await openConfirm();
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete 3" }));

    await within(dialog).findByText("2 conversations couldn't be deleted");
    expect(within(dialog).queryByText(/read_failed|delete_failed/)).toBeNull();
    expect(
      within(dialog).getByText(/Couldn't read this conversation/),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        /Couldn't delete this conversation from this computer/,
      ),
    ).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: "Retry 2" }));
    await waitFor(() =>
      expect(appMocks.ExecuteChatCleanup).toHaveBeenCalledTimes(2),
    );
    expect(appMocks.ExecuteChatCleanup).toHaveBeenLastCalledWith({
      sessionIds: [12, 13],
      subagentSessionIds: [21, 22],
    });
  });

  it("完成：只关已删会话的标签页，刷新侧栏，toast 写已删/待删机器/关闭标签数", async () => {
    openTab(11);
    openTab(12);
    openTab(99); // 没被删，必须留着
    appMocks.ExecuteChatCleanup.mockResolvedValueOnce(
      execResp({
        deleted: [11, 12],
        pending: [
          { kind: "machine", machine: "old-laptop", count: 2 },
          { kind: "account", machine: "", count: 1 },
        ],
      }),
    );
    render(<DataBackupPanel />);
    const dialog = await openConfirm();
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete 3" }));

    await waitFor(() => expect(sonnerMocks.toast.success).toHaveBeenCalled());
    const tabs = useChatTabsStore.getState().tabs;
    expect(
      tabs.map((t) => (t.meta.kind === "session" ? t.meta.sessionId : -1)),
    ).toEqual([99]);
    expect(reloadMock).toHaveBeenCalled();
    const [title, options] = sonnerMocks.toast.success.mock.calls[0];
    expect(title).toBe("Deleted 2 conversations");
    const body = String(options?.description);
    expect(body).toContain(
      "2 on old-laptop will be deleted next time it comes online",
    );
    expect(body).toContain(
      "1 on your account will be deleted next time it comes online",
    );
    expect(body).toContain("Closed 2 tabs");
  });

  it("没有打开的标签页、没有待删：toast 只有已删数量", async () => {
    render(<DataBackupPanel />);
    const dialog = await openConfirm();
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete 3" }));

    await waitFor(() => expect(sonnerMocks.toast.success).toHaveBeenCalled());
    const [title, options] = sonnerMocks.toast.success.mock.calls[0];
    expect(title).toBe("Deleted 3 conversations");
    expect(options?.description).toBeUndefined();
  });
});
