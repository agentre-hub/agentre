import { beforeEach, describe, expect, it, vi } from "vitest";

// 只 mock 到 reload 的依赖边界: chat-agents-store / session-index-store 在 import
// 时会拉 App binding 与 project-tree hook, 这里替身掉, 让 sidebar-reload 的纯逻辑
// (是否已知 + 是否触发 reload) 可以脱离 Wails 单测。
vi.mock("../../../wailsjs/go/app/App", () => ({
  ListChatAgents: vi.fn(),
  ListChatIndexSessions: vi.fn(),
}));
const reloadProjectTreeCache = vi.fn();
const treeLoaded = { value: false };
vi.mock("@/hooks/use-project-tree", () => ({
  ensureProjectTreeLoaded: vi.fn(),
  isProjectTreeCacheLoaded: () => treeLoaded.value,
  reloadProjectTreeCache: (...args: unknown[]) =>
    reloadProjectTreeCache(...args),
}));

import { useChatAgentsStore, type AgentSlim } from "../chat-agents-store";
import { useSessionIndexStore } from "../session-index-store";
import {
  ensureSessionInSidebar,
  isSessionKnownToSidebar,
  reloadSidebarSources,
} from "../sidebar-reload";

function seedAgents(...sessionIds: number[]) {
  useChatAgentsStore.setState({
    agents: [
      { id: 1, name: "Eng", sessions: [], sessionIds } as unknown as AgentSlim,
    ],
    loading: false,
    error: null,
  });
}

function setTreeLoaded(loaded: boolean) {
  treeLoaded.value = loaded;
}

describe("sidebar-reload helpers", () => {
  beforeEach(() => {
    // spyOn 在「已被 spy 的方法」上会复用同一个 spy(连带其调用计数),
    // 不 restore 的话上一例的 reload 计数会漏到下一例 —— 每例先还原再重新 spy。
    vi.restoreAllMocks();
    reloadProjectTreeCache.mockReset();
    reloadProjectTreeCache.mockResolvedValue([]);
    setTreeLoaded(false);
    useChatAgentsStore.getState().__reset();
    useSessionIndexStore.getState().__reset();
  });

  describe("isSessionKnownToSidebar", () => {
    it("returns true when an agent already lists the session id", () => {
      seedAgents(99);
      expect(isSessionKnownToSidebar(99)).toBe(true);
    });

    it("returns false for a session id no agent lists", () => {
      seedAgents(99);
      expect(isSessionKnownToSidebar(11)).toBe(false);
    });

    it("returns false for non-positive ids", () => {
      seedAgents(99);
      expect(isSessionKnownToSidebar(0)).toBe(false);
      expect(isSessionKnownToSidebar(-1)).toBe(false);
    });
  });

  describe("reloadSidebarSources", () => {
    it("Given the project tree was loaded, When the sidebar refreshes, Then the tree is refetched too", () => {
      // F3：项目树**没有任何推送通道**（全仓 EventsOn 里没有项目变更），此前只靠
      // 项目页那条 1 秒轮询兜着。轮询删掉后它必须挂在这个统一入口上，否则另一台
      // 设备同步过来的项目要等一次别的交互才出现。
      setTreeLoaded(true);
      vi.spyOn(useChatAgentsStore.getState(), "reload").mockResolvedValue();
      vi.spyOn(
        useSessionIndexStore.getState(),
        "reloadLoaded",
      ).mockResolvedValue();

      reloadSidebarSources();

      expect(reloadProjectTreeCache).toHaveBeenCalledTimes(1);
    });

    it("Given the project tree was never loaded, When the sidebar refreshes, Then it stays silent instead of pulling project data for a user who never opened it", () => {
      setTreeLoaded(false);
      vi.spyOn(useChatAgentsStore.getState(), "reload").mockResolvedValue();
      vi.spyOn(
        useSessionIndexStore.getState(),
        "reloadLoaded",
      ).mockResolvedValue();

      reloadSidebarSources();

      expect(reloadProjectTreeCache).not.toHaveBeenCalled();
    });

    it("Given loaded index scopes, When the sidebar refreshes, Then they are refetched", () => {
      const chatReload = vi
        .spyOn(useChatAgentsStore.getState(), "reload")
        .mockResolvedValue();
      const indexReload = vi
        .spyOn(useSessionIndexStore.getState(), "reloadLoaded")
        .mockResolvedValue();

      reloadSidebarSources();

      expect(chatReload).toHaveBeenCalledTimes(1);
      expect(indexReload).toHaveBeenCalledTimes(1);
    });
  });

  describe("ensureSessionInSidebar", () => {
    it("reloads both sidebar sources when the session is unknown", () => {
      seedAgents(99);
      const chatReload = vi
        .spyOn(useChatAgentsStore.getState(), "reload")
        .mockResolvedValue();
      const indexReload = vi
        .spyOn(useSessionIndexStore.getState(), "reloadLoaded")
        .mockResolvedValue();

      ensureSessionInSidebar(11);

      expect(chatReload).toHaveBeenCalledTimes(1);
      expect(indexReload).toHaveBeenCalledTimes(1);
    });

    it("does not reload when the session is already in the sidebar", () => {
      seedAgents(11);
      const chatReload = vi
        .spyOn(useChatAgentsStore.getState(), "reload")
        .mockResolvedValue();
      const indexReload = vi
        .spyOn(useSessionIndexStore.getState(), "reloadLoaded")
        .mockResolvedValue();

      ensureSessionInSidebar(11);

      expect(chatReload).not.toHaveBeenCalled();
      expect(indexReload).not.toHaveBeenCalled();
    });

    it("ignores non-positive session ids", () => {
      seedAgents(99);
      const chatReload = vi
        .spyOn(useChatAgentsStore.getState(), "reload")
        .mockResolvedValue();

      ensureSessionInSidebar(0);

      expect(chatReload).not.toHaveBeenCalled();
    });
  });
});
