import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

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

import { useSessionRootStore } from "@/stores/session-root-store";
import { useToolWindowLayout } from "@/stores/tool-window-layout-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { useWebPreviewStore } from "../../tool-windows/web-preview-store";
import type { SessionProcess } from "../../background-tasks/types";
import { ChatContextToolWindows } from "../index";

import { showOnly } from "./tool-window-helpers";

const proc = (pid: number, command: string, ports: number[]) => ({
  pid,
  command,
  ports,
  startedAt: 1,
});

function windows(
  processes: SessionProcess[] | undefined,
  onRefreshProcesses?: () => void,
) {
  return (
    <MemoryRouter>
      <ChatContextToolWindows
        sessionId={1}
        messages={[]}
        activeMessageId={null}
        onJumpToMessage={() => {}}
        cwd="/repo"
        remote={false}
        processes={processes}
        onRefreshProcesses={onRefreshProcesses}
      />
    </MemoryRouter>
  );
}

function tabKeys(): string[] {
  return (useWebPreviewStore.getState().bySession[1]?.tabs ?? []).map(
    (t) => `${t.key}:${t.listening ? "on" : "gone"}`,
  );
}

beforeEach(() => {
  localStorage.clear();
  Object.values(api).forEach((f) => f.mockReset());
  api.ListAgentBackends.mockResolvedValue({ items: [] });
  api.IssueListBySession.mockResolvedValue([]);
  api.ListSessionDispatches.mockResolvedValue({
    dispatchedBy: [],
    dispatched: [],
  });
  api.ListSessionsSharingRoot.mockResolvedValue([]);
  api.ProbeSessionWebPreview.mockImplementation((_id: number, url: string) =>
    Promise.resolve({
      url,
      forwarded: false,
      embeddable: true,
      reason: "",
      detail: "",
    }),
  );
  showOnly();
  useWebPreviewStore.setState({ bySession: {} });
  useSessionStatusStore.getState().__reset();
  useSessionRootStore.setState({ bySession: {} });
});

describe("网页预览 · 端口标签同步", () => {
  // spec「网页预览工具窗」：非当前标签随端口消失而移除，当前标签保留并标「不再监听」。
  // 进程列表不可知（面板收着、取数失败）不是「端口消失」：标签原样留着。
  it("leaves the port tabs alone while the session's process list is unknown", async () => {
    const { rerender } = render(
      windows([proc(1, "npm run dev", [3000]), proc(2, "vite", [5173])]),
    );
    await waitFor(() => expect(tabKeys()).toHaveLength(2));
    const before = tabKeys();

    rerender(windows(undefined));

    expect(tabKeys()).toEqual(before);
    expect(tabKeys().every((k) => k.endsWith(":on"))).toBe(true);
  });

  it("still drops a non-current tab once a known list no longer has its port", async () => {
    const { rerender } = render(
      windows([proc(1, "npm run dev", [3000]), proc(2, "vite", [5173])]),
    );
    await waitFor(() => expect(tabKeys()).toHaveLength(2));

    rerender(windows([proc(1, "npm run dev", [3000])]));

    await waitFor(() => expect(tabKeys()).toHaveLength(1));
  });
});

describe("网页预览 · 重新加载", () => {
  // spec：当前标签的端口不再监听时显示「这个端口已不再监听」与「重新加载」——重新加载要
  // 立刻重取会话的进程与端口，而不是等下一次轮询。
  it("asks the host to refetch the session's processes when Reload is pressed on a gone tab", async () => {
    const refresh = vi.fn();
    const { rerender } = render(
      windows([proc(1, "npm run dev", [3000])], refresh),
    );
    await waitFor(() => expect(tabKeys()).toHaveLength(1));
    useToolWindowLayout.getState().show("webPreview");
    rerender(windows([], refresh));

    const region = await screen.findByRole("region", { name: "Web preview" });
    await userEvent.click(
      await within(region).findByRole("button", { name: "Reload" }),
    );

    expect(refresh).toHaveBeenCalledTimes(1);
  });
});
