import { beforeEach, describe, expect, it } from "vitest";

import { useChatSidebarStore } from "../chat-sidebar-store";

describe("chat-sidebar-store", () => {
  beforeEach(() => {
    localStorage.clear();
    useChatSidebarStore.setState({ showIgnored: false });
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

  it("defaults showIgnored to false and persists a toggle", () => {
    expect(useChatSidebarStore.getState().showIgnored).toBe(false);
    useChatSidebarStore.getState().setShowIgnored(true);
    expect(useChatSidebarStore.getState().showIgnored).toBe(true);
    expect(localStorage.getItem("chat-sidebar-state")).toContain(
      '"showIgnored":true',
    );
  });

  it("drops a persisted changes scope: the Changes window no longer has tiers", async () => {
    // 「本次会话 / 未提交」两档已去掉，只剩未提交一份清单：旧版本存下的档位读回来无处
    // 安放，也不会被再写回 localStorage。
    localStorage.setItem(
      "chat-sidebar-state",
      JSON.stringify({
        state: { changesScope: "uncommitted", showIgnored: true },
        version: 0,
      }),
    );
    await useChatSidebarStore.persist.rehydrate();

    const state = useChatSidebarStore.getState() as Record<string, unknown>;
    expect(state.changesScope).toBeUndefined();
    expect(state.setChangesScope).toBeUndefined();
    expect(useChatSidebarStore.getState().showIgnored).toBe(true);
    useChatSidebarStore.getState().setShowIgnored(false);
    expect(localStorage.getItem("chat-sidebar-state")).not.toContain(
      "changesScope",
    );
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
  });

  it("falls back to hiding ignored entries when the persisted state has no flag", async () => {
    localStorage.setItem(
      "chat-sidebar-state",
      JSON.stringify({
        state: { open: true, activeTab: "changes" },
        version: 0,
      }),
    );
    await useChatSidebarStore.persist.rehydrate();
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
