import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";

import {
  SESSION_ROOT_NOTICE_MS,
  useSessionRoot,
  useSessionRootStore,
  type WorkRoot,
} from "../session-root-store";

const CWD = "/work/demo-app";
const WT = "/work/demo-app-dark-mode";
const WT2 = "/work/demo-app-ia";

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

/** 主仓库 + 一个 worktree，AI 在 `ai` 指向的那个根。 */
function rootsWithAI(ai: "main" | "wt" | "none" = "main"): WorkRoot[] {
  return [
    root(CWD, "demo-app", { isPrimary: true, aiHere: ai === "main" }),
    root(WT, "dark-mode", { isWorktree: true, aiHere: ai === "wt" }),
  ];
}

const s = () => useSessionRootStore.getState();
const apply = (sessionId: number, roots: WorkRoot[], cwd = CWD) =>
  act(() => s().applyRoots(sessionId, cwd, roots));

beforeEach(() => {
  useSessionRootStore.setState({ bySession: {} });
});

describe("useSessionRoot · 默认与规范形", () => {
  // Given 还没拿到任何工作根, When 读会话当前根, Then 跟随默认开启、当前根就是会话
  // cwd，而交给绑定与预览标签的根实参是空串（主根的唯一表示）。
  it("defaults to following, rooted at the session cwd with the canonical primary arg", () => {
    act(() => s().applyRoots(7, CWD, []));
    const { result } = renderHook(() => useSessionRoot(7));
    expect(result.current.follow).toBe(true);
    expect(result.current.current).toBe(CWD);
    expect(result.current.rootArg).toBe("");
  });

  // Given 一个从没出现过的会话, Then 也有一份可用的默认值（跟随开、没有根）。
  it("gives an unknown session a following, empty default", () => {
    const { result } = renderHook(() => useSessionRoot(99));
    expect(result.current.follow).toBe(true);
    expect(result.current.current).toBe("");
    expect(result.current.rootArg).toBe("");
    expect(result.current.roots).toEqual([]);
    expect(result.current.notice).toBeNull();
  });

  // Given 主根的路径与会话 cwd 字面不同（尾斜杠）, When 当前根是主根, Then 根实参
  // 仍是空串——主根只有一种表示，不会因为字面差异变成绝对路径。
  it("keeps the primary root canonical as '' even when its path differs from the cwd string", () => {
    apply(7, [root(`${CWD}/`, "demo-app", { isPrimary: true, aiHere: true })]);
    const { result } = renderHook(() => useSessionRoot(7));
    expect(result.current.rootArg).toBe("");
    expect(result.current.current).toBe(`${CWD}/`);
  });

  // Given 当前根是 worktree, Then 根实参是它的绝对路径。
  it("uses the absolute path as the arg of a non-primary root", () => {
    apply(7, rootsWithAI("wt"));
    const { result } = renderHook(() => useSessionRoot(7));
    expect(result.current.rootArg).toBe(WT);
    expect(result.current.currentRoot?.name).toBe("dark-mode");
  });
});

