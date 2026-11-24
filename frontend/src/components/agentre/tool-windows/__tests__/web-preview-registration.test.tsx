import "@testing-library/jest-dom/vitest";

import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/../wailsjs/runtime/runtime", async () => ({
  ...(await vi.importActual("@/../wailsjs/runtime/runtime")),
  EventsOn: vi.fn(() => () => {}),
  EventsOff: vi.fn(),
}));

const api = vi.hoisted(() => ({
  ProbeSessionWebPreview: vi.fn(),
  ListAgentBackends: vi.fn(),
  IssueListBySession: vi.fn(),
  ListSessionDispatches: vi.fn(),
  ListSessionsSharingRoot: vi.fn(),
}));
vi.mock("@/../wailsjs/go/app/App", () => ({
  ...api,
  WorkspaceFsWorkRoots: () => Promise.resolve([]),
  WorkspaceFsGitState: () => Promise.resolve({ notARepo: true }),
  WorkspaceFsGitChanges: () =>
    Promise.resolve({ notARepo: true, changes: [], baseRef: "" }),
  WorkspaceFsListDir: () =>
    Promise.resolve({ path: "/repo", entries: [], truncated: false }),
  GetAppSetting: () => Promise.reject(new Error("nf")),
}));
vi.mock("../../../../../wailsjs/go/app/App", () => ({
  ...api,
  GetAppSetting: () => Promise.reject(new Error("nf")),
  UpdateAppSettings: vi.fn(),
}));

import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useSessionRootStore } from "@/stores/session-root-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import type { chat_svc } from "../../../../../wailsjs/go/models";
import { ChatContextToolWindows } from "../../chat-context-sidebar";
import {
  isToolWindowShown,
  showOnly,
  stripButton,
} from "../../chat-context-sidebar/__tests__/tool-window-helpers";
import { openWebPreview, useWebPreviewStore } from "../web-preview-store";

const proc = (pid: number, command: string, ports: number[]) => ({
  pid,
  command,
  ports,
  startedAt: 1,
});

function renderWindows(
  processes: ReturnType<typeof proc>[],
  session: Partial<chat_svc.ChatSessionDetail> | null = null,
) {
  return render(
    <MemoryRouter>
      <ChatContextToolWindows
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
        remote={session?.deviceID !== undefined && session.deviceID !== ""}
        processes={processes}
        sessionInfo={{
          session: session as chat_svc.ChatSessionDetail | null,
          permissionMode: "plan",
          modelLabel: "m",
          reasoningEffort: "",
          contextUsage: { used: 0, max: 0 },
          onCopyLaunchCommand: () => {},
        }}
      />
    </MemoryRouter>,
  );
}

beforeAll(() => {
  (
    window as unknown as {
      happyDOM: { settings: { disableIframePageLoading: boolean } };
    }
  ).happyDOM.settings.disableIframePageLoading = true;
});

beforeEach(() => {
  localStorage.clear();
  Object.values(api).forEach((f) => f.mockReset());
  api.ProbeSessionWebPreview.mockImplementation((_id: number, url: string) =>
    Promise.resolve({
      url,
      forwarded: false,
      embeddable: true,
      reason: "",
      detail: "",
    }),
  );
  api.ListAgentBackends.mockResolvedValue({ items: [] });
  api.IssueListBySession.mockResolvedValue([]);
  api.ListSessionDispatches.mockResolvedValue({
    dispatchedBy: [],
    dispatched: [],
  });
  api.ListSessionsSharingRoot.mockResolvedValue([]);
  showOnly();
  useWebPreviewStore.setState({ bySession: {} });
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
  useSessionStatusStore.getState().__reset();
  useSessionRootStore.setState({ bySession: {} });
});

describe("网页预览工具窗 · 接入", () => {
  // Given 会话有监听端口的进程, Then 图标条有「网页预览」，点开后端口成标签（来源与后台弹层同一份进程列表）。
  it("registers the Web preview window and turns the session's listening ports into tabs", async () => {
    renderWindows([
      proc(10, "npm run dev", [3000]),
      proc(11, "go run .", []),
      proc(12, "vite", [5173, 24678]),
    ]);

    await userEvent.setup().click(stripButton("Web preview"));

    const region = screen.getByRole("region", { name: "Web preview" });
    expect(
      within(region)
        .getAllByRole("tab")
        .map((t) => t.textContent),
    ).toEqual([":3000 npm run dev", ":5173 vite", ":24678 vite"]);
  });

  // Given 窗口没打开, When 端口出现 / 消失, Then 标签仍随进程列表同步，打开时已就绪。
  it("keeps tabs in sync with the process list while the window is hidden", async () => {
    const view = renderWindows([proc(10, "a", [3000]), proc(11, "b", [8080])]);
    view.rerender(
      <MemoryRouter>
        <ChatContextToolWindows
          sessionId={1}
          messages={[]}
          activeMessageId={null}
          onJumpToMessage={() => {}}
          cwd="/repo"
          processes={[proc(10, "a", [3000])]}
        />
      </MemoryRouter>,
    );

    await userEvent.setup().click(stripButton("Web preview"));
    const region = screen.getByRole("region", { name: "Web preview" });
    expect(
      within(region)
        .getAllByRole("tab")
        .map((t) => t.textContent),
    ).toEqual([":3000 a"]);
  });

  // Given 远端会话, When 地址被转发, Then 窗口标出设备名。
  it("labels the execution device for a forwarded remote session", async () => {
    api.ProbeSessionWebPreview.mockResolvedValue({
      url: "http://127.0.0.1:45123/",
      forwarded: true,
      embeddable: true,
      reason: "",
      detail: "",
    });
    renderWindows([proc(10, "npm run dev", [3000])], {
      id: 1,
      deviceID: "7",
      deviceName: "build-box",
    });
    await userEvent.setup().click(stripButton("Web preview"));
    expect(
      await screen.findByText("build-box · via port forward"),
    ).toBeInTheDocument();
  });

  // Given 入口调用 openWebPreview, Then 显示窗口并在其中打开该地址。
  it("openWebPreview shows the window and opens the address in it", async () => {
    renderWindows([proc(10, "a", [3000])]);
    expect(isToolWindowShown("webPreview")).toBe(false);

    act(() => openWebPreview(1, "http://localhost:3000/docs"));

    expect(isToolWindowShown("webPreview")).toBe(true);
    await waitFor(() =>
      expect(screen.getByRole("textbox", { name: "Address" })).toHaveValue(
        "http://localhost:3000/docs",
      ),
    );
  });
});
