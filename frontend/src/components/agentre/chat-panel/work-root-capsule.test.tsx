import "@testing-library/jest-dom/vitest";

import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@agentre-hub/agentre-ui";
import { beforeEach, describe, expect, it, vi } from "vitest";

const gitStateMock = vi.hoisted(() => vi.fn());
vi.mock("@/../wailsjs/go/app/App", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  WorkspaceFsGitState: (sessionId: number, root: string) =>
    gitStateMock(sessionId, root),
}));

import type { ChatSessionDetail } from "@/hooks/use-chat-session";
import {
  useSessionRootStore,
  type WorkRoot,
} from "@/stores/session-root-store";

import { ChatPanelHeader } from "./chat-panel-header";

const CWD = "/work/demo-app";
const WT = "/work/demo-app-dark-mode";

function root(
  path: string,
  name: string,
  extra: Partial<Pick<WorkRoot, "isPrimary" | "isWorktree" | "aiHere">> = {},
): WorkRoot {
  return {
    path,
    name,
    isPrimary: extra.isPrimary ?? false,
    isWorktree: extra.isWorktree ?? false,
    aiHere: extra.aiHere ?? false,
  };
}

const MAIN_ROOT = (aiHere = false) =>
  root(CWD, "demo-app", { isPrimary: true, aiHere });
const WT_ROOT = (aiHere = false) =>
  root(WT, "dark-mode", { isWorktree: true, aiHere });

function gitState(branch: string, notARepo = false) {
  return {
    notARepo,
    branch,
    worktree: "",
    dirty: 0,
    ahead: 0,
    behind: 0,
    hasUpstream: false,
    commonDir: "",
  };
}

function session(cwd: string): ChatSessionDetail {
  return {
    id: 7,
    title: "dark mode",
    agentId: 1,
    agentName: "Claude Code",
    agentColor: "agent-1",
    backendType: "claudecode",
    cwd,
    deviceID: "",
    deviceName: "",
    execTargetCount: 1,
    lastMessageAt: 0,
  } as unknown as ChatSessionDetail;
}

const apply = (roots: WorkRoot[]) =>
  act(() => useSessionRootStore.getState().applyRoots(7, CWD, roots));

function renderHeader(cwd = CWD) {
  return render(
    <TooltipProvider>
      <ChatPanelHeader
        session={session(cwd)}
        showNewSessionPrompt={false}
        title="dark mode"
        status="idle"
        topline={null}
        agentName="Claude Code"
        metaTail=""
        backgroundTasks={[]}
        onClearCompletedTasks={() => {}}
        canStop={false}
        onStop={() => {}}
        onRename={() => {}}
        onCopyLaunchCommand={() => {}}
        onDelete={() => {}}
      />
    </TooltipProvider>,
  );
}

const capsule = () => screen.getByTestId("work-root-capsule");

beforeEach(() => {
  useSessionRootStore.setState({ bySession: {} });
  gitStateMock.mockReset();
  gitStateMock.mockResolvedValue(gitState("main"));
});

describe("标题栏工作根胶囊 · 呈现", () => {
  // Given 会话没有工作目录, Then 标题栏不显示胶囊。
  it("shows no capsule for a session without a cwd", () => {
    renderHeader("");
    expect(screen.queryByTestId("work-root-capsule")).toBeNull();
  });

  // Given 当前是主仓库（唯一的根）, Then 胶囊是中性样式，显示根名与分支，且不可点击。
  it("shows the main repo neutrally with its branch and is not clickable with one root", async () => {
    apply([MAIN_ROOT(true)]);
    renderHeader();
    await waitFor(() => expect(capsule()).toHaveTextContent("main"));
    expect(capsule()).toHaveTextContent("demo-app");
    expect(capsule()).toHaveAttribute("data-kind", "main");
    expect(capsule().tagName).not.toBe("BUTTON");
    expect(
      within(screen.getByTestId("chat-header-meta")).queryByRole("button"),
    ).toBeNull();
    expect(gitStateMock).toHaveBeenCalledWith(7, "");
  });

  // Given 不是 git 仓库, Then 只显示目录名。
  it("shows only the directory name outside a git repo", async () => {
    gitStateMock.mockResolvedValue(gitState("", true));
    apply([MAIN_ROOT(true)]);
    renderHeader();
    await waitFor(() => expect(gitStateMock).toHaveBeenCalled());
    expect(capsule()).toHaveTextContent(/^demo-app$/);
  });

  // Given 当前是 worktree, Then 胶囊是醒目样式，显示分支名与「worktree」，可点击。
  it("shows a worktree prominently with its branch and the worktree tag", async () => {
    gitStateMock.mockResolvedValue(gitState("dark-mode-branch"));
    apply([MAIN_ROOT(), WT_ROOT(true)]);
    renderHeader();
    await waitFor(() =>
      expect(capsule()).toHaveTextContent("dark-mode-branch"),
    );
    expect(capsule()).toHaveTextContent("worktree");
    expect(capsule()).toHaveAttribute("data-kind", "worktree");
    expect(capsule().tagName).toBe("BUTTON");
    expect(gitStateMock).toHaveBeenLastCalledWith(7, WT);
  });
});

