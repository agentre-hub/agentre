import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  invalidateProjectTreeCache,
  reloadProjectTreeCache,
  useProjectTree,
  __resetProjectTreeForTesting,
} from "../use-project-tree";

vi.mock("../../../wailsjs/go/app/App", () => ({
  ProjectListTree: vi.fn(),
}));

import { ProjectListTree } from "../../../wailsjs/go/app/App";

beforeEach(() => {
  __resetProjectTreeForTesting();
  (ProjectListTree as ReturnType<typeof vi.fn>).mockReset();
});

describe("useProjectTree", () => {
  it("首次 mount 拉取 tree, 缓存后多个 hook 共享同一份", async () => {
    (ProjectListTree as ReturnType<typeof vi.fn>).mockResolvedValue([
      { project: { id: 1, name: "Agentre" }, children: [] },
    ]);
    const { result: r1 } = renderHook(() => useProjectTree());
    await waitFor(() => {
      expect(r1.current.tree).toHaveLength(1);
    });
    const { result: r2 } = renderHook(() => useProjectTree());
    expect(r2.current.tree).toBe(r1.current.tree);
    expect(ProjectListTree).toHaveBeenCalledTimes(1);
  });

  it("invalidate() 重拉", async () => {
    (ProjectListTree as ReturnType<typeof vi.fn>).mockResolvedValue([]);
    const { result } = renderHook(() => useProjectTree());
    await waitFor(() => expect(result.current.tree).toEqual([]));
    result.current.invalidate();
    await waitFor(() => expect(ProjectListTree).toHaveBeenCalledTimes(2));
  });

  it("外部 invalidateProjectTreeCache() 会更新已挂载的 hook", async () => {
    (ProjectListTree as ReturnType<typeof vi.fn>)
      .mockResolvedValueOnce([
        { project: { id: 1, name: "Agentre", color: "agent-1" }, children: [] },
      ])
      .mockResolvedValueOnce([
        { project: { id: 1, name: "Agentre", color: "agent-2" }, children: [] },
      ]);

    const { result } = renderHook(() => useProjectTree());
    await waitFor(() => {
      expect(result.current.tree[0]?.project?.color).toBe("agent-1");
    });

    invalidateProjectTreeCache();

    await waitFor(() => {
      expect(result.current.tree[0]?.project?.color).toBe("agent-2");
    });
    expect(ProjectListTree).toHaveBeenCalledTimes(2);
  });
});

// ── 重拉期间不清空（侧栏在每轮对话落定时都会走这条路）──────────────────────
//
// reloadSidebarSources() 在 turn 起手 / 落定各调一次；它前两步同步写了
// chat-agents-store / session-index-store，索引页因此必然在同一个 tick 重渲染一次。
// 项目树若在发 RPC 前先把自己清空，那一帧读到的就是空树 —— 默认的项目轴整块塌成
// 只剩「随手对话」，等 RPC 回来才恢复，看起来就是「列表每轮都重载一遍」。
describe("project tree refresh", () => {
  function deferred<T>() {
    let resolve!: (value: T) => void;
    let reject!: (reason: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
      resolve = res;
      reject = rej;
    });
    return { promise, resolve, reject };
  }

  const treeOf = (name: string) => [{ project: { id: 1, name }, children: [] }];

  async function loadedTree(initial = "P1") {
    (ProjectListTree as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
      treeOf(initial),
    );
    const view = renderHook(() => useProjectTree());
    await waitFor(() => expect(view.result.current.tree).toHaveLength(1));
    return view;
  }

  it("Given a loaded tree, When a sidebar refresh refetches it, Then the old tree stays readable until the new one arrives", async () => {
    const { result, rerender } = await loadedTree("P1");
    const pending = deferred<unknown>();
    (ProjectListTree as ReturnType<typeof vi.fn>).mockReturnValueOnce(
      pending.promise,
    );

    act(() => {
      void reloadProjectTreeCache();
    });
    rerender();

    expect(result.current.tree).toHaveLength(1);
    expect(result.current.loaded).toBe(true);

    await act(async () => {
      pending.resolve(treeOf("P1 renamed"));
      await pending.promise;
    });
    expect(result.current.tree[0]?.project?.name).toBe("P1 renamed");
  });

  it("Given a refetch in flight, When the sidebar refreshes again, Then the in-flight request is reused instead of a second RPC", async () => {
    await loadedTree();
    const pending = deferred<unknown>();
    (ProjectListTree as ReturnType<typeof vi.fn>).mockReturnValueOnce(
      pending.promise,
    );

    act(() => {
      void reloadProjectTreeCache();
      void reloadProjectTreeCache();
    });

    // 首屏 1 次 + 重拉 1 次；第二次重拉复用在飞的那条。
    expect(ProjectListTree).toHaveBeenCalledTimes(2);
    await act(async () => {
      pending.resolve(treeOf("P1"));
      await pending.promise;
    });
  });

  it("Given a project was just mutated, When invalidate runs while a refresh is in flight, Then a fresh RPC is issued instead of reusing the stale one", async () => {
    const { result } = await loadedTree("P1");
    const stale = deferred<unknown>();
    (ProjectListTree as ReturnType<typeof vi.fn>)
      .mockReturnValueOnce(stale.promise)
      .mockResolvedValueOnce(treeOf("P1 renamed"));

    act(() => {
      void reloadProjectTreeCache(); // 顺手校准，发出时项目还没改名
    });
    invalidateProjectTreeCache(); // 改名 RPC 已返回，必须看到改后的树

    expect(ProjectListTree).toHaveBeenCalledTimes(3);
    await act(async () => {
      stale.resolve(treeOf("P1"));
      await stale.promise;
    });
    await waitFor(() =>
      expect(result.current.tree[0]?.project?.name).toBe("P1 renamed"),
    );
  });

  it("Given a refetch rejects, When it settles, Then the previously loaded tree is kept instead of blanking the sidebar", async () => {
    const { result } = await loadedTree("P1");
    (ProjectListTree as ReturnType<typeof vi.fn>).mockRejectedValueOnce(
      new Error("boom"),
    );

    await act(async () => {
      await reloadProjectTreeCache();
    });

    expect(result.current.tree).toHaveLength(1);
    expect(result.current.loaded).toBe(true);
  });

  it("Given the refetched tree is unchanged, When it lands, Then subscribers are not re-rendered at all", async () => {
    const { result } = await loadedTree("P1");
    const before = result.current.tree;
    (ProjectListTree as ReturnType<typeof vi.fn>).mockResolvedValueOnce(
      treeOf("P1"),
    );

    await act(async () => {
      await reloadProjectTreeCache();
    });

    // 引用不变 = 索引页 / 面包屑 / tab 视图这一轮完全不动。项目树绝大多数轮次
    // 一个字节都没变, 换新数组就是每轮白重渲一次整个左栏。
    expect(result.current.tree).toBe(before);
  });

  it("Given two overlapping fetches, When the older one resolves last, Then it does not overwrite the newer tree", async () => {
    const { result } = await loadedTree("P1");
    const older = deferred<unknown>();
    const newer = deferred<unknown>();
    (ProjectListTree as ReturnType<typeof vi.fn>)
      .mockReturnValueOnce(older.promise)
      .mockReturnValueOnce(newer.promise);

    act(() => {
      invalidateProjectTreeCache();
      invalidateProjectTreeCache();
    });

    await act(async () => {
      newer.resolve(treeOf("newer"));
      await newer.promise;
      older.resolve(treeOf("older"));
      await older.promise;
    });

    expect(result.current.tree[0]?.project?.name).toBe("newer");
  });
});
