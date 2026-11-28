import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("sonner", () => ({ toast: { warning: vi.fn() } }));

import {
  DEFAULT_FILE_RATIO,
  EDITOR_SPLIT_KEY,
  FILE_RATIO_LIMITS,
  useEditorSplitStore,
} from "../editor-split-store";
import { useFilePreviewTabsStore } from "../file-preview-tabs-store";

function persistSplit(state: unknown) {
  localStorage.setItem(EDITOR_SPLIT_KEY, JSON.stringify({ state, version: 0 }));
}

async function rehydrate() {
  await useEditorSplitStore.persist.rehydrate();
}

const open = (sessionId: number, path: string) =>
  useFilePreviewTabsStore.getState().openPreview(sessionId, path, "directory");

beforeEach(() => {
  localStorage.clear();
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useEditorSplitStore.setState({
    fileRatio: DEFAULT_FILE_RATIO,
    fileFullBySession: {},
  });
});

describe("editor split store · 比例", () => {
  // Given 用户拖过分隔条, When 重启（重新读回）, Then 比例还是拖过的值。
  it("persists the dragged ratio and restores it on rehydrate", async () => {
    useEditorSplitStore.getState().setFileRatio(0.37);
    expect(JSON.parse(localStorage.getItem(EDITOR_SPLIT_KEY)!).state).toEqual({
      fileRatio: 0.37,
    });

    // 模拟重启：内存回到默认值，存储里仍是拖过的那份。
    const saved = localStorage.getItem(EDITOR_SPLIT_KEY)!;
    useEditorSplitStore.setState({ fileRatio: DEFAULT_FILE_RATIO });
    localStorage.setItem(EDITOR_SPLIT_KEY, saved);
    await rehydrate();
    expect(useEditorSplitStore.getState().fileRatio).toBe(0.37);
  });

  it("clamps a drag past the limits", () => {
    useEditorSplitStore.getState().setFileRatio(0.99);
    expect(useEditorSplitStore.getState().fileRatio).toBe(
      FILE_RATIO_LIMITS.max,
    );
    useEditorSplitStore.getState().setFileRatio(0);
    expect(useEditorSplitStore.getState().fileRatio).toBe(
      FILE_RATIO_LIMITS.min,
    );
  });

  it.each([["0.4"], [7], [null], [Number.NaN]])(
    "falls back to the default when the saved ratio is %s",
    async (saved) => {
      persistSplit({ fileRatio: saved });
      await rehydrate();
      expect(useEditorSplitStore.getState().fileRatio).toBe(DEFAULT_FILE_RATIO);
    },
  );

  it("never persists the transient file-full state", () => {
    open(1, "a.ts");
    useEditorSplitStore.getState().toggleFileFull(1);
    expect(
      JSON.parse(localStorage.getItem(EDITOR_SPLIT_KEY)!).state,
    ).not.toHaveProperty("fileFullBySession");
  });
});

describe("editor split store · 文件占满", () => {
  // Given 一个会话开着文件, When 切占满两次, Then 进入再还原。
  it("toggles file-full for a session with an open file", () => {
    open(1, "a.ts");
    useEditorSplitStore.getState().toggleFileFull(1);
    expect(useEditorSplitStore.getState().fileFullBySession[1]).toBe(true);
    useEditorSplitStore.getState().toggleFileFull(1);
    expect(useEditorSplitStore.getState().fileFullBySession[1]).toBeUndefined();
  });

  it("ignores file-full when the session has no file open", () => {
    useEditorSplitStore.getState().toggleFileFull(1);
    expect(useEditorSplitStore.getState().fileFullBySession).toEqual({});
  });

  it("is per session", () => {
    open(1, "a.ts");
    open(2, "b.ts");
    useEditorSplitStore.getState().toggleFileFull(1);
    expect(useEditorSplitStore.getState().fileFullBySession).toEqual({
      1: true,
    });
  });

  // Given 占满中, When 关掉最后一个文件标签, Then 占满一起消失——下次打开文件是分屏。
  it("drops file-full when the session's last tab closes", () => {
    open(1, "a.ts");
    open(2, "b.ts");
    useEditorSplitStore.getState().toggleFileFull(1);
    useEditorSplitStore.getState().toggleFileFull(2);

    useFilePreviewTabsStore.getState().closePreviewTab(1, "a.ts");
    expect(useEditorSplitStore.getState().fileFullBySession).toEqual({
      2: true,
    });

    useFilePreviewTabsStore.getState().closeAllPreviewTabs(2);
    expect(useEditorSplitStore.getState().fileFullBySession).toEqual({});
  });
});
