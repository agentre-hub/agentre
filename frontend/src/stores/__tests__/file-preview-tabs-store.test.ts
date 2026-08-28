import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { warning: vi.fn(), error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

import {
  selectActivePreviewTab,
  useFilePreviewTabsStore,
} from "../file-preview-tabs-store";

describe("file-preview-tabs-store", () => {
  beforeEach(() => {
    localStorage.clear();
    useFilePreviewTabsStore.setState({
      previewTabsBySession: {},
    });
    sonnerMocks.toast.warning.mockReset();
  });

  const store = () => useFilePreviewTabsStore.getState();
  const entry = (sessionId: number) => store().previewTabsBySession[sessionId];
  const paths = (sessionId: number) =>
    (entry(sessionId)?.tabs ?? []).map((tab) => tab.path);
  const activePath = (sessionId: number) => entry(sessionId)?.activePath;
  const tabAt = (sessionId: number, path: string) =>
    (entry(sessionId)?.tabs ?? []).find((tab) => tab.path === path);

  it("opens a single-clicked file as the one temporary tab", () => {
    store().openPreview(7, "README.md", "directory");

    expect(entry(7)?.tabs).toEqual([
      expect.objectContaining({
        path: "README.md",
        segment: null,
        sourceMode: "directory",
        isPreview: true,
        isPinned: false,
      }),
    ]);
    expect(activePath(7)).toBe("README.md");
    expect(selectActivePreviewTab(store(), 7)?.path).toBe("README.md");
  });

  it("replaces the temporary tab in place instead of adding a second one", () => {
    store().openPreviewInNewTab(7, "kept.go", "directory");
    store().openPreview(7, "a.md", "directory");
    store().openPreview(7, "b.md", "directory");

    expect(paths(7)).toEqual(["kept.go", "b.md"]);
    expect(tabAt(7, "b.md")?.isPreview).toBe(true);
    expect(activePath(7)).toBe("b.md");
  });

  it("keeps the markdown segment when the temporary tab is replaced within the same mode", () => {
    store().openPreview(7, "a.md", "directory");
    store().setPreviewSegment(7, "split");
    store().openPreview(7, "b.md", "directory");

    expect(tabAt(7, "b.md")?.segment).toBe("split");
  });

  it("resets the segment when the temporary tab is replaced from another mode", () => {
    store().openPreview(7, "a.md", "directory");
    store().setPreviewSegment(7, "split");
    store().openPreview(7, "b.md", "session");

    expect(tabAt(7, "b.md")).toMatchObject({
      segment: null,
      sourceMode: "session",
    });
  });

  it("activates the existing tab when an already open file is opened again", () => {
    store().openPreviewInNewTab(7, "a.md", "directory");
    store().openPreviewInNewTab(7, "b.md", "directory");
    store().openPreview(7, "a.md", "directory");

    expect(paths(7)).toEqual(["a.md", "b.md"]);
    expect(activePath(7)).toBe("a.md");
    // 既有标签被激活，不会被降级回临时标签。
    expect(tabAt(7, "a.md")?.isPreview).toBe(false);
  });

  it("re-points an already open tab at the mode it was re-opened from", () => {
    store().openPreviewInNewTab(7, "a.go", "directory");
    store().setPreviewSegment(7, "split");
    store().openPreview(7, "a.go", "git");

    // 首视图由入口模式决定（决策 9）：从 Git 重新点开同一个文件要看到对比。
    expect(paths(7)).toEqual(["a.go"]);
    expect(tabAt(7, "a.go")).toMatchObject({
      sourceMode: "git",
      segment: null,
    });
  });

  it("promotes the active temporary tab to a permanent one (double click)", () => {
    store().openPreview(7, "a.md", "directory");
    store().promoteActivePreviewTab(7);

    expect(tabAt(7, "a.md")?.isPreview).toBe(false);

    // 转常驻之后再单击别的文件不再原地替换它。
    store().openPreview(7, "b.md", "directory");
    expect(paths(7)).toEqual(["a.md", "b.md"]);
  });

  it("opens a permanent tab directly and promotes an already open temporary tab", () => {
    store().openPreview(7, "a.md", "directory");
    store().openPreviewInNewTab(7, "a.md", "directory");

    expect(paths(7)).toEqual(["a.md"]);
    expect(tabAt(7, "a.md")?.isPreview).toBe(false);
  });

  it("pins a tab in place (which also promotes it) and unpins it again", () => {
    store().openPreviewInNewTab(7, "a.md", "directory");
    store().openPreview(7, "b.md", "directory");
    store().togglePreviewTabPin(7, "b.md");

    expect(paths(7)).toEqual(["a.md", "b.md"]);
    expect(tabAt(7, "b.md")).toMatchObject({
      isPinned: true,
      isPreview: false,
    });

    store().togglePreviewTabPin(7, "b.md");
    expect(tabAt(7, "b.md")?.isPinned).toBe(false);
    expect(paths(7)).toEqual(["a.md", "b.md"]);
  });

  it("sets the segment on the active tab only, and each tab keeps its own", () => {
    store().openPreviewInNewTab(7, "a.md", "directory");
    store().setPreviewSegment(7, "text");
    store().openPreviewInNewTab(7, "b.md", "directory");
    store().setPreviewSegment(7, "split");

    expect(tabAt(7, "a.md")?.segment).toBe("text");
    expect(tabAt(7, "b.md")?.segment).toBe("split");

    store().activatePreviewTab(7, "a.md");
    expect(selectActivePreviewTab(store(), 7)?.segment).toBe("text");
  });

  it("rejects an unknown segment or a session without tabs by no-op", () => {
    store().openPreview(7, "a.md", "directory");
    store().setPreviewSegment(7, "bogus" as never);
    expect(tabAt(7, "a.md")?.segment).toBeNull();

    store().setPreviewSegment(8, "text");
    expect(entry(8)).toBeUndefined();
  });

  it("ignores an open for a non-session id, an empty path or an unknown source mode", () => {
    store().openPreview(0, "a.md", "directory");
    store().openPreview(7, "", "directory");
    store().openPreview(7, "a.md", "bogus" as never);

    expect(store().previewTabsBySession).toEqual({});
  });

  it("activates the right neighbour after closing the active tab, then the left", () => {
    store().openPreviewInNewTab(7, "a.md", "directory");
    store().openPreviewInNewTab(7, "b.md", "directory");
    store().openPreviewInNewTab(7, "c.md", "directory");

    store().activatePreviewTab(7, "b.md");
    store().closePreviewTab(7, "b.md");
    expect(paths(7)).toEqual(["a.md", "c.md"]);
    expect(activePath(7)).toBe("c.md");

    // 最右的标签关掉后没有右邻居，激活左邻居。
    store().closePreviewTab(7, "c.md");
    expect(activePath(7)).toBe("a.md");
  });

  it("keeps the active tab when a different tab is closed", () => {
    store().openPreviewInNewTab(7, "a.md", "directory");
    store().openPreviewInNewTab(7, "b.md", "directory");

    store().closePreviewTab(7, "a.md");
    expect(activePath(7)).toBe("b.md");
  });

  it("drops the whole session entry when the last tab is closed", () => {
    store().openPreview(7, "a.md", "directory");
    store().openPreview(8, "b.md", "directory");

    store().closePreviewTab(7, "a.md");

    expect(entry(7)).toBeUndefined();
    expect(selectActivePreviewTab(store(), 7)).toBeNull();
    expect(paths(8)).toEqual(["b.md"]);
  });

  it("closes other tabs, keeping pinned ones", () => {
    store().openPreviewInNewTab(7, "a.md", "directory");
    store().openPreviewInNewTab(7, "b.md", "directory");
    store().openPreviewInNewTab(7, "c.md", "directory");
    store().togglePreviewTabPin(7, "a.md");

    store().closeOtherPreviewTabs(7, "b.md");
    expect(paths(7)).toEqual(["a.md", "b.md"]);

    store().togglePreviewTabPin(7, "a.md");
    store().closeOtherPreviewTabs(7, "b.md");
    expect(paths(7)).toEqual(["b.md"]);
    expect(activePath(7)).toBe("b.md");
  });

  // 「全部关闭」= 整组关掉,固定标签也不例外(菜单项就叫「全部关闭」;要留下某
  // 几个的语义已经由「关闭其他」承担)。关光之后整条会话条目消失,预览面板收起。
  it("closes every tab including pinned ones, leaving other sessions alone", () => {
    store().openPreviewInNewTab(7, "a.md", "directory");
    store().openPreviewInNewTab(7, "b.md", "directory");
    store().togglePreviewTabPin(7, "a.md");
    store().openPreview(8, "other.md", "directory");

    store().closeAllPreviewTabs(7);

    expect(entry(7)).toBeUndefined();
    expect(selectActivePreviewTab(store(), 7)).toBeNull();
    expect(paths(8)).toEqual(["other.md"]);
  });

  it("evicts the least recently activated unpinned tab at the 8 tab cap", () => {
    for (let i = 1; i <= 8; i++) {
      store().openPreviewInNewTab(7, `f${i}.md`, "directory");
    }
    // f1 刚被激活过，最久未被激活的是 f2。
    store().activatePreviewTab(7, "f1.md");
    store().openPreviewInNewTab(7, "f9.md", "directory");

    expect(paths(7)).toEqual([
      "f1.md",
      "f3.md",
      "f4.md",
      "f5.md",
      "f6.md",
      "f7.md",
      "f8.md",
      "f9.md",
    ]);
    expect(activePath(7)).toBe("f9.md");
  });

  it("never evicts a pinned tab, preferring an older unpinned one", () => {
    for (let i = 1; i <= 8; i++) {
      store().openPreviewInNewTab(7, `f${i}.md`, "directory");
    }
    store().togglePreviewTabPin(7, "f1.md");
    store().openPreviewInNewTab(7, "f9.md", "directory");

    expect(paths(7)).toContain("f1.md");
    expect(paths(7)).not.toContain("f2.md");
    expect(paths(7)).toHaveLength(8);
  });

  it("does not resurrect a clobbered tab at the cap by evicting the tab the double click just promoted", () => {
    // 7 个固定标签 + 1 个临时标签，正好占满上限。这是 sidebar-row 双击手势的
    // 真实序列：第一次 click 把临时标签 t.md 原地换成 f9.md，dblclick 把 f9.md
    // 转常驻，随后才补回被吞掉的 t.md。
    for (let i = 1; i <= 7; i++) {
      store().openPreviewInNewTab(7, `f${i}.md`, "directory");
      store().togglePreviewTabPin(7, `f${i}.md`);
    }
    store().openPreview(7, "t.md", "directory");
    const clobbered = store().openPreview(7, "f9.md", "directory");
    expect(clobbered?.path).toBe("t.md");
    store().openPreviewInNewTab(7, "f9.md", "directory");

    store().restoreClobberedPreviewTab(7, clobbered!);

    // 补回是尽力而为：没位置就不补，绝不能把用户刚双击出来的那个标签顶掉——
    // 顶掉它还会让 activePath 指向一个不存在的标签，整个预览面板就此消失。
    expect(paths(7)).toContain("f9.md");
    expect(activePath(7)).toBe("f9.md");
    expect(selectActivePreviewTab(store(), 7)).not.toBeNull();
  });

  it("refuses to open a ninth tab when all eight are pinned and says so", () => {
    for (let i = 1; i <= 8; i++) {
      store().openPreviewInNewTab(7, `f${i}.md`, "directory");
      store().togglePreviewTabPin(7, `f${i}.md`);
    }

    store().openPreviewInNewTab(7, "f9.md", "directory");

    expect(paths(7)).toHaveLength(8);
    expect(paths(7)).not.toContain("f9.md");
    expect(activePath(7)).toBe("f8.md");
    // 提示要是真人话，而不是漏翻译的原始 key。
    expect(sonnerMocks.toast.warning).toHaveBeenCalledTimes(1);
    const notice = sonnerMocks.toast.warning.mock.calls[0][0] as string;
    expect(notice).not.toContain("chatContext.");
    expect(notice).toContain("8");
  });

  it("keeps tabs per session and persists them across a restart", async () => {
    store().openPreviewInNewTab(7, "a.md", "directory");
    store().openPreview(7, "b.go", "git");
    store().openPreview(8, "c.md", "session");

    expect(paths(7)).toEqual(["a.md", "b.go"]);
    expect(paths(8)).toEqual(["c.md"]);

    // 重启 = 上一次运行写进 localStorage 的内容被新进程读回来。
    const persisted = localStorage.getItem("file-preview-tabs-state");
    expect(persisted).toContain('"path":"b.go"');
    useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
    localStorage.setItem("file-preview-tabs-state", persisted as string);
    await useFilePreviewTabsStore.persist.rehydrate();

    expect(paths(7)).toEqual(["a.md", "b.go"]);
    expect(tabAt(7, "b.go")).toMatchObject({
      sourceMode: "git",
      isPreview: true,
    });
    expect(activePath(7)).toBe("b.go");
    expect(paths(8)).toEqual(["c.md"]);
  });

  it("drops a tab whose source mode is not one this build writes", async () => {
    // 项目未发布，不为已废弃的旧来源模式留回落：本 store 写出去的每个标签都带
    // 一个合法 sourceMode，读到别的值就说明数据被改坏了，整条丢弃。
    localStorage.setItem(
      "file-preview-tabs-state",
      JSON.stringify({
        state: {
          previewTabsBySession: {
            7: {
              tabs: [
                {
                  path: "a.go",
                  segment: null,
                  sourceMode: "changes",
                  isPreview: false,
                  isPinned: false,
                  activatedAt: 1,
                },
              ],
              activePath: "a.go",
            },
          },
        },
        version: 0,
      }),
    );
    await useFilePreviewTabsStore.persist.rehydrate();

    expect(paths(7)).toEqual([]);
  });

  it("keeps only the 20 most recently activated sessions", () => {
    for (let sessionId = 1; sessionId <= 21; sessionId++) {
      store().openPreview(sessionId, "a.md", "directory");
    }

    const ids = Object.keys(store().previewTabsBySession)
      .map(Number)
      .sort((a, b) => a - b);
    expect(ids).toHaveLength(20);
    expect(ids).not.toContain(1);
    expect(ids).toContain(21);
  });

  it("drops persisted session entries that are missing, invalid or over the cap", async () => {
    localStorage.setItem(
      "file-preview-tabs-state",
      JSON.stringify({
        state: {
          previewTabsBySession: {
            7: {
              tabs: [
                {
                  path: "README.md",
                  segment: "split",
                  sourceMode: "directory",
                  isPreview: false,
                  isPinned: true,
                  activatedAt: 5,
                },
              ],
              activePath: "README.md",
            },
            8: {
              tabs: [
                {
                  path: "a.go",
                  segment: null,
                  sourceMode: "git",
                  isPreview: true,
                  isPinned: false,
                  activatedAt: 3,
                },
              ],
              activePath: "a.go",
            },
            // segment 字段缺失。
            13: {
              tabs: [
                {
                  path: "d.md",
                  sourceMode: "git",
                  isPreview: false,
                  isPinned: false,
                  activatedAt: 2,
                },
              ],
              activePath: "d.md",
            },
            // sourceMode 非法。
            9: {
              tabs: [
                {
                  path: "b.md",
                  segment: null,
                  sourceMode: "bogus",
                  isPreview: false,
                  isPinned: false,
                  activatedAt: 1,
                },
              ],
              activePath: "b.md",
            },
            // activePath 不在标签集合里。
            10: {
              tabs: [
                {
                  path: "c.md",
                  segment: null,
                  sourceMode: "git",
                  isPreview: false,
                  isPinned: false,
                  activatedAt: 1,
                },
              ],
              activePath: "gone.md",
            },
            // 标签数超过每会话上限。
            11: {
              tabs: Array.from({ length: 9 }, (_, i) => ({
                path: `f${i}.md`,
                segment: null,
                sourceMode: "directory",
                isPreview: false,
                isPinned: false,
                activatedAt: i,
              })),
              activePath: "f0.md",
            },
            // 不是会话 id。
            notAnId: {
              tabs: [
                {
                  path: "x.md",
                  segment: null,
                  sourceMode: "git",
                  isPreview: false,
                  isPinned: false,
                  activatedAt: 1,
                },
              ],
              activePath: "x.md",
            },
            // 空标签集合。
            12: { tabs: [], activePath: null },
          },
        },
        version: 0,
      }),
    );
    await useFilePreviewTabsStore.persist.rehydrate();

    expect(Object.keys(store().previewTabsBySession).sort()).toEqual([
      "7",
      "8",
    ]);
    expect(tabAt(8, "a.go")?.segment).toBeNull();
  });

  it("falls back to an empty tab table when the persisted value is not an object", async () => {
    localStorage.setItem(
      "file-preview-tabs-state",
      JSON.stringify({
        state: { previewTabsBySession: "README.md" },
        version: 0,
      }),
    );
    await useFilePreviewTabsStore.persist.rehydrate();

    expect(store().previewTabsBySession).toEqual({});
  });
});
