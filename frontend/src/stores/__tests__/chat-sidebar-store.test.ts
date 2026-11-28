import { beforeEach, describe, expect, it } from "vitest";

import { useChatSidebarStore, type ChangesScope } from "../chat-sidebar-store";

describe("chat-sidebar-store", () => {
  beforeEach(() => {
    localStorage.clear();
    useChatSidebarStore.setState({
      changesScope: "session",
      showIgnored: false,
    });
  });

  it("no longer carries the sidebar open flag or top-level tab (moved to the tool-window layout)", async () => {
    localStorage.setItem(
      "chat-sidebar-state",
      JSON.stringify({
        state: { open: false, activeTab: "changes" },
        version: 0,
      }),
    );
    await useChatSidebarStore.persist.rehydrate();
    const state = useChatSidebarStore.getState() as Record<string, unknown>;
    expect(state.open).toBeUndefined();
    expect(state.activeTab).toBeUndefined();
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