describe("useSessionRoot · 跟随 AI", () => {
  // Given 会话（重启后）第一次拿到工作根、AI 在 worktree 里, Then 当前根直接就是
  // 那个 worktree，且不弹「已跟随」（这不是一次切换）。
  it("starts at the AI's root on the first root set without a follow notice", () => {
    apply(7, rootsWithAI("wt"));
    const { result } = renderHook(() => useSessionRoot(7));
    expect(result.current.current).toBe(WT);
    expect(result.current.notice).toBeNull();
  });

  // Given 跟随开启、当前在主仓库, When chat:workroots 推来 AI 进了 worktree,
  // Then 当前根切过去，并给出一条记着「原根」的已跟随提示。
  it("switches to the AI's new root mid-turn and notices where it came from", () => {
    apply(7, rootsWithAI("main"));
    apply(7, rootsWithAI("wt"));
    const { result } = renderHook(() => useSessionRoot(7));
    expect(result.current.current).toBe(WT);
    expect(result.current.notice).toMatchObject({
      kind: "followed",
      to: { path: WT },
      from: { path: CWD },
    });
  });

  // Given 已跟随进 worktree, When AI 回到主仓库, Then 同样跟回去。
  it("follows the AI back to the main repo too", () => {
    apply(7, rootsWithAI("wt"));
    apply(7, rootsWithAI("main"));
    const { result } = renderHook(() => useSessionRoot(7));
    expect(result.current.current).toBe(CWD);
    expect(result.current.rootArg).toBe("");
    expect(result.current.notice).toMatchObject({
      kind: "followed",
      to: { path: CWD },
      from: { path: WT },
    });
  });

  // Given 推来的集合里 AI 不在任何根, Then 当前根不动、也不提示。
  it("stays put when the AI is in none of the roots", () => {
    apply(7, rootsWithAI("main"));
    apply(7, rootsWithAI("none"));
    const { result } = renderHook(() => useSessionRoot(7));
    expect(result.current.current).toBe(CWD);
    expect(result.current.notice).toBeNull();
  });

  // Given 刚跟随进 worktree, When 用户点「留在 <原根>」, Then 回到原根并关闭跟随；
  // 之后 AI 再动也不跟。
  it("returns to the previous root and turns follow off on stay", () => {
    apply(7, rootsWithAI("main"));
    apply(7, rootsWithAI("wt"));
    act(() => s().stay(7));
    const { result } = renderHook(() => useSessionRoot(7));
    expect(result.current.current).toBe(CWD);
    expect(result.current.follow).toBe(false);
    expect(result.current.notice).toBeNull();

    apply(7, [
      ...rootsWithAI("none"),
      root(WT2, "ia", { isWorktree: true, aiHere: true }),
    ]);
    expect(renderHook(() => useSessionRoot(7)).result.current.current).toBe(
      CWD,
    );
  });

  // Given 跟随开启, When 用户在下拉里手动选根, Then 跟随自动关闭、当前根是所选。
  it("turns follow off on a manual pick", () => {
    apply(7, rootsWithAI("main"));
    act(() => s().select(7, WT));
    const { result } = renderHook(() => useSessionRoot(7));
    expect(result.current.current).toBe(WT);
    expect(result.current.follow).toBe(false);

    // AI 回主仓库 → 不跟。
    apply(7, rootsWithAI("main"));
    expect(renderHook(() => useSessionRoot(7)).result.current.current).toBe(WT);
  });

  // Given 手动选过根（跟随关）, When 重新打开跟随, Then 立即切到 AI 所在的根。
  it("jumps to the AI's root when follow is turned back on", () => {
    apply(7, rootsWithAI("wt"));
    act(() => s().select(7, CWD));
    act(() => s().setFollow(7, true));
    const { result } = renderHook(() => useSessionRoot(7));
    expect(result.current.follow).toBe(true);
    expect(result.current.current).toBe(WT);
  });

  // Given 已跟随的提示在显示, When 它的计时到期（按 id 收）, Then 提示消失、根不变；
  // 一条更新的提示不会被旧计时收走。
  it("dismisses only the notice whose id matches", () => {
    apply(7, rootsWithAI("main"));
    apply(7, rootsWithAI("wt"));
    const first = renderHook(() => useSessionRoot(7)).result.current.notice!;
    apply(7, rootsWithAI("main"));
    act(() => s().dismissNotice(7, first.id));
    expect(
      renderHook(() => useSessionRoot(7)).result.current.notice,
    ).not.toBeNull();

    const second = renderHook(() => useSessionRoot(7)).result.current.notice!;
    act(() => s().dismissNotice(7, second.id));
    const { result } = renderHook(() => useSessionRoot(7));
    expect(result.current.notice).toBeNull();
    expect(result.current.current).toBe(CWD);
    expect(SESSION_ROOT_NOTICE_MS).toBe(5000);
  });
});

