import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({
  ListAgentBackends: vi.fn(),
}));
vi.mock("@/../wailsjs/go/app/App", () => api);

import { useSessionRootStore } from "@/stores/session-root-store";

import type { chat_svc } from "../../../../../wailsjs/go/models";
import {
  SessionInfoToolWindow,
  type SessionInfoData,
} from "../session-info-tool-window";

const SID = 7;

const baseSession = {
  id: SID,
  agentId: 3,
  agentName: "Architect",
  agentColor: "agent-3",
  agentIcon: "",
  agentAvatarDataUrl: "",
  backendType: "claudecode",
  agentBackendId: 11,
  deviceID: "",
  deviceName: "",
  createtime: new Date("2026-11-20T08:30:00").getTime(),
  providerSessionId: "cli-session-abc-123",
  carriedMemory: [],
} as unknown as chat_svc.ChatSessionDetail;

function data(over: Partial<SessionInfoData> = {}): SessionInfoData {
  return {
    session: baseSession,
    permissionMode: "acceptEdits",
    modelLabel: "claude-opus-4",
    reasoningEffort: "high",
    contextUsage: { used: 50_000, max: 200_000 },
    turns: 4,
    usage: { input: 1200, output: 340, cacheHits: 800 },
    onCopyLaunchCommand: vi.fn(),
    ...over,
  };
}

function seedRoots(
  roots: Array<{
    path: string;
    name: string;
    isWorktree?: boolean;
    isPrimary?: boolean;
  }>,
) {
  useSessionRootStore.setState({
    bySession: {
      [SID]: {
        cwd: roots[0]?.path ?? "",
        roots: roots.map((r) => ({
          isWorktree: false,
          isPrimary: false,
          aiHere: false,
          ...r,
        })),
        loaded: true,
        selected: null,
        follow: true,
        notice: null,
        removed: [],
      },
    },
  });
}

function renderWindow(info: SessionInfoData) {
  return render(<SessionInfoToolWindow sessionId={SID} info={info} />);
}

const row = (key: string) => screen.queryByTestId(`session-info-row-${key}`);
const value = (key: string) =>
  within(screen.getByTestId(`session-info-row-${key}`)).getByTestId(
    "session-info-value",
  );

beforeEach(() => {
  api.ListAgentBackends.mockReset();
  api.ListAgentBackends.mockResolvedValue({
    items: [{ id: 11, name: "My Claude" }],
  });
  useSessionRootStore.setState({ bySession: {} });
  seedRoots([
    { path: "/repo", name: "repo", isPrimary: true },
    { path: "/repo-wt", name: "repo-wt", isWorktree: true },
    { path: "/repo-wt2", name: "repo-wt2", isWorktree: true },
  ]);
});

describe("会话信息工具窗 · 各行", () => {
  // Given 全部数据可得, Then 每一行按数据渲染。
  it("renders every row from data", async () => {
    renderWindow(data());

    expect(value("agent")).toHaveTextContent("Architect");
    await waitFor(() =>
      expect(value("backend")).toHaveTextContent("My Claude · Local"),
    );
    expect(value("model")).toHaveTextContent("claude-opus-4 · high");
    expect(value("permission")).toHaveTextContent("Accept Edits");
    expect(value("context")).toHaveTextContent("50.0k / 200k");
    expect(value("usage")).toHaveTextContent(
      "Input 1.2k · Output 340 · Cache hits 800",
    );
    expect(value("memory")).toHaveTextContent("None");
    expect(value("location")).toHaveTextContent(
      "repo · Main repo · 2 other worktrees",
    );
    expect(value("created")).toHaveTextContent("4 turns");
    expect(value("created")).toHaveTextContent(/2026/);
    expect(value("cliSession")).toHaveTextContent("cli-session-abc-123");
  });

  // Given 远端会话, Then 后端行写设备名而不是「本地」。
  it("shows the device name for a remote session", async () => {
    renderWindow(
      data({
        session: {
          ...baseSession,
          deviceID: "sha256:dev",
          deviceName: "build-box",
        } as chat_svc.ChatSessionDetail,
      }),
    );
    await waitFor(() =>
      expect(value("backend")).toHaveTextContent("My Claude · build-box"),
    );
  });

  // Given 某项数据不可得, Then 那一行整行不显示，不编值。
  it("omits a row whose data is unavailable", async () => {
    seedRoots([]);
    api.ListAgentBackends.mockResolvedValue({ items: [] });
    renderWindow(
      data({
        session: {
          ...baseSession,
          providerSessionId: "",
          createtime: 0,
        } as chat_svc.ChatSessionDetail,
        permissionMode: undefined,
        modelLabel: undefined,
        reasoningEffort: undefined,
        contextUsage: undefined,
        usage: null,
      }),
    );

    // 等后端查询落定（没有这个后端 → 那行也不显示）。
    await waitFor(() => expect(api.ListAgentBackends).toHaveBeenCalled());
    await Promise.resolve();

    for (const key of [
      "backend",
      "model",
      "permission",
      "context",
      "usage",
      "location",
      "created",
      "cliSession",
    ]) {
      expect(row(key), key).toBeNull();
    }
    expect(value("agent")).toHaveTextContent("Architect");
    expect(value("memory")).toHaveTextContent("None");
  });

  it("shows only the model when no effective reasoning effort is known", () => {
    renderWindow(data({ reasoningEffort: "" }));
    expect(value("model")).toHaveTextContent(/^claude-opus-4$/);
  });

  it("renders nothing when there is no session yet", () => {
    const { container } = renderWindow(data({ session: null }));
    expect(
      container.querySelectorAll("[data-testid^=session-info-row-]"),
    ).toHaveLength(0);
  });

  // Given 权限模式是已知档 / 未知档, Then 标签与输入框旁的药丸同一口径（档位名表，未知原样）。
  it("labels the permission mode exactly like the composer pill", () => {
    const { rerender } = renderWindow(
      data({ permissionMode: "bypassPermissions" }),
    );
    expect(value("permission")).toHaveTextContent("Bypass");
    rerender(
      <SessionInfoToolWindow
        sessionId={SID}
        info={data({ permissionMode: "dontAsk" })}
      />,
    );
    expect(value("permission")).toHaveTextContent("dontAsk");
  });

  it("counts a single worktree with the singular wording and none when alone", () => {
    seedRoots([
      { path: "/repo", name: "repo", isPrimary: true },
      { path: "/repo-wt", name: "repo-wt", isWorktree: true },
    ]);
    const { unmount } = renderWindow(data());
    expect(value("location")).toHaveTextContent("1 other worktree");
    unmount();

    seedRoots([{ path: "/repo", name: "repo", isPrimary: true }]);
    renderWindow(data());
    expect(value("location")).not.toHaveTextContent("other worktree");
  });

  it("names a worktree current root as a worktree", () => {
    seedRoots([
      { path: "/repo-wt", name: "repo-wt", isWorktree: true, isPrimary: true },
    ]);
    renderWindow(data());
    expect(value("location")).toHaveTextContent("repo-wt · worktree");
  });
});

