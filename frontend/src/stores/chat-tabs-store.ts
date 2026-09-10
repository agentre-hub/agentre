import { create } from "zustand";
import i18n from "@/i18n";
import { writePersistedTabs, readPersistedTabs } from "./chat-tabs-persistence";

export type TabKind =
  | { kind: "session"; sessionId: number }
  | { kind: "new"; projectId: number; agentId: number; workMode: string }
  | {
      kind: "terminal";
      projectId: number;
      deviceId: string;
      terminalId: string;
      attach?: boolean;
      command?: string;
    }
  | {
      kind: "peer";
      fingerprint: string;
      // 对端会话按 conversation_id 寻址（uuid 字符串），不是本机的 chat_sessions.id。
      conversationId: string;
      deviceName: string;
    };

export type ChatTab = {
  id: string;
  meta: TabKind;
  isPreview: boolean;
  isPinned: boolean;
  pinAt: number;
  openedAt: number;
  title?: string;
};

type State = {
  tabs: ChatTab[];
  activeTabId: string | null;
};

type Actions = {
  openSession: (sessionId: number) => void;
  openSessionInNewTab: (sessionId: number) => void;
  openNewSession: (
    projectId: number,
    agentId: number,
    workMode: string,
  ) => void;
  promoteCurrent: () => void;
  togglePin: (id: string) => void;
  closeTab: (id: string) => void;
  closeOthers: (id: string) => void;
  closeTabsToRight: (id: string) => void;
  setActive: (id: string) => void;
  moveTab: (fromIndex: number, toIndex: number) => void;
  bumpToAfterPinned: (id: string) => void;
  resolveNewTab: (tabId: string, sessionId: number) => void;
  reconcileMissingSessions: (existingSessionIds: Set<number>) => void;
  openTerminal: (
    projectId: number,
    deviceId: string,
    deviceName?: string,
  ) => void;
  attachTerminal: (args: { terminalId: string; command?: string }) => void;
  /** 打开一枚远端桌面会话 Peer Tab（R19）。指纹 + 远端会话 id 唯一标识一枚 Tab，
   *  已打开时只切到它（不重复开）。关闭只 detach，不删除对端会话。 */
  openPeerTab: (args: {
    fingerprint: string;
    conversationId: string;
    title?: string;
    deviceName: string;
  }) => void;
};

// nextId: 测试用例可以 stub。生产环境用 crypto.randomUUID。
// Used by actions implemented in Tasks 2–11; declared here for testability.
let nextId = (): string => crypto.randomUUID();
export const __setNextIdFactoryForTesting = (fn: () => string) => {
  nextId = fn;
};

// now: 同上,测试可控的时间源。
let now = (): number => Date.now();
export const __setNowForTesting = (fn: () => number) => {
  now = fn;
};

