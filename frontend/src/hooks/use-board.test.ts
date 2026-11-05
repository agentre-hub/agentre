import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const toastMock = vi.hoisted(() =>
  Object.assign(
    vi.fn(() => "toast-1"),
    { dismiss: vi.fn() },
  ),
);
vi.mock("sonner", () => ({ toast: toastMock }));

vi.mock("../../wailsjs/go/app/App", () => ({
  IssueList: vi.fn(),
  IssueListLabels: vi.fn(),
  IssueMove: vi.fn(),
  IssueCreate: vi.fn(),
  IssueUpdate: vi.fn(),
  IssueDelete: vi.fn(),
  IssueCreateLabel: vi.fn(),
  IssueUpdateLabel: vi.fn(),
  IssueDeleteLabel: vi.fn(),
}));

import { EMPTY_BOARD_QUERY, type BoardQuery } from "@agentre-hub/agentre-ui";

import {
  IssueCreate,
  IssueCreateLabel,
  IssueDelete,
  IssueDeleteLabel,
  IssueList,
  IssueListLabels,
  IssueMove,
  IssueUpdate,
  IssueUpdateLabel,
} from "../../wailsjs/go/app/App";
import { useBoard } from "./use-board";

const issueList = IssueList as ReturnType<typeof vi.fn>;
const issueListLabels = IssueListLabels as ReturnType<typeof vi.fn>;

function query(patch: Partial<BoardQuery> = {}): BoardQuery {
  return { ...EMPTY_BOARD_QUERY, ...patch };
}

/** 一个能从外面决定何时兑现的 promise：两次取数的返回顺序要由测试自己排。 */
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function boardResponse(cards: { id: number; stage: string }[]) {
  return {
    issues: cards.map((card) => ({
      id: card.id,
      projectID: 0,
      title: `card-${card.id}`,
      body: "",
      stage: card.stage,
      position: 0,
      updatetime: 0,
      labels: [],
    })),
    stageCounts: {},
    stageTotals: {},
    projectCounts: [],
  };
}

