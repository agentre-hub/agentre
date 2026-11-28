import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const runtime = vi.hoisted(() => ({
  handlers: new Map<string, (payload: unknown) => void>(),
  off: vi.fn(),
}));
vi.mock("@/../wailsjs/runtime/runtime", async () => ({
  ...(await vi.importActual("@/../wailsjs/runtime/runtime")),
  EventsOn: vi.fn((name: string, cb: (payload: unknown) => void) => {
    runtime.handlers.set(name, cb);
    return () => {
      runtime.handlers.delete(name);
      runtime.off(name);
    };
  }),
}));

const workRootsMock = vi.fn();
vi.mock("@/../wailsjs/go/app/App", () => ({
  WorkspaceFsWorkRoots: (sessionId: number) => workRootsMock(sessionId),
}));

import {
  useSessionRoot,
  useSessionRootStore,
} from "@/stores/session-root-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { useSessionRootSync } from "../views/use-session-root-sync";

const CWD = "/work/demo-app";
const WT = "/work/demo-app-dark-mode";

function roots(ai: "main" | "wt") {
  return [
    {
      path: CWD,
      name: "demo-app",
      isPrimary: true,
      isWorktree: false,
      aiHere: ai === "main",
    },
    {
      path: WT,
      name: "dark-mode",
      isPrimary: false,
      isWorktree: true,
      aiHere: ai === "wt",
    },
  ];
}

function renderSync(sessionId = 7, cwd = CWD) {
  return renderHook(
    (p: { sessionId: number; cwd: string }) => {
      useSessionRootSync(p);
      return useSessionRoot(p.sessionId);
    },
    { initialProps: { sessionId, cwd } },
  );
}

beforeEach(() => {
  useSessionRootStore.setState({ bySession: {} });
  useSessionStatusStore.getState().__reset();
  runtime.handlers.clear();
  runtime.off.mockReset();
  workRootsMock.mockReset();
  workRootsMock.mockResolvedValue(roots("main"));
});

describe("useSessionRootSync", () => {
  // Given 会话有工作目录, When 挂载, Then 取一次认领集合；取回之前当前根就是 cwd。
  it("fetches the claimed roots and shows the cwd until they arrive", async () => {
    let land: (v: unknown) => void = () => {};
    workRootsMock.mockReturnValue(new Promise((r) => (land = r)));
    const { result } = renderSync();
    expect(result.current.current).toBe(CWD);
    expect(workRootsMock).toHaveBeenCalledWith(7);

    await act(async () => land(roots("main")));
    expect(result.current.roots).toHaveLength(2);
  });

  // Given 跟随开启, When 后端在轮次进行中推 chat:workroots:<id>（AI 进了 worktree）,
  // Then 当前根立即切过去——不等轮末重取。
  it("applies a chat:workroots push mid-turn", async () => {
    const { result } = renderSync();
    await waitFor(() => expect(result.current.roots).toHaveLength(2));

    act(() =>
      runtime.handlers.get("chat:workroots:7")!({
        sessionId: 7,
        roots: roots("wt"),
      }),
    );
    expect(result.current.current).toBe(WT);
    expect(result.current.notice?.kind).toBe("followed");
  });

  // Given 一次取数还在途, When 推送先到, Then 晚到的取数结果（更旧）被丢弃。
  it("drops a fetch that lands after a newer push", async () => {
    let land: (v: unknown) => void = () => {};
    workRootsMock.mockReturnValue(new Promise((r) => (land = r)));
    const { result } = renderSync();

    act(() =>
      runtime.handlers.get("chat:workroots:7")!({
        sessionId: 7,
        roots: roots("wt"),
      }),
    );
    await act(async () => land(roots("main")));
    expect(result.current.current).toBe(WT);
  });

  // Given 一次取数还在途, When 点链接当场认领了 worktree（认领结果直接落进会话根 store，
  // 不经推送）, Then 晚到的旧取数不会把刚认领的根冲掉、记成「已移除」。
  it("drops a fetch that lands after a claim applied straight to the store", async () => {
    let land: (v: unknown) => void = () => {};
    workRootsMock.mockReturnValue(new Promise((r) => (land = r)));
    const { result } = renderSync();

    act(() => useSessionRootStore.getState().applyRoots(7, CWD, roots("main")));
    await act(async () => land([roots("main")[0]]));

    expect(result.current.roots.map((r) => r.path)).toEqual([CWD, WT]);
    expect(result.current.removed).toEqual([]);
  });

  // Given 轮次结束, Then 重取一次（worktree 被移除只在重取里看得到）。
  it("refetches when a turn settles", async () => {
    renderSync();
    await waitFor(() => expect(workRootsMock).toHaveBeenCalledTimes(1));
    act(() => useSessionStatusStore.getState().bumpDone(7, { kind: "done" }));
    await waitFor(() => expect(workRootsMock).toHaveBeenCalledTimes(2));
  });

  // Given 取数失败, Then 保留已有的集合，不当作「根都没了」。
  it("keeps the known roots when a refetch fails", async () => {
    const { result } = renderSync();
    await waitFor(() => expect(result.current.roots).toHaveLength(2));
    workRootsMock.mockRejectedValue(new Error("boom"));
    act(() => useSessionStatusStore.getState().bumpDone(7, { kind: "done" }));
    await waitFor(() => expect(workRootsMock).toHaveBeenCalledTimes(2));
    expect(result.current.roots).toHaveLength(2);
    expect(result.current.notice).toBeNull();
  });

  // Given 会话没有工作目录, Then 不取数也不订阅，当前根为空。
  it("does nothing for a session without a cwd", () => {
    const { result } = renderSync(7, "");
    expect(workRootsMock).not.toHaveBeenCalled();
    expect(runtime.handlers.size).toBe(0);
    expect(result.current.current).toBe("");
  });

  // Given 卸载, Then 退订本会话的事件。
  it("unsubscribes on unmount", () => {
    const { unmount } = renderSync();
    unmount();
    expect(runtime.off).toHaveBeenCalledWith("chat:workroots:7");
  });
});