describe("useSessionRoot · worktree 被移除", () => {
  // Given 当前根是 worktree（手动选的，跟随关）, When 它从集合里消失, Then 回到主根
  // 并提示一次；下一次同样的集合不再重复提示；它进入「已移除」清单。
  it("falls back to the primary root and notices the removal once", () => {
    apply(7, rootsWithAI("main"));
    act(() => s().select(7, WT));
    const onlyMain = [root(CWD, "demo-app", { isPrimary: true, aiHere: true })];
    apply(7, onlyMain);

    let r = renderHook(() => useSessionRoot(7)).result.current;
    expect(r.current).toBe(CWD);
    expect(r.rootArg).toBe("");
    expect(r.notice).toMatchObject({
      kind: "removed",
      removed: { name: "dark-mode" },
      primary: { name: "demo-app" },
    });
    expect(r.removed.map((x) => x.path)).toEqual([WT]);

    act(() => s().dismissNotice(7, r.notice!.id));
    apply(7, onlyMain);
    r = renderHook(() => useSessionRoot(7)).result.current;
    expect(r.notice).toBeNull();
    expect(r.removed.map((x) => x.path)).toEqual([WT]);
  });

  // Given 跟随中、当前根是 worktree, When 它被移除, Then 同样回主根并提示移除。
  it("falls back on removal while following too", () => {
    apply(7, rootsWithAI("wt"));
    apply(7, [root(CWD, "demo-app", { isPrimary: true, aiHere: true })]);
    const r = renderHook(() => useSessionRoot(7)).result.current;
    expect(r.current).toBe(CWD);
    expect(r.notice?.kind).toBe("removed");
  });

  // Given 一个不是当前根的 worktree 被移除, Then 当前根不动、不提示，但它仍进
  // 「已移除」清单（变更窗要画它的分组）；它又被认领回来时从清单里撤掉。
  it("records a non-current removed root without noticing, and forgets it once reclaimed", () => {
    apply(7, rootsWithAI("main"));
    apply(7, [root(CWD, "demo-app", { isPrimary: true, aiHere: true })]);
    let r = renderHook(() => useSessionRoot(7)).result.current;
    expect(r.notice).toBeNull();
    expect(r.removed.map((x) => x.path)).toEqual([WT]);

    apply(7, rootsWithAI("main"));
    r = renderHook(() => useSessionRoot(7)).result.current;
    expect(r.removed).toEqual([]);
  });

  // Given 后端取数在集合之后带回本进程里已移除的根（removed=true）——这一页从没见过它
  // （会话没开着时被移除）, When 落到 store, Then 它进「已移除」清单而不是集合；与集合里
  // 同路径的（取数之间又认领回来了）不算移除。
  it("takes roots the backend reports as removed into the removed list, not the set", () => {
    const reported = [
      root(CWD, "demo-app", { isPrimary: true, aiHere: true }),
      root(WT2, "ia", { isWorktree: true }),
      { ...root(WT, "dark-mode", { isWorktree: true }), removed: true },
      { ...root(WT2, "ia", { isWorktree: true }), removed: true },
    ] as WorkRoot[];
    apply(7, reported);

    const r = renderHook(() => useSessionRoot(7)).result.current;
    expect(r.roots.map((x) => x.path)).toEqual([CWD, WT2]);
    expect(r.removed.map((x) => x.path)).toEqual([WT]);
    expect(r.notice).toBeNull();
  });
});

describe("useSessionRoot · 重新加载后的已移除清单", () => {
  // Given 当前根 worktree 被移除过, When 页面重新加载（内存状态全丢）、后端集合里没有它
  // 而在集合之后报告它已移除, Then 它仍在「已移除」清单里；跟随与当前根回到默认、移除
  // 提示不再出现（只提示一次）。
  it("takes removed roots back from the backend after a reload while follow, selection and the notice reset", () => {
    apply(7, rootsWithAI("main"));
    act(() => s().select(7, WT));
    const onlyMain = [root(CWD, "demo-app", { isPrimary: true, aiHere: true })];
    apply(7, onlyMain);

    act(() => useSessionRootStore.setState({ bySession: {} }));
    act(() => s().attach(7, CWD));
    apply(7, [
      ...onlyMain,
      { ...root(WT, "dark-mode", { isWorktree: true }), removed: true },
    ] as WorkRoot[]);

    const r = renderHook(() => useSessionRoot(7)).result.current;
    expect(r.removed.map((x) => x.path)).toEqual([WT]);
    expect(r.follow).toBe(true);
    expect(r.current).toBe(CWD);
    expect(r.notice).toBeNull();
  });
});

describe("useSessionRoot · 按会话", () => {
  // Given 两个会话, When 只在一个里关跟随并选根, Then 另一个不受影响。
  it("keeps follow and the current root per session", () => {
    apply(7, rootsWithAI("main"));
    apply(8, rootsWithAI("main"));
    act(() => s().select(7, WT));
    const a = renderHook(() => useSessionRoot(7)).result.current;
    const b = renderHook(() => useSessionRoot(8)).result.current;
    expect(a.follow).toBe(false);
    expect(a.current).toBe(WT);
    expect(b.follow).toBe(true);
    expect(b.current).toBe(CWD);
  });

  // Given 驱动认下会话 cwd, Then 集合取回之前当前根就是 cwd；同一个 cwd 再认一次
  // 不会把已选的根与跟随开关清掉。
  it("attaches a session at its cwd without resetting an unchanged one", () => {
    act(() => s().attach(7, CWD));
    expect(renderHook(() => useSessionRoot(7)).result.current.current).toBe(
      CWD,
    );
    apply(7, rootsWithAI("main"));
    act(() => s().select(7, WT));
    act(() => s().attach(7, CWD));
    const r = renderHook(() => useSessionRoot(7)).result.current;
    expect(r.current).toBe(WT);
    expect(r.follow).toBe(false);
  });

  // Given 会话的工作目录变了（指定了本机路径）, Then 它的根状态从默认重新开始。
  it("restarts a session's state when its cwd changes", () => {
    apply(7, rootsWithAI("main"));
    act(() => s().select(7, WT));
    act(() => s().applyRoots(7, "/elsewhere", []));
    const r = renderHook(() => useSessionRoot(7)).result.current;
    expect(r.follow).toBe(true);
    expect(r.current).toBe("/elsewhere");
  });
});
