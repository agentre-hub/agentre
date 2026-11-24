import { create } from "zustand";

import { useToolWindowLayout } from "@/stores/tool-window-layout-store";

/**
 * 「网页预览」工具窗的会话级状态（spec「网页预览工具窗」）：标签、每个标签的地址历史。
 * 放在 store 里而不是组件里：工具窗在最大化 / 还原、换槽时会被重新挂载，地址与历史
 * 不能跟着丢；转录悬浮卡与后台弹层的「应用内预览」也要在窗口还没渲染时就把地址递进来。
 * 只活在内存里，不持久化。
 */
export type PreviewTab = {
  /** 端口标签 `port:<端口>`；手填 / 链接带来的非会话端口地址 `url:<origin>`。 */
  key: string;
  /** 端口标签的端口；临时标签为 null。 */
  port: number | null;
  /** 监听该端口的进程命令（标签名「:端口 命令」里的命令）；临时标签为空。 */
  command: string;
  /** 端口仍在被监听。只有端口标签会变 false；临时标签恒为 true。 */
  listening: boolean;
  history: string[];
  index: number;
  /** 「刷新」/「重新加载」每按一次加一，窗口据此重新预检并重载。 */
  reloadSeq: number;
};

export type SessionPreview = {
  tabs: PreviewTab[];
  activeKey: string | null;
};

export type PortEntry = { port: number; command: string };

type State = { bySession: Record<number, SessionPreview> };

type Actions = {
  /** 用会话此刻监听的端口对齐标签：新端口开标签；消失的端口当前标签保留并标「不再监听」，其余移除。 */
  syncPorts: (sessionId: number, ports: readonly PortEntry[]) => void;
  /** 在窗口里打开一个地址：归到监听该端口的标签，没有就开临时标签，并设为当前。 */
  openUrl: (sessionId: number, url: string) => void;
  /** 当前标签跳到新地址（地址栏提交），截断前进历史。 */
  navigate: (sessionId: number, key: string, url: string) => void;
  back: (sessionId: number, key: string) => void;
  reload: (sessionId: number, key: string) => void;
  activate: (sessionId: number, key: string) => void;
  /** 关临时标签；端口标签随端口存亡，不可手动关。 */
  closeTab: (sessionId: number, key: string) => void;
};

const EMPTY: SessionPreview = { tabs: [], activeKey: null };

export function selectActiveTab(
  session: SessionPreview | undefined,
): PreviewTab | null {
  if (!session) return null;
  return session.tabs.find((t) => t.key === session.activeKey) ?? null;
}

export function currentUrl(tab: PreviewTab): string {
  return tab.history[tab.index];
}

const LOOPBACK_HOSTS = ["localhost", "127.0.0.1", "[::1]"];

/** 地址指向的环回端口（没写端口按协议默认）；不是环回 http(s) 地址时为 null。 */
function loopbackPort(raw: string): number | null {
  try {
    const u = new URL(raw);
    if (u.protocol !== "http:" && u.protocol !== "https:") return null;
    if (!LOOPBACK_HOSTS.includes(u.hostname)) return null;
    if (u.port !== "") return Number(u.port);
    return u.protocol === "https:" ? 443 : 80;
  } catch {
    return null;
  }
}

function portTab(entry: PortEntry): PreviewTab {
  return {
    key: `port:${entry.port}`,
    port: entry.port,
    command: entry.command,
    listening: true,
    history: [`http://localhost:${entry.port}/`],
    index: 0,
    reloadSeq: 0,
  };
}

function adhocTab(url: string): PreviewTab {
  return {
    key: `url:${new URL(url).origin}`,
    port: null,
    command: "",
    listening: true,
    history: [url],
    index: 0,
    reloadSeq: 0,
  };
}

function pushUrl(tab: PreviewTab, url: string): PreviewTab {
  if (tab.history[tab.index] === url) {
    return { ...tab, reloadSeq: tab.reloadSeq + 1 };
  }
  return {
    ...tab,
    history: [...tab.history.slice(0, tab.index + 1), url],
    index: tab.index + 1,
  };
}

function mapTab(
  state: State,
  sessionId: number,
  key: string,
  fn: (tab: PreviewTab) => PreviewTab,
): State | Partial<State> {
  const session = state.bySession[sessionId];
  if (!session || !session.tabs.some((t) => t.key === key)) return state;
  return {
    bySession: {
      ...state.bySession,
      [sessionId]: {
        ...session,
        tabs: session.tabs.map((t) => (t.key === key ? fn(t) : t)),
      },
    },
  };
}

