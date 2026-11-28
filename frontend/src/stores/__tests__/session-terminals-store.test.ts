import { act } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import { useSessionRootStore, type WorkRoot } from "../session-root-store";
import {
  hasLiveSessionTerminal,
  useSessionTerminalsStore,
} from "../session-terminals-store";

const CWD = "/work/demo-app";
const WT = "/work/demo-app-dark-mode";

function root(path: string, name: string, isPrimary: boolean): WorkRoot {
  return { path, name, isPrimary, isWorktree: !isPrimary, aiHere: false };
}

const s = () => useSessionTerminalsStore.getState();
const entry = (sessionId: number) => s().bySession[sessionId];

beforeEach(() => {
  useSessionTerminalsStore.setState({ bySession: {} });
  useSessionRootStore.setState({ bySession: {} });
});

describe("session terminals · open at the session's current root", () => {
  // Given 会话只有主根, When 新建终端, Then 标签记着主根的规范标识（空串）与它的路径。
  it("opens on the primary root with the canonical empty root arg", () => {
    act(() =>
      useSessionRootStore
        .getState()
        .applyRoots(7, CWD, [root(CWD, "demo-app", true)]),
    );
    const id = s().openInCurrentRoot(7);
    expect(entry(7).tabs).toHaveLength(1);
    expect(entry(7).tabs[0]).toMatchObject({
      id,
      root: "",
      rootPath: CWD,
      exited: false,
    });
    expect(entry(7).activeId).toBe(id);
  });

  // Given 当前根是 worktree, When 新建终端, Then 标签记着 worktree 的绝对路径。
  it("opens on the selected worktree root with its absolute path", () => {
    act(() => {
      useSessionRootStore
        .getState()
        .applyRoots(7, CWD, [
          root(CWD, "demo-app", true),
          root(WT, "dark-mode", false),
        ]);
      useSessionRootStore.getState().select(7, WT);
    });
    s().openInCurrentRoot(7);
    expect(entry(7).tabs[0]).toMatchObject({ root: WT, rootPath: WT });
  });

  // Given 还没取到任何工作根, When 新建终端, Then 仍按主根（空串）开，不因没有集合而失败。
  it("falls back to the primary root before any roots are loaded", () => {
    s().openInCurrentRoot(3);
    expect(entry(3).tabs[0]).toMatchObject({ root: "" });
  });

  // 每个终端有自己的编号：同一会话的多个标签默认标题不重名。
  it("numbers terminals within a session and keeps sessions apart", () => {
    s().openInCurrentRoot(1);
    s().openInCurrentRoot(1);
    s().openInCurrentRoot(2);
    expect(entry(1).tabs.map((t) => t.seq)).toEqual([1, 2]);
    expect(entry(2).tabs.map((t) => t.seq)).toEqual([1]);
  });
});

describe("session terminals · attach a running process", () => {
  // Given 转录卡片上有一个在跑的进程, When 接入, Then 复用它的 terminalId 开成新标签并激活。
  it("adds a tab bound to the existing terminal id and activates it", () => {
    s().openInCurrentRoot(5);
    s().attach(5, { terminalId: "cmd-1", command: "npm test" });
    expect(entry(5).tabs).toHaveLength(2);
    expect(entry(5).tabs[1]).toMatchObject({
      id: "cmd-1",
      attach: true,
      command: "npm test",
    });
    expect(entry(5).activeId).toBe("cmd-1");
  });

  // 同一个进程再次接入只切到那个标签，不重复开。
  it("re-attaching the same process only activates its tab", () => {
    s().attach(5, { terminalId: "cmd-1", command: "npm test" });
    s().openInCurrentRoot(5);
    s().attach(5, { terminalId: "cmd-1", command: "npm test" });
    expect(entry(5).tabs.filter((t) => t.id === "cmd-1")).toHaveLength(1);
    expect(entry(5).activeId).toBe("cmd-1");
  });
});

describe("session terminals · close and activate", () => {
  // Given 三个标签、中间的是当前, When 关掉它, Then 激活相邻的一个。
  it("activates a neighbour when the active tab closes", () => {
    const a = s().openInCurrentRoot(1);
    const b = s().openInCurrentRoot(1);
    const c = s().openInCurrentRoot(1);
    s().setActive(1, b);
    s().close(1, b);
    expect(entry(1).tabs.map((t) => t.id)).toEqual([a, c]);
    expect(entry(1).activeId).toBe(c);
  });

  // 关掉最后一个标签后没有当前标签。
  it("leaves no active tab when the last one closes", () => {
    const a = s().openInCurrentRoot(1);
    s().close(1, a);
    expect(entry(1).tabs).toEqual([]);
    expect(entry(1).activeId).toBeNull();
  });

  // 关一个从没开过的标签是无害的。
  it("ignores closing an unknown tab", () => {
    s().openInCurrentRoot(1);
    s().close(1, "nope");
    expect(entry(1).tabs).toHaveLength(1);
  });
});

describe("session terminals · live process detection", () => {
  // Given 一个正在跑的终端, Then 该会话「有存活终端」；别的会话没有。
  it("reports a live terminal only for the session that owns it", () => {
    s().openInCurrentRoot(1);
    expect(hasLiveSessionTerminal(s().bySession[1])).toBe(true);
    expect(hasLiveSessionTerminal(s().bySession[2])).toBe(false);
  });

  // Given 终端进程已退出（断线）但标签还留着看输出, Then 不再算存活。
  it("stops counting a terminal once its process exited", () => {
    const id = s().openInCurrentRoot(1);
    s().markExited(1, id);
    expect(hasLiveSessionTerminal(s().bySession[1])).toBe(false);
  });

  // 一个退出、一个仍在跑：仍算有存活终端。
  it("stays live while any terminal still runs", () => {
    const a = s().openInCurrentRoot(1);
    s().openInCurrentRoot(1);
    s().markExited(1, a);
    expect(hasLiveSessionTerminal(s().bySession[1])).toBe(true);
  });
});