describe("useBoard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    issueList.mockResolvedValue({
      issues: [
        {
          id: 1,
          projectID: 4,
          title: "demo",
          body: "",
          stage: "doing",
          position: 10,
          updatetime: 0,
          labels: [],
        },
      ],
      stageCounts: { doing: 1 },
      stageTotals: { doing: 3 },
      projectCounts: [
        { projectID: 0, count: 2 },
        { projectID: 4, count: 6 },
      ],
    });
    issueListLabels.mockResolvedValue([
      { id: 1, name: "bug", tone: "red", usageCount: 4 },
    ]);
  });

  it("Given a mount, When the board loads, Then columns, labels and subtree counts all arrive", async () => {
    const { result } = renderHook(() => useBoard(query(), () => null));

    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));
    expect(result.current.viewModel.columns.doing?.cards).toHaveLength(1);
    expect(result.current.viewModel.columns.doing?.total).toBe(3);
    expect(result.current.labels[0].usageCount).toBe(4);
    expect(result.current.unassignedCount).toBe(2);
    expect(result.current.matchedCount).toBe(1);
  });

  it("Given a scope change, When the board reloads, Then the request carries the new scope", async () => {
    const { rerender, result } = renderHook(
      ({ q }: { q: BoardQuery }) => useBoard(q, () => null),
      { initialProps: { q: query() } },
    );
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    rerender({ q: query({ scope: { kind: "project", projectId: 4 } }) });

    await waitFor(() =>
      expect(issueList).toHaveBeenLastCalledWith(
        expect.objectContaining({ scope: "project", projectID: 4 }),
      ),
    );
  });

  it("Given each scope, When the board builds its view model, Then the empty state names only the scopes it can pin down", async () => {
    const projectOf = (id: number) =>
      id === 4 ? { name: "Apollo", color: "agent-1" } : null;
    const { rerender, result } = renderHook(
      ({ q }: { q: BoardQuery }) => useBoard(q, projectOf),
      { initialProps: { q: query() } },
    );
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    // 全部项目：没有可点名的范围 —— 退化为泛化文案，而不是编一个名字。
    expect(result.current.viewModel.emptyScope).toBeUndefined();

    rerender({ q: query({ scope: { kind: "unassigned" } }) });
    await waitFor(() =>
      expect(result.current.viewModel.emptyScope).toEqual({
        kind: "unassigned",
      }),
    );

    rerender({ q: query({ scope: { kind: "project", projectId: 4 } }) });
    await waitFor(() =>
      expect(result.current.viewModel.emptyScope).toEqual({
        kind: "project",
        name: "Apollo",
      }),
    );
  });

  it("Given an unchanged query object identity, When the host re-renders, Then no second request is fired", async () => {
    const { rerender, result } = renderHook(
      ({ q }: { q: BoardQuery }) => useBoard(q, () => null),
      { initialProps: { q: query() } },
    );
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    rerender({ q: query() });
    await waitFor(() => expect(result.current.searching).toBe(false));

    expect(issueList).toHaveBeenCalledTimes(1);
  });

  it("Given a failing list call, When the board loads, Then the reason is surfaced instead of thrown", async () => {
    issueList.mockRejectedValue(new Error("boom"));
    const { result } = renderHook(() => useBoard(query(), () => null));

    await waitFor(() =>
      expect(result.current.error).toEqual({
        kind: "unknown",
        title: "The board could not be loaded",
        message: "",
        detail: "boom",
      }),
    );
  });

  // 连打几个字会让多次取数重叠：先发的那一次晚返回时不能把新结果盖回旧的。这是
  // 这支 hook 里最微妙的一段（requestRef 那三处比较），删掉它整套用例照样绿。
  it("Given two overlapping list calls, When the older one resolves last, Then the newer response still wins", async () => {
    const older = deferred<unknown>();
    const newer = deferred<unknown>();
    issueList
      .mockImplementationOnce(() => older.promise)
      .mockImplementationOnce(() => newer.promise);

    const { rerender, result } = renderHook(
      ({ q }: { q: BoardQuery }) => useBoard(q, () => null),
      { initialProps: { q: query() } },
    );
    rerender({ q: query({ keyword: "gate" }) });
    await waitFor(() => expect(issueList).toHaveBeenCalledTimes(2));

    newer.resolve(boardResponse([{ id: 2, stage: "review" }]));
    await waitFor(() =>
      expect(result.current.viewModel.columns.review?.cards).toHaveLength(1),
    );

    // 迟到的那一次带着完全不同的结果，它一个字都不许写进去。
    older.resolve(boardResponse([{ id: 1, stage: "todo" }]));
    await waitFor(() => expect(result.current.searching).toBe(false));

    expect(result.current.viewModel.columns.review?.cards).toHaveLength(1);
    expect(result.current.viewModel.columns.todo?.cards ?? []).toHaveLength(0);
  });

  // 迟到的那一次**失败**同样不许写：旧请求的报错盖在新结果上，用户看到的是一块
  // 好好的板配一句解释不了的错。
  it("Given a stale list call, When it rejects last, Then the error is not shown", async () => {
    const older = deferred<unknown>();
    const newer = deferred<unknown>();
    issueList
      .mockImplementationOnce(() => older.promise)
      .mockImplementationOnce(() => newer.promise);

    const { rerender, result } = renderHook(
      ({ q }: { q: BoardQuery }) => useBoard(q, () => null),
      { initialProps: { q: query() } },
    );
    rerender({ q: query({ keyword: "gate" }) });
    await waitFor(() => expect(issueList).toHaveBeenCalledTimes(2));

    newer.resolve(boardResponse([{ id: 2, stage: "review" }]));
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    older.reject(new Error("stale boom"));
    // 等那次拒绝真的走完 hook 里的 catch/finally，再问它写没写进去。
    await older.promise.catch(() => undefined);
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(result.current.error).toBeNull();
  });

  it("Given a card dropped in another column, When it is moved, Then the write lands and the board refetches", async () => {
    const { result } = renderHook(() => useBoard(query(), () => null));
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    await result.current.moveIssue(1, "review", 0);

    expect(IssueMove).toHaveBeenCalledWith({
      id: 1,
      stage: "review",
      afterID: 0,
    });
    await waitFor(() => expect(issueList).toHaveBeenCalledTimes(2));
  });

  it("Given a new task, When it is saved, Then all three execution fields ride along", async () => {
    const { result } = renderHook(() => useBoard(query(), () => null));
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    await result.current.saveTask({
      title: "fix OAuth",
      description: "body",
      stage: "doing",
      projectId: 4,
      labelIds: [1],
      assigneeAgentId: 7,
      agentBackendId: 9,
      llmProviderKey: "prov",
      llmModelKey: "model",
    });

    expect(IssueCreate).toHaveBeenCalledWith({
      projectID: 4,
      title: "fix OAuth",
      body: "body",
      labelIDs: [1],
      stage: "doing",
      assigneeAgentID: 7,
      agentBackendID: 9,
      llmProviderKey: "prov",
      llmModelKey: "model",
    });
  });

  it("Given an existing task, When it is saved, Then the update carries its id and stage", async () => {
    const { result } = renderHook(() => useBoard(query(), () => null));
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    await result.current.saveTask({
      id: 12,
      title: "edited",
      description: "",
      stage: "review",
      projectId: null,
      labelIds: [],
      assigneeAgentId: null,
      agentBackendId: null,
      llmProviderKey: "",
      llmModelKey: "",
    });

    expect(IssueUpdate).toHaveBeenCalledWith(
      expect.objectContaining({ id: 12, stage: "review", projectID: 0 }),
    );
  });

  it("Given a label mutation, When each kind is dispatched, Then it reaches its own binding", async () => {
    const { result } = renderHook(() => useBoard(query(), () => null));
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    await result.current.mutateLabel({
      kind: "create",
      name: "ui",
      tone: "blue",
    });
    await result.current.mutateLabel({
      kind: "update",
      id: 3,
      name: "ux",
      tone: "violet",
    });
    await result.current.mutateLabel({ kind: "delete", id: 3 });

    expect(IssueCreateLabel).toHaveBeenCalledWith({
      id: 0,
      name: "ui",
      tone: "blue",
    });
    expect(IssueUpdateLabel).toHaveBeenCalledWith({
      id: 3,
      name: "ux",
      tone: "violet",
    });
    expect(IssueDeleteLabel).toHaveBeenCalledWith(3);
  });

  // 写路径**一次都不 reject**：`void board.deleteTask(id)` 这一类调用接不住拒绝，
  // 卡片会弹回原位而用户什么都看不到。失败就地进 error，并回 false。
  it("Given a failing write, When it is dispatched, Then it resolves false and the reason is surfaced", async () => {
    const { result } = renderHook(() => useBoard(query(), () => null));
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    (IssueDelete as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error("row is gone"),
    );
    vi.useFakeTimers();
    await expect(result.current.deleteTask(1)).resolves.toBe(true);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    vi.useRealTimers();
    await waitFor(() =>
      expect(result.current.error).toEqual({
        kind: "unknown",
        title: "That change was not saved",
        message: "",
        detail: "row is gone",
      }),
    );
    // 写没过去就不该重拉：拉回来的还是同一块板，只会把报错洗掉。
    expect(issueList).toHaveBeenCalledTimes(1);

    (IssueMove as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error("move failed"),
    );
    await expect(result.current.moveIssue(1, "todo", 0)).resolves.toBe(false);
    await waitFor(() =>
      expect(result.current.error).toEqual({
        kind: "unknown",
        title: "That change was not saved",
        message: "",
        detail: "move failed",
      }),
    );

    (IssueCreateLabel as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error("name taken"),
    );
    await expect(
      result.current.mutateLabel({ kind: "create", name: "bug", tone: "red" }),
    ).resolves.toBe(false);
    await waitFor(() =>
      expect(result.current.error).toEqual({
        kind: "unknown",
        title: "That change was not saved",
        message: "",
        detail: "name taken",
      }),
    );
  });

  // S5：写失败按码分类 —— 已经不在了 / 连不上各说各的中文事实，机器原话不当标题。
  it("Given a write rejected with a coded not-found error, When it settles, Then the error carries kind notFound and the backend sentence", async () => {
    const { result } = renderHook(() => useBoard(query(), () => null));
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    (IssueMove as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      "agentre-code:18200 任务不存在",
    );
    await expect(result.current.moveIssue(1, "todo", 0)).resolves.toBe(false);
    await waitFor(() =>
      expect(result.current.error).toEqual({
        kind: "notFound",
        title:
          "This task no longer exists. Refresh to bring the board up to date",
        message: "任务不存在",
        detail: undefined,
      }),
    );
  });

  it("Given a write rejected with a coded offline error, When it settles, Then the error carries kind offline", async () => {
    const { result } = renderHook(() => useBoard(query(), () => null));
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    (IssueMove as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error("agentre-code:20301 服务器不可达"),
    );
    await result.current.moveIssue(1, "todo", 0);
    await waitFor(() =>
      expect(result.current.error).toMatchObject({
        kind: "offline",
        title:
          "Can't reach the service, so the change was not saved. Try again later",
      }),
    );
  });

  it("Given a write that goes through, When it settles, Then it resolves true and clears an earlier error", async () => {
    const { result } = renderHook(() => useBoard(query(), () => null));
    await waitFor(() => expect(result.current.viewModel.loading).toBe(false));

    (IssueDelete as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error("row is gone"),
    );
    vi.useFakeTimers();
    await result.current.deleteTask(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    vi.useRealTimers();
    await waitFor(() =>
      expect(result.current.error).toEqual({
        kind: "unknown",
        title: "That change was not saved",
        message: "",
        detail: "row is gone",
      }),
    );

    vi.useFakeTimers();
    await result.current.deleteTask(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000);
    });
    vi.useRealTimers();
    await waitFor(() => expect(result.current.error).toBeNull());
    expect(issueList).toHaveBeenCalledTimes(2);
  });

  describe("deferred delete", () => {
    const cardIDs = (result: { current: ReturnType<typeof useBoard> }) =>
      Object.values(result.current.viewModel.columns).flatMap((column) =>
        (column?.cards ?? []).map((card) => card.id),
      );

    async function setup() {
      issueList.mockResolvedValue(
        boardResponse([
          { id: 1, stage: "todo" },
          { id: 2, stage: "todo" },
        ]),
      );
      const hook = renderHook(() => useBoard(query(), () => null));
      await waitFor(() =>
        expect(hook.result.current.viewModel.loading).toBe(false),
      );
      vi.useFakeTimers();
      return hook;
    }

    afterEach(() => {
      vi.useRealTimers();
    });

    it("Given a delete, When it is requested, Then the card hides at once with an undo toast and IssueDelete waits ~6s", async () => {
      const { result } = await setup();

      let ok: boolean | undefined;
      await act(async () => {
        ok = await result.current.deleteTask(1);
      });

      expect(ok).toBe(true);
      expect(cardIDs(result)).toEqual([2]);
      expect(toastMock).toHaveBeenCalledTimes(1);
      const [, options] = toastMock.mock.calls[0] as unknown as [
        string,
        { action: { label: string; onClick: () => void } },
      ];
      expect(options.action.onClick).toBeTypeOf("function");

      await act(async () => {
        await vi.advanceTimersByTimeAsync(5900);
      });
      expect(IssueDelete).not.toHaveBeenCalled();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(200);
      });
      expect(IssueDelete).toHaveBeenCalledTimes(1);
      expect(IssueDelete).toHaveBeenCalledWith(1);
    });

    it("Given a pending delete, When undo is clicked, Then the card returns and IssueDelete never fires", async () => {
      const { result } = await setup();
      await act(async () => {
        await result.current.deleteTask(1);
      });
      const [, options] = toastMock.mock.calls[0] as unknown as [
        string,
        { action: { onClick: () => void } },
      ];

      act(() => options.action.onClick());
      expect(cardIDs(result).sort()).toEqual([1, 2]);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(10000);
      });
      expect(IssueDelete).not.toHaveBeenCalled();
    });

    it("Given two deletes in a row, When the first is undone, Then only the second is committed", async () => {
      const { result } = await setup();
      await act(async () => {
        await result.current.deleteTask(1);
        await result.current.deleteTask(2);
      });
      expect(cardIDs(result)).toEqual([]);
      const [, first] = toastMock.mock.calls[0] as unknown as [
        string,
        { action: { onClick: () => void } },
      ];
      act(() => first.action.onClick());
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(IssueDelete).toHaveBeenCalledTimes(1);
      expect(IssueDelete).toHaveBeenCalledWith(2);
    });

    it("Given a pending delete, When the host unmounts, Then it commits at once and the timer does not fire twice", async () => {
      const { result, unmount } = await setup();
      await act(async () => {
        await result.current.deleteTask(1);
      });
      expect(IssueDelete).not.toHaveBeenCalled();

      unmount();
      expect(IssueDelete).toHaveBeenCalledWith(1);
      await vi.advanceTimersByTimeAsync(10000);
      expect(IssueDelete).toHaveBeenCalledTimes(1);
    });

    it("Given a delete that fails after the delay, When it settles, Then the card returns and the error is surfaced", async () => {
      const { result } = await setup();
      (IssueDelete as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
        new Error("row is gone"),
      );
      await act(async () => {
        await result.current.deleteTask(1);
        await vi.advanceTimersByTimeAsync(6000);
      });
      expect(result.current.error).toMatchObject({ detail: "row is gone" });
      expect(cardIDs(result).sort()).toEqual([1, 2]);
    });

    it("Given a committed delete, When undo is clicked late, Then nothing is restored or re-sent", async () => {
      const { result } = await setup();
      await act(async () => {
        await result.current.deleteTask(1);
        await vi.advanceTimersByTimeAsync(6000);
      });
      const [, options] = toastMock.mock.calls[0] as unknown as [
        string,
        { action: { onClick: () => void } },
      ];
      act(() => options.action.onClick());
      expect(IssueDelete).toHaveBeenCalledTimes(1);
    });
  });
});