export const useWebPreviewStore = create<State & Actions>((set) => ({
  bySession: {},

  syncPorts: (sessionId, ports) =>
    set((s) => {
      const session = s.bySession[sessionId] ?? EMPTY;
      const live = new Map(ports.map((p) => [p.port, p]));
      let changed = false;
      const tabs: PreviewTab[] = [];
      for (const tab of session.tabs) {
        if (tab.port === null) {
          tabs.push(tab);
          continue;
        }
        const entry = live.get(tab.port);
        live.delete(tab.port);
        if (entry) {
          if (tab.listening && tab.command === entry.command) {
            tabs.push(tab);
          } else {
            changed = true;
            tabs.push({ ...tab, listening: true, command: entry.command });
          }
        } else if (tab.key === session.activeKey) {
          if (tab.listening) {
            changed = true;
            tabs.push({ ...tab, listening: false });
          } else {
            tabs.push(tab);
          }
        } else {
          changed = true; // 非当前标签随端口消失而移除
        }
      }
      for (const entry of live.values()) {
        changed = true;
        tabs.push(portTab(entry));
      }
      if (!changed) return s;
      const activeKey =
        session.activeKey !== null &&
        tabs.some((t) => t.key === session.activeKey)
          ? session.activeKey
          : (tabs[0]?.key ?? null);
      return {
        bySession: { ...s.bySession, [sessionId]: { tabs, activeKey } },
      };
    }),

  openUrl: (sessionId, url) =>
    set((s) => {
      const session = s.bySession[sessionId] ?? EMPTY;
      const port = loopbackPort(url);
      const owner =
        port === null
          ? undefined
          : session.tabs.find((t) => t.port === port && t.listening);
      let tabs: PreviewTab[];
      let key: string;
      if (owner) {
        key = owner.key;
        tabs = session.tabs.map((t) => (t === owner ? pushUrl(t, url) : t));
      } else {
        const fresh = adhocTab(url);
        key = fresh.key;
        tabs = session.tabs.some((t) => t.key === key)
          ? session.tabs.map((t) => (t.key === key ? pushUrl(t, url) : t))
          : [...session.tabs, fresh];
      }
      return {
        bySession: {
          ...s.bySession,
          [sessionId]: { tabs, activeKey: key },
        },
      };
    }),

  navigate: (sessionId, key, url) =>
    set((s) => mapTab(s, sessionId, key, (t) => pushUrl(t, url))),

  back: (sessionId, key) =>
    set((s) => {
      const tab = s.bySession[sessionId]?.tabs.find((t) => t.key === key);
      if (!tab || tab.index === 0) return s;
      return mapTab(s, sessionId, key, (t) => ({ ...t, index: t.index - 1 }));
    }),

  reload: (sessionId, key) =>
    set((s) =>
      mapTab(s, sessionId, key, (t) => ({ ...t, reloadSeq: t.reloadSeq + 1 })),
    ),

  activate: (sessionId, key) =>
    set((s) => {
      const session = s.bySession[sessionId];
      if (!session || session.activeKey === key) return s;
      if (!session.tabs.some((t) => t.key === key)) return s;
      return {
        bySession: {
          ...s.bySession,
          [sessionId]: { ...session, activeKey: key },
        },
      };
    }),

  closeTab: (sessionId, key) =>
    set((s) => {
      const session = s.bySession[sessionId];
      const tab = session?.tabs.find((t) => t.key === key);
      if (!session || !tab || tab.port !== null) return s;
      const tabs = session.tabs.filter((t) => t.key !== key);
      const activeKey =
        session.activeKey === key ? (tabs[0]?.key ?? null) : session.activeKey;
      return {
        bySession: { ...s.bySession, [sessionId]: { tabs, activeKey } },
      };
    }),
}));

/**
 * 「应用内预览」入口（转录悬浮卡、后台弹层的端口）：把地址递给网页预览窗并显示它。
 * 窗口还没渲染也没关系——地址已在 store 里，窗口出现时直接读。
 */
export function openWebPreview(sessionId: number, url: string) {
  useWebPreviewStore.getState().openUrl(sessionId, url);
  useToolWindowLayout.getState().show("webPreview");
}