describe("标题栏工作根胶囊 · 下拉", () => {
  // Given 两个根、AI 在 worktree、当前是 worktree, When 打开下拉, Then 每行有名称、
  // 主仓库 / worktree 与路径，不带变更数（按工具名数出来的数字不可信，已去掉）；
  // 当前根打勾，AI 所在根标「AI 在这里」，底部是开着的「跟随 AI 切换」。
  it("lists every root with kind, path, the current check and where the agent is, without a change count", async () => {
    const user = userEvent.setup();
    apply([MAIN_ROOT(), WT_ROOT(true)]);
    renderHeader();
    await user.click(capsule());
    const menu = await screen.findByRole("menu");

    const rows = within(menu).getAllByTestId("work-root-option");
    expect(rows.map((r) => r.getAttribute("data-root"))).toEqual([CWD, WT]);
    expect(rows[0]).toHaveTextContent("demo-app");
    expect(rows[0]).toHaveTextContent("Main repo");
    expect(rows[0]).toHaveTextContent(CWD);
    expect(rows[0]).not.toHaveAttribute("aria-current");
    expect(rows[0]).not.toHaveTextContent("Agent is here");
    expect(within(menu).queryAllByTestId("work-root-count")).toHaveLength(0);

    expect(rows[1]).toHaveTextContent("dark-mode");
    expect(rows[1]).toHaveTextContent("worktree");
    expect(rows[1]).toHaveTextContent(WT);
    expect(rows[1]).toHaveAttribute("aria-current", "true");
    expect(within(rows[1]).getByTestId("work-root-check")).toBeInTheDocument();
    expect(within(rows[0]).queryByTestId("work-root-check")).toBeNull();
    expect(rows[1]).toHaveTextContent("Agent is here");

    const follow = within(menu).getByRole("menuitemcheckbox", {
      name: "Follow the agent",
    });
    expect(follow).toHaveAttribute("aria-checked", "true");
  });

  // Given 跟随开启, When 在下拉里手动选主仓库, Then 当前根切过去、跟随关闭。
  it("turns follow off when a root is picked by hand", async () => {
    const user = userEvent.setup();
    apply([MAIN_ROOT(), WT_ROOT(true)]);
    renderHeader();
    await user.click(capsule());
    const menu = await screen.findByRole("menu");
    await user.click(within(menu).getAllByTestId("work-root-option")[0]);

    await waitFor(() => expect(capsule()).toHaveAttribute("data-kind", "main"));
    expect(useSessionRootStore.getState().bySession[7].follow).toBe(false);
  });

  // Given 跟随已关、当前是主仓库, When 打开「跟随 AI 切换」, Then 立即切到 AI 所在的
  // worktree，开关显示为开。
  it("jumps to the agent's root when the follow switch is turned on", async () => {
    const user = userEvent.setup();
    apply([MAIN_ROOT(), WT_ROOT(true)]);
    act(() => useSessionRootStore.getState().select(7, CWD));
    renderHeader();
    await user.click(capsule());
    const menu = await screen.findByRole("menu");
    const follow = within(menu).getByRole("menuitemcheckbox", {
      name: "Follow the agent",
    });
    expect(follow).toHaveAttribute("aria-checked", "false");
    await user.click(follow);

    expect(useSessionRootStore.getState().bySession[7].follow).toBe(true);
    await waitFor(() =>
      expect(capsule()).toHaveAttribute("data-kind", "worktree"),
    );
  });
});

describe("标题栏工作根胶囊 · 提示", () => {
  // Given 跟随开启、当前在主仓库, When AI 进了 worktree, Then 胶囊下方出现「已跟随 AI
  // 切到 worktree <名称>」与「留在 <原根名>」；点「留在」回到原根并关闭跟随。
  it("notices a follow and offers to stay in the previous root", async () => {
    const user = userEvent.setup();
    apply([MAIN_ROOT(true), WT_ROOT()]);
    renderHeader();
    apply([MAIN_ROOT(), WT_ROOT(true)]);

    const notice = await screen.findByTestId("work-root-notice");
    expect(notice).toHaveTextContent(
      "Followed the agent to worktree dark-mode",
    );
    await user.click(
      within(notice).getByRole("button", { name: "Stay in demo-app" }),
    );

    expect(screen.queryByTestId("work-root-notice")).toBeNull();
    expect(capsule()).toHaveAttribute("data-kind", "main");
    expect(useSessionRootStore.getState().bySession[7].follow).toBe(false);
  });

  // Given 已跟随提示在显示, When 过了约 5 秒, Then 它自行收起，当前根不变。
  it("collapses the follow notice on its own after about five seconds", async () => {
    vi.useFakeTimers();
    try {
      apply([MAIN_ROOT(true), WT_ROOT()]);
      renderHeader();
      apply([MAIN_ROOT(), WT_ROOT(true)]);
      expect(screen.getByTestId("work-root-notice")).toBeInTheDocument();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5100);
      });
      expect(screen.queryByTestId("work-root-notice")).toBeNull();
      expect(capsule()).toHaveAttribute("data-kind", "worktree");
    } finally {
      vi.useRealTimers();
    }
  });

  // Given 当前根是 worktree, When 它从集合中消失, Then 胶囊回到主仓库并提示一次移除。
  it("notices a removed worktree once and falls back to the main repo", async () => {
    apply([MAIN_ROOT(), WT_ROOT(true)]);
    renderHeader();
    apply([MAIN_ROOT(true)]);

    const notice = await screen.findByTestId("work-root-notice");
    expect(notice).toHaveTextContent(
      "Worktree dark-mode was removed; back to demo-app",
    );
    expect(within(notice).queryByRole("button")).toBeNull();
    expect(capsule()).toHaveAttribute("data-kind", "main");
  });
});