describe("会话信息工具窗 · 带入的记忆", () => {
  const carried = [
    ["Agent", "[m1] likes short answers (2026-11-01)"],
    [
      "Project: Alpha",
      "[m9] deploy via gitea (2026-11-03)",
      "[m10] use pnpm (2026-11-04)",
    ],
  ];

  // Given 带入了 3 条, Then 行内显示条数与「查看」，点开只读列出各层条目。
  it("shows the count and opens a read-only view of the carried-in memory", async () => {
    renderWindow(
      data({
        session: {
          ...baseSession,
          carriedMemory: carried,
        } as unknown as chat_svc.ChatSessionDetail,
      }),
    );
    expect(value("memory")).toHaveTextContent("3 entries");

    await userEvent.setup().click(screen.getByRole("button", { name: "View" }));

    const view = await screen.findByTestId("session-info-memory-view");
    expect(within(view).getByText("Agent")).toBeInTheDocument();
    expect(within(view).getByText("Project: Alpha")).toBeInTheDocument();
    expect(
      within(view).getByText("[m10] use pnpm (2026-11-04)"),
    ).toBeInTheDocument();
    // 只读：里面没有输入控件，条目文本可选中。
    expect(within(view).queryAllByRole("textbox")).toHaveLength(0);
    expect(view).toHaveAttribute("data-selectable-text", "true");
  });

  // Given 没有带入, Then 显示「无」而且没有「查看」。
  it("shows None without a View button when nothing was carried in", () => {
    renderWindow(data());
    expect(value("memory")).toHaveTextContent("None");
    expect(screen.queryByRole("button", { name: "View" })).toBeNull();
  });
});

describe("会话信息工具窗 · 复制启动命令与可选中", () => {
  it.each(["claudecode", "codex", "piagent"])(
    "offers Copy Launch Command for %s and calls the handler",
    async (backendType) => {
      const onCopy = vi.fn();
      renderWindow(
        data({
          session: {
            ...baseSession,
            backendType,
          } as chat_svc.ChatSessionDetail,
          onCopyLaunchCommand: onCopy,
        }),
      );
      await userEvent
        .setup()
        .click(screen.getByRole("button", { name: "Copy Launch Command" }));
      expect(onCopy).toHaveBeenCalledTimes(1);
    },
  );

  it.each(["builtin", "openclaw", "hermes", ""])(
    "does not offer Copy Launch Command for %s",
    (backendType) => {
      renderWindow(
        data({
          session: {
            ...baseSession,
            backendType,
          } as chat_svc.ChatSessionDetail,
        }),
      );
      expect(
        screen.queryByRole("button", { name: "Copy Launch Command" }),
      ).toBeNull();
    },
  );

  // Given 全部行可见, Then 每个值都标记为可选中文本，行内没有复制按钮。
  it("marks every value as selectable text and adds no copy buttons", async () => {
    renderWindow(data());
    await waitFor(() =>
      expect(value("backend")).toHaveTextContent("My Claude"),
    );
    const values = screen.getAllByTestId("session-info-value");
    expect(values.length).toBeGreaterThanOrEqual(9);
    for (const v of values) {
      expect(v).toHaveAttribute("data-selectable-text", "true");
      expect(within(v).queryAllByRole("button")).toHaveLength(0);
    }
    expect(screen.queryByRole("button", { name: /^copy$/i })).toBeNull();
    expect(
      screen.queryByRole("button", { name: /copy (session|cli|id)/i }),
    ).toBeNull();
  });
});
