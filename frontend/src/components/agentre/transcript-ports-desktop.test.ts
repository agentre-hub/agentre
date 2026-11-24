import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";

const navigate = vi.fn();

vi.mock("react-router-dom", () => ({ useNavigate: () => navigate }));

vi.mock("../../../wailsjs/go/app/App", () => ({
  AnswerToolApproval: vi.fn(),
  AnswerToolPermission: vi.fn(),
  AnswerUserQuestion: vi.fn(),
  OpenPath: vi.fn(),
  ResolveSessionURL: vi.fn(),
  ResolveExecApproval: vi.fn(),
  ResolvePlanAction: vi.fn(),
  WorkspaceFsClaimPath: vi.fn(),
  WorkspaceFsReadFile: vi.fn(),
}));

vi.mock("../../../wailsjs/runtime/runtime", () => ({
  BrowserOpenURL: vi.fn(),
}));

import {
  ResolveSessionURL,
  WorkspaceFsClaimPath,
} from "../../../wailsjs/go/app/App";
import type { workspace_fs_svc } from "../../../wailsjs/go/models";
import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";

import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import { useFileSettingsStore } from "@/stores/file-settings-store";
import { useSessionRootStore } from "@/stores/session-root-store";
import { useSessionTerminalsStore } from "@/stores/session-terminals-store";
import {
  selectIsToolWindowShown,
  useToolWindowLayout,
} from "@/stores/tool-window-layout-store";
import {
  currentUrl,
  selectActiveTab,
  useWebPreviewStore,
} from "./tool-windows/web-preview-store";
import {
  toLinkRoots,
  useDesktopTranscriptPorts,
} from "./transcript-ports-desktop";

beforeEach(() => {
  navigate.mockReset();
  vi.mocked(ResolveSessionURL).mockReset();
  vi.mocked(BrowserOpenURL).mockReset();
  vi.mocked(WorkspaceFsClaimPath).mockReset();
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useFileSettingsStore.setState({ settings: { openAction: "preview" } });
  useSessionRootStore.setState({ bySession: {} });
});

function root(
  path: string,
  extra: Partial<workspace_fs_svc.WorkRootView> = {},
): workspace_fs_svc.WorkRootView {
  return {
    path,
    name: path.split("/").pop() ?? path,
    isWorktree: !extra.isPrimary,
    isPrimary: false,
    aiHere: false,
    ...extra,
  } as workspace_fs_svc.WorkRootView;
}

const PRIMARY = root("/repo", { isPrimary: true });
const WT = root("/repo-wt");

function previewTabs(sessionId: number) {
  return (
    useFilePreviewTabsStore.getState().previewTabsBySession[sessionId]?.tabs ??
    []
  ).map((t) => ({ root: t.root, path: t.path }));
}

describe("useDesktopTranscriptPorts 的 openSession", () => {
  it("Given a child session id, When the port is called, Then the chat tab for it is opened and the chat route shown", () => {
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    result.current.openSession?.(42);

    const { tabs, activeTabId } = useChatTabsStore.getState();
    const active = tabs.find((t) => t.id === activeTabId);
    expect(active?.meta).toEqual({ kind: "session", sessionId: 42 });
    expect(navigate).toHaveBeenCalledWith("/chat");
  });

  it("Given the session already has a tab, When the port is called again, Then that tab is reused", () => {
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    result.current.openSession?.(42);
    result.current.openSession?.(42);

    expect(useChatTabsStore.getState().tabs).toHaveLength(1);
  });
});

describe("useDesktopTranscriptPorts 的 openExternalURL", () => {
  it("Given a remote session's localhost link, When opened with the session id, Then the browser gets the forwarded address, not the raw link", async () => {
    vi.mocked(ResolveSessionURL).mockResolvedValue(
      "http://127.0.0.1:54321/p?q=1",
    );
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    await result.current.openExternalURL?.("http://localhost:3000/p?q=1", {
      sessionId: 42,
    });

    expect(ResolveSessionURL).toHaveBeenCalledWith(
      42,
      "http://localhost:3000/p?q=1",
    );
    expect(BrowserOpenURL).toHaveBeenCalledTimes(1);
    expect(BrowserOpenURL).toHaveBeenCalledWith("http://127.0.0.1:54321/p?q=1");
  });

  it("Given a link without a session, When opened, Then it is opened unchanged and nothing is resolved", async () => {
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    await result.current.openExternalURL?.("https://example.com/a");

    expect(ResolveSessionURL).not.toHaveBeenCalled();
    expect(BrowserOpenURL).toHaveBeenCalledWith("https://example.com/a");
  });

  it("Given resolving fails (device offline), When opened, Then the error surfaces and the raw localhost link is not opened", async () => {
    vi.mocked(ResolveSessionURL).mockRejectedValue(new Error("offline"));
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    await expect(
      result.current.openExternalURL?.("http://localhost:3000/", {
        sessionId: 42,
      }),
    ).rejects.toThrow("offline");
    expect(BrowserOpenURL).not.toHaveBeenCalled();
  });
});

