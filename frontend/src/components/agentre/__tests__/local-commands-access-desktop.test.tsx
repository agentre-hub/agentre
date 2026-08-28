import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useLocalCommandsStore } from "@/stores/local-commands-store";

import { desktopLocalCommandsAccess } from "../local-commands-access-desktop";

/**
 * 桌面端本地命令接缝的适配层用例。
 *
 * 重点不是"能不能读到条目"，而是**输出流不得经过反应式读**：卡片的反应式投影
 * 刻意不含 `output`（见包里 `local-command/access.tsx`），所以一条长跑命令
 * 每追加一段输出，这个 hook 都必须返回同一个引用 —— 否则整张卡片每 chunk
 * 重渲染一次，而那在桌面端只表现为"有点卡"，没有任何机械信号。
 */
function startCommand(id: string, command = "go test") {
  useLocalCommandsStore
    .getState()
    .start({ id, sessionId: 1, command, createdAt: 1000 });
}

beforeEach(() => {
  useLocalCommandsStore.setState({ entries: {} });
});

describe("desktopLocalCommandsAccess", () => {
  it("Given a store entry, When the reactive read runs, Then it projects everything except the streaming output", () => {
    startCommand("t1");
    useLocalCommandsStore.getState().appendOutput("t1", "=== RUN\n");

    const { result } = renderHook(() =>
      desktopLocalCommandsAccess.useLocalCommand("t1"),
    );

    expect(result.current).toEqual({
      id: "t1",
      sessionId: 1,
      command: "go test",
      createdAt: 1000,
      status: "running",
      exitCode: undefined,
      finishedAt: undefined,
      expanded: undefined,
      hasOutput: true,
    });
    expect(result.current).not.toHaveProperty("output");
  });

  it("Given a missing entry, When the reactive read runs, Then it reports undefined so the card renders nothing", () => {
    const { result } = renderHook(() =>
      desktopLocalCommandsAccess.useLocalCommand("nope"),
    );

    expect(result.current).toBeUndefined();
  });

  it("Given a long-running command, When output keeps arriving, Then the projection keeps its identity and the reader does not re-render", () => {
    startCommand("t2");
    let renders = 0;
    const { result } = renderHook(() => {
      renders += 1;
      return desktopLocalCommandsAccess.useLocalCommand("t2");
    });
    // 首段输出把 hasOutput 从 false 翻成 true —— 只发生一次,不随 chunk 数增长。
    act(() => useLocalCommandsStore.getState().appendOutput("t2", "boot\n"));
    const rendersBeforeStreaming = renders;
    const viewBeforeStreaming = result.current;

    for (let i = 0; i < 32; i += 1) {
      act(() => useLocalCommandsStore.getState().appendOutput("t2", `${i}\n`));
    }

    expect(renders).toBe(rendersBeforeStreaming);
    expect(result.current).toBe(viewBeforeStreaming);
  });

  it("Given the command finishes, When the status changes, Then the reactive read does re-render with the new projection", () => {
    startCommand("t3");
    const { result } = renderHook(() =>
      desktopLocalCommandsAccess.useLocalCommand("t3"),
    );

    act(() => useLocalCommandsStore.getState().finish("t3", "failed", 2));

    expect(result.current?.status).toBe("failed");
    expect(result.current?.exitCode).toBe(2);
  });

  it("Given a backlog of output, When a view subscribes, Then it is seeded once and afterwards receives only deltas", () => {
    startCommand("t4");
    useLocalCommandsStore.getState().appendOutput("t4", "boot\n");
    const onAppend = vi.fn();

    const unsubscribe = desktopLocalCommandsAccess.subscribeOutput(
      "t4",
      onAppend,
    );

    expect(onAppend).toHaveBeenCalledTimes(1);
    expect(onAppend).toHaveBeenCalledWith("boot\n");

    useLocalCommandsStore.getState().appendOutput("t4", "more\n");

    expect(onAppend).toHaveBeenCalledTimes(2);
    expect(onAppend).toHaveBeenLastCalledWith("more\n");

    unsubscribe();
    useLocalCommandsStore.getState().appendOutput("t4", "after\n");

    expect(onAppend).toHaveBeenCalledTimes(2);
  });

  it("Given two views on the same command, When one unsubscribes, Then the other keeps receiving its own deltas from its own cursor", () => {
    startCommand("t5");
    useLocalCommandsStore.getState().appendOutput("t5", "boot\n");
    const first = vi.fn();
    const second = vi.fn();

    const unsubscribeFirst = desktopLocalCommandsAccess.subscribeOutput(
      "t5",
      first,
    );
    const unsubscribeSecond = desktopLocalCommandsAccess.subscribeOutput(
      "t5",
      second,
    );

    // 各自从头 seed:两个视图都要看到完整内容,游标各归各。
    expect(first).toHaveBeenCalledWith("boot\n");
    expect(second).toHaveBeenCalledWith("boot\n");

    unsubscribeFirst();
    useLocalCommandsStore.getState().appendOutput("t5", "more\n");

    expect(first).toHaveBeenCalledTimes(1);
    expect(second).toHaveBeenLastCalledWith("more\n");
    unsubscribeSecond();
  });

  it("Given a non-output store change, When subscribers run, Then nothing is re-delivered", () => {
    startCommand("t6");
    const onAppend = vi.fn();
    const unsubscribe = desktopLocalCommandsAccess.subscribeOutput(
      "t6",
      onAppend,
    );

    useLocalCommandsStore.getState().finish("t6", "done", 0);

    expect(onAppend).not.toHaveBeenCalled();
    unsubscribe();
  });

  it("Given the user collapses or dismisses a card, When the write actions run, Then they land on the host store", () => {
    startCommand("t7");
    useLocalCommandsStore.getState().finish("t7", "done", 0);

    // finished → 默认折叠;toggle 一次应当展开。
    desktopLocalCommandsAccess.toggleExpanded("t7");
    expect(useLocalCommandsStore.getState().get("t7")?.expanded).toBe(true);

    desktopLocalCommandsAccess.remove("t7");
    expect(useLocalCommandsStore.getState().get("t7")).toBeUndefined();
  });
});