export const useChatTabsStore = create<State & Actions>((set, _get) => ({
  tabs: [],
  activeTabId: null,

  openSession: (sessionId) =>
    set((state) => {
      const existing = state.tabs.find(
        (t) => t.meta.kind === "session" && t.meta.sessionId === sessionId,
      );
      if (existing) {
        return { activeTabId: existing.id };
      }
      const previewIdx = state.tabs.findIndex((t) => t.isPreview);
      const newTab: ChatTab = {
        id: nextId(),
        meta: { kind: "session", sessionId },
        isPreview: true,
        isPinned: false,
        pinAt: 0,
        openedAt: now(),
      };
      if (previewIdx >= 0) {
        const tabs = [...state.tabs];
        tabs[previewIdx] = newTab;
        return { tabs, activeTabId: newTab.id };
      }
      return { tabs: [...state.tabs, newTab], activeTabId: newTab.id };
    }),
  openSessionInNewTab: (sessionId) =>
    set((state) => {
      const existing = state.tabs.find(
        (t) => t.meta.kind === "session" && t.meta.sessionId === sessionId,
      );
      if (existing) {
        return { activeTabId: existing.id };
      }
      const newTab: ChatTab = {
        id: nextId(),
        meta: { kind: "session", sessionId },
        isPreview: false,
        isPinned: false,
        pinAt: 0,
        openedAt: now(),
      };
      return { tabs: [...state.tabs, newTab], activeTabId: newTab.id };
    }),
  openNewSession: (projectId, agentId, workMode) =>
    set((state) => {
      const newTab: ChatTab = {
        id: nextId(),
        meta: { kind: "new", projectId, agentId, workMode },
        isPreview: false,
        isPinned: false,
        pinAt: 0,
        openedAt: now(),
      };
      return { tabs: [...state.tabs, newTab], activeTabId: newTab.id };
    }),
  promoteCurrent: () =>
    set((state) => {
      if (!state.activeTabId) return state;
      const idx = state.tabs.findIndex((t) => t.id === state.activeTabId);
      if (idx < 0 || !state.tabs[idx].isPreview) return state;
      const tabs = [...state.tabs];
      tabs[idx] = { ...tabs[idx], isPreview: false };
      return { tabs };
    }),
  togglePin: (id) =>
    set((state) => {
      const idx = state.tabs.findIndex((t) => t.id === id);
      if (idx < 0) return state;
      const cur = state.tabs[idx];
      if (cur.isPinned) {
        // unpin: 位置不动
        const tabs = [...state.tabs];
        tabs[idx] = { ...cur, isPinned: false, pinAt: 0 };
        return { tabs };
      }
      // pin: 搬到 pinned 前缀末端
      let lastPinnedPrefixIndex = -1;
      for (let i = 0; i < state.tabs.length; i++) {
        if (state.tabs[i].isPinned) lastPinnedPrefixIndex = i;
        else break;
      }
      const pinned: ChatTab = {
        ...cur,
        isPinned: true,
        pinAt: now(),
        isPreview: false,
      };
      const target = lastPinnedPrefixIndex + 1;
      const tabs = [...state.tabs];
      tabs.splice(idx, 1);
      const insertAt = idx < target ? target - 1 : target;
      tabs.splice(insertAt, 0, pinned);
      return { tabs };
    }),
  closeTab: (id) =>
    set((state) => {
      const idx = state.tabs.findIndex((t) => t.id === id);
      if (idx < 0) return state;
      const tabs = [...state.tabs.slice(0, idx), ...state.tabs.slice(idx + 1)];
      let activeTabId = state.activeTabId;
      if (state.activeTabId === id) {
        if (tabs.length === 0) {
          activeTabId = null;
        } else if (idx < tabs.length) {
          activeTabId = tabs[idx].id; // 右邻居
        } else {
          activeTabId = tabs[tabs.length - 1].id; // 左邻居
        }
      }
      return { tabs, activeTabId };
    }),
  closeOthers: (id) =>
    set((state) => {
      const target = state.tabs.find((t) => t.id === id);
      if (!target) return state;
      const tabs = state.tabs.filter((t) => t.id === id || t.isPinned);
      if (tabs.length === state.tabs.length) return state;
      const activeTabId =
        state.activeTabId && tabs.some((t) => t.id === state.activeTabId)
          ? state.activeTabId
          : id;
      return { tabs, activeTabId };
    }),
  closeTabsToRight: (id) =>
    set((state) => {
      const idx = state.tabs.findIndex((t) => t.id === id);
      if (idx < 0) return state;
      const tabs = state.tabs.filter((t, i) => i <= idx || t.isPinned);
      if (tabs.length === state.tabs.length) return state;
      const activeTabId =
        state.activeTabId && tabs.some((t) => t.id === state.activeTabId)
          ? state.activeTabId
          : id;
      return { tabs, activeTabId };
    }),
  setActive: (id) =>
    set((state) => {
      if (!state.tabs.some((t) => t.id === id)) return state;
      return { activeTabId: id };
    }),
  moveTab: (fromIndex, toIndex) =>
    set((state) => {
      const len = state.tabs.length;
      if (
        fromIndex < 0 ||
        fromIndex >= len ||
        toIndex < 0 ||
        toIndex >= len ||
        fromIndex === toIndex
      ) {
        return state;
      }
      const tabs = [...state.tabs];
      const [moved] = tabs.splice(fromIndex, 1);
      tabs.splice(toIndex, 0, moved);

      // normalize: if moved tab was pinned but ended up outside pinned prefix, unpin it
      let lastPinnedPrefixIndex = -1;
      for (let i = 0; i < tabs.length; i++) {
        if (tabs[i].isPinned) lastPinnedPrefixIndex = i;
        else break;
      }
      const finalIdx = tabs.indexOf(moved);
      if (moved.isPinned && finalIdx > lastPinnedPrefixIndex) {
        tabs[finalIdx] = { ...moved, isPinned: false, pinAt: 0 };
      }
      return { tabs };
    }),
  bumpToAfterPinned: (id) =>
    set((state) => {
      const idx = state.tabs.findIndex((t) => t.id === id);
      if (idx < 0) return state;
      let lastPinnedPrefixIndex = -1;
      for (let i = 0; i < state.tabs.length; i++) {
        if (state.tabs[i].isPinned) lastPinnedPrefixIndex = i;
        else break;
      }
      const target = lastPinnedPrefixIndex + 1;
      if (idx === target) return state;
      const tabs = [...state.tabs];
      const [moved] = tabs.splice(idx, 1);
      // 注意: 如果 idx < target, splice 后 target 要 -1
      const insertAt = idx < target ? target - 1 : target;
      tabs.splice(insertAt, 0, moved);
      return { tabs };
    }),
  resolveNewTab: (tabId, sessionId) =>
    set((state) => {
      const idx = state.tabs.findIndex((t) => t.id === tabId);
      if (idx < 0 || state.tabs[idx].meta.kind !== "new") return state;
      const tabs = [...state.tabs];
      tabs[idx] = {
        ...tabs[idx],
        meta: { kind: "session", sessionId },
      };
      return { tabs };
    }),
  reconcileMissingSessions: (existingSessionIds) =>
    set((state) => {
      const tabs = state.tabs.filter((t) => {
        if (t.meta.kind !== "session") return true;
        return existingSessionIds.has(t.meta.sessionId);
      });
      if (tabs.length === state.tabs.length) return state;
      let activeTabId = state.activeTabId;
      if (activeTabId && !tabs.some((t) => t.id === activeTabId)) {
        activeTabId = tabs[0]?.id ?? null;
      }
      return { tabs, activeTabId };
    }),
  openTerminal: (projectId, deviceId, deviceName) =>
    set((state) => {
      const newTab: ChatTab = {
        id: nextId(),
        meta: { kind: "terminal", projectId, deviceId, terminalId: nextId() },
        isPreview: false,
        isPinned: false,
        pinAt: 0,
        openedAt: now(),
        title: deviceName
          ? i18n.t("chatTabs.terminal.titleWithDevice", { deviceName })
          : i18n.t("chatTabs.terminal.title"),
      };
      return { tabs: [...state.tabs, newTab], activeTabId: newTab.id };
    }),
  attachTerminal: ({ terminalId, command }) =>
    set((state) => {
      // attach 复用同一 terminalId(绑定到卡片已在跑的 PTY),不 nextId() 新建 PTY。
      const newTab: ChatTab = {
        id: nextId(),
        meta: {
          kind: "terminal",
          projectId: 0,
          deviceId: "",
          terminalId,
          attach: true,
          command,
        },
        isPreview: false,
        isPinned: false,
        pinAt: 0,
        openedAt: now(),
        title: command
          ? i18n.t("chatTabs.terminal.titleWithCommand", { command })
          : i18n.t("chatTabs.terminal.title"),
      };
      return { tabs: [...state.tabs, newTab], activeTabId: newTab.id };
    }),
  openPeerTab: ({ fingerprint, conversationId, title, deviceName }) =>
    set((state) => {
      const existing = state.tabs.find(
        (t) =>
          t.meta.kind === "peer" &&
          t.meta.fingerprint === fingerprint &&
          t.meta.conversationId === conversationId,
      );
      if (existing) {
        return { activeTabId: existing.id };
      }
      const newTab: ChatTab = {
        id: nextId(),
        meta: {
          kind: "peer",
          fingerprint,
          conversationId,
          deviceName,
        },
        isPreview: false,
        isPinned: false,
        pinAt: 0,
        openedAt: now(),
        title:
          title ||
          i18n.t("chatTabs.peer.titleWithDevice", { deviceName }) ||
          i18n.t("chatTabs.peer.title"),
      };
      return { tabs: [...state.tabs, newTab], activeTabId: newTab.id };
    }),
}));

let writeTimer: ReturnType<typeof setTimeout> | undefined;

useChatTabsStore.subscribe((state) => {
  if (writeTimer !== undefined) clearTimeout(writeTimer);
  writeTimer = setTimeout(() => {
    writeTimer = undefined;
    writePersistedTabs(state.tabs, state.activeTabId);
  }, 150);
});

// hydrate: 同步从 localStorage 读, 模块 import 时跑一次。
const restored = readPersistedTabs();
if (restored && restored.tabs.length > 0) {
  useChatTabsStore.setState({
    tabs: restored.tabs,
    activeTabId: restored.activeTabId,
  });
}