describe("useDesktopTranscriptPorts 的 previewFile（链接归属的工作根）", () => {
  beforeEach(() => {
    useSessionRootStore.getState().applyRoots(42, "/repo", [PRIMARY, WT]);
  });

  it("Given a link owned by the primary root, When previewed, Then the tab uses the canonical primary root so the sidebar opens the same tab", () => {
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    expect(
      result.current.previewFile?.(42, { root: "/repo", path: "src/a.ts" }),
    ).toBe(true);

    expect(previewTabs(42)).toEqual([{ root: "", path: "src/a.ts" }]);
  });

  it("Given a link owned by a worktree, When previewed, Then the tab is bound to that worktree", () => {
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    result.current.previewFile?.(42, { root: "/repo-wt", path: "src/a.ts" });

    expect(previewTabs(42)).toEqual([{ root: "/repo-wt", path: "src/a.ts" }]);
  });

  it("Given the click setting is the system app, When a worktree link is previewed, Then the host declines and opens no tab", () => {
    useFileSettingsStore.setState({ settings: { openAction: "external" } });
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    expect(
      result.current.previewFile?.(42, { root: "/repo-wt", path: "src/a.ts" }),
    ).toBe(false);
    expect(previewTabs(42)).toEqual([]);
  });
});

describe("useDesktopTranscriptPorts 的 claimPath", () => {
  it("Given an unclaimed worktree path, When claimed, Then the backend claims it, the session's roots update and the full set comes back", async () => {
    useSessionRootStore.getState().applyRoots(42, "/repo", [PRIMARY]);
    const wt3 = root("/repo-wt3");
    vi.mocked(WorkspaceFsClaimPath).mockResolvedValue([PRIMARY, wt3]);
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    const roots = await result.current.claimPath?.(42, "/repo-wt3/src/c.ts");

    expect(WorkspaceFsClaimPath).toHaveBeenCalledWith(42, "/repo-wt3/src/c.ts");
    expect(roots).toEqual([
      { path: "/repo", name: "repo", primary: true },
      { path: "/repo-wt3", name: "repo-wt3", primary: false },
    ]);
    expect(
      useSessionRootStore.getState().bySession[42]?.roots.map((r) => r.path),
    ).toEqual(["/repo", "/repo-wt3"]);
  });

  it("Given the claim fails, When claimed, Then the failure surfaces to the caller", async () => {
    vi.mocked(WorkspaceFsClaimPath).mockRejectedValue(new Error("not a repo"));
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    await expect(result.current.claimPath?.(42, "/tmp/x.ts")).rejects.toThrow(
      "not a repo",
    );
  });
});

describe("useDesktopTranscriptPorts 的 attachTerminal", () => {
  beforeEach(() => {
    useSessionTerminalsStore.setState({ bySession: {}, viewing: {} });
    useToolWindowLayout.getState().hide("terminal");
  });

  it("Given a session tab is in front, When a card attaches a running process, Then it opens as a tab of that session's terminal window and the window is revealed", () => {
    useChatTabsStore.getState().openSession(42);
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    result.current.attachTerminal?.({
      terminalId: "cmd-1",
      command: "npm test",
    });

    const entry = useSessionTerminalsStore.getState().bySession[42];
    expect(entry.tabs).toEqual([
      expect.objectContaining({
        id: "cmd-1",
        attach: true,
        command: "npm test",
      }),
    ]);
    expect(entry.activeId).toBe("cmd-1");
    const layout = useToolWindowLayout.getState();
    expect(layout.shown[layout.slotOf.terminal]).toBe("terminal");
    // 不再另开一个对话标签页。
    expect(
      useChatTabsStore
        .getState()
        .tabs.filter((t) => t.meta.kind === "terminal"),
    ).toEqual([]);
  });

  it("Given no session tab is in front, When a card attaches, Then it falls back to a standalone terminal chat tab", () => {
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    result.current.attachTerminal?.({
      terminalId: "cmd-1",
      command: "npm test",
    });

    expect(useSessionTerminalsStore.getState().bySession).toEqual({});
    expect(
      useChatTabsStore
        .getState()
        .tabs.filter((t) => t.meta.kind === "terminal"),
    ).toHaveLength(1);
  });
});

describe("useDesktopTranscriptPorts 的 previewURL", () => {
  it("Given a localhost link in a session, When previewURL is called, Then the web preview window shows and opens it, with the default open path untouched", () => {
    useWebPreviewStore.setState({ bySession: {} });
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    result.current.previewURL?.(7, "http://localhost:3000/a?x=1");

    const tab = selectActiveTab(useWebPreviewStore.getState().bySession[7]);
    expect(tab && currentUrl(tab)).toBe("http://localhost:3000/a?x=1");
    expect(
      selectIsToolWindowShown("webPreview")(useToolWindowLayout.getState()),
    ).toBe(true);
    expect(BrowserOpenURL).not.toHaveBeenCalled();
    expect(ResolveSessionURL).not.toHaveBeenCalled();
  });
});

describe("toLinkRoots", () => {
  it("Given no claimed roots yet, When mapped, Then the session cwd stands in as the primary root", () => {
    expect(toLinkRoots([], "/repo")).toEqual([
      { path: "/repo", primary: true },
    ]);
  });

  it("Given claimed roots, When mapped, Then each keeps its name and primary flag", () => {
    expect(toLinkRoots([PRIMARY, WT], "/repo")).toEqual([
      { path: "/repo", name: "repo", primary: true },
      { path: "/repo-wt", name: "repo-wt", primary: false },
    ]);
  });
});
