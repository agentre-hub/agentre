import { beforeEach, describe, expect, it } from "vitest";

import {
  useChatSidebarStore,
  type ChangesScope,
  type ChatSidebarTab,
} from "../chat-sidebar-store";

describe("chat-sidebar-store", () => {
  beforeEach(() => {
    localStorage.clear();
    useChatSidebarStore.setState({
      open: true,
      activeTab: "outline",
      changesScope: "session",
      showIgnored: false,
    });
  });

  it("toggles open and persists to localStorage", () => {
    useChatSidebarStore.getState().setOpen(false);
    expect(useChatSidebarStore.getState().open).toBe(false);
    const raw = localStorage.getItem("chat-sidebar-state");
    expect(raw).toContain('"open":false');
  });

  it("switches activeTab between outline, changes and directory", () => {
    useChatSidebarStore.getState().setActiveTab("changes");
    expect(useChatSidebarStore.getState().activeTab).toBe("changes");
    useChatSidebarStore.getState().setActiveTab("directory");
    expect(useChatSidebarStore.getState().activeTab).toBe("directory");
  });

  it("rejects unknown tab values at runtime by no-op", () => {
    useChatSidebarStore.getState().setActiveTab("bogus" as ChatSidebarTab);
    expect(useChatSidebarStore.getState().activeTab).toBe("outline");
  });

  it("falls back to outline when the persisted active tab is invalid", async () => {
    localStorage.setItem(
      "chat-sidebar-state",
      JSON.stringify({
        state: { open: true, activeTab: "bogus" },
        version: 0,
      }),
    );
    await useChatSidebarStore.persist.rehydrate();
    expect(useChatSidebarStore.getState().activeTab).toBe("outline");
  });

  it("falls back to outline when the persisted state has no active tab", async () => {
    localStorage.setItem(
      "chat-sidebar-state",
      JSON.stringify({ state: { open: true }, version: 0 }),
    );
    await useChatSidebarStore.persist.rehydrate();
    expect(useChatSidebarStore.getState().activeTab).toBe("outline");
  });

  it("keeps a valid persisted active tab of changes", async () => {
    localStorage.setItem(
      "chat-sidebar-state",
      JSON.stringify({
        state: { open: true, activeTab: "changes" },
        version: 0,
      }),
    );
    await useChatSidebarStore.persist.rehydrate();
    expect(useChatSidebarStore.getState().activeTab).toBe("changes");
  });

  it("falls back to outline for the removed top-level tab values git and files", async () => {
    // 一级三段改为 大纲 / 变更 / 目录（本轮决策 1），旧值 "git" 与 "files" 都
    // 成了非法值；决策 13 明确它们直接丢弃并回落，不写兼容层。
    for (const stale of ["git", "files"]) {
      localStorage.setItem(
        "chat-sidebar-state",
        JSON.stringify({
          state: { open: true, activeTab: stale },
          version: 0,
        }),
      );
      await useChatSidebarStore.persist.rehydrate();
      expect(useChatSidebarStore.getState().activeTab).toBe("outline");
    }
  });

  it("defaults the changes scope to this session and persists a switch", () => {
    expect(useChatSidebarStore.getState().changesScope).toBe("session");
    useChatSidebarStore.getState().setChangesScope("uncommitted");
    expect(useChatSidebarStore.getState().changesScope).toBe("uncommitted");
    expect(localStorage.getItem("chat-sidebar-state")).toContain(
      '"changesScope":"uncommitted"',
    );
  });

  it("rejects unknown changes scope values at runtime by no-op", () => {
    useChatSidebarStore.getState().setChangesScope("bogus" as ChangesScope);
    expect(useChatSidebarStore.getState().changesScope).toBe("session");
  });

  it("defaults showIgnored to false and persists a toggle", () => {
    expect(useChatSidebarStore.getState().showIgnored).toBe(false);
    useChatSidebarStore.getState().setShowIgnored(true);
    expect(useChatSidebarStore.getState().showIgnored).toBe(true);
    expect(localStorage.getItem("chat-sidebar-state")).toContain(
      '"showIgnored":true',
    );
  });

  it("falls back to this session when the persisted changes scope is invalid", async () => {
    localStorage.setItem(
      "chat-sidebar-state",
      JSON.stringify({
        state: { open: true, activeTab: "changes", changesScope: "bogus" },
        version: 0,
      }),
    );
    await useChatSidebarStore.persist.rehydrate();
    expect(useChatSidebarStore.getState().changesScope).toBe("session");
  });

  it("drops the removed filesMode and gitBaselineBySession values instead of restoring them", async () => {
    // 决策 13：「文件」页的两档模式与「本分支」档的基线记录整个废弃，不写兼容层。
    localStorage.setItem(
      "chat-sidebar-state",
      JSON.stringify({
        state: {
          open: true,
          activeTab: "changes",
          filesMode: "directory",
          gitBaselineBySession: { 7: "origin/main" },
        },
        version: 0,
      }),
    );
    await useChatSidebarStore.persist.rehydrate();

    const state = useChatSidebarStore.getState() as Record<string, unknown>;
    expect(state.filesMode).toBeUndefined();
    expect(state.gitBaselineBySession).toBeUndefined();
    expect(useChatSidebarStore.getState().changesScope).toBe("session");
  });

  it("falls back to this session when the persisted state has no changes scope", async () => {
    localStorage.setItem(
      "chat-sidebar-state",
      JSON.stringify({
        state: { open: true, activeTab: "changes" },
        version: 0,
      }),
    );
    await useChatSidebarStore.persist.rehydrate();
    expect(useChatSidebarStore.getState().changesScope).toBe("session");
    expect(useChatSidebarStore.getState().showIgnored).toBe(false);
  });

  it("falls back to hiding ignored entries when the persisted flag is not a boolean", async () => {
    localStorage.setItem(
      "chat-sidebar-state",
      JSON.stringify({
        state: { open: true, activeTab: "changes", showIgnored: "yes" },
        version: 0,
      }),
    );
    await useChatSidebarStore.persist.rehydrate();
    expect(useChatSidebarStore.getState().showIgnored).toBe(false);
  });
});
