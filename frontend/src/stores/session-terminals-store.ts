import { create } from "zustand";

import { readSessionRoot } from "./session-root-store";

/**
 * 会话终端工具窗里的一个终端标签（spec「终端工具窗」）。
 *
 * 状态按会话分开存、而不是挂在某个组件上：终端进程的寿命必须长于工具窗的显示——
 * 隐藏工具窗、切到别的会话都不结束进程，只有关掉标签（或进程自己退出）才结束。
 * 项目列表打开的「项目终端」没有会话可依附，仍是 chat-tabs-store 里的独立标签页。
 */
export type SessionTerminal = {
  /** 就是 terminalId：PTY 与 `terminal:<id>:data/exit` 事件都按它寻址。 */
  id: string;
  /**
   * 起始工作根的规范标识（主根 = 空串，其余为绝对路径，与 `useSessionRoot.rootArg`
   * 同口径）。创建后不再变：工作根变化时已打开的终端不会自动 `cd`。
   */
  root: string;
  /** 起始工作根的绝对路径，标签名取根名用。接入的进程不知道它的根，为空串。 */
  rootPath: string;
  /** 会话内的序号（从 1 起，只增不减），默认标题「终端 N」不重名。 */
  seq: number;
  /** 接管转录卡片上已在跑的进程：不开、不替它关 PTY。 */
  attach?: boolean;
  command?: string;
  /** 进程已退出（如远端断线）但标签留着让用户看旧输出。 */
  exited: boolean;
};

type Entry = {
  tabs: SessionTerminal[];
  activeId: string | null;
  /** 下一个序号。 */
  nextSeq: number;
};

type State = {
  bySession: Record<number, Entry>;
  /**
   * 该会话的终端工具窗此刻是否画在屏幕上。进程不随它起落（终端面板常驻在工具窗
   * 之外），它只决定面板要不要按可见尺寸适配 / 抢焦点。
   */
  viewing: Record<number, boolean>;
  setViewing: (sessionId: number, on: boolean) => void;
  /** 在会话当前工作根新开一个终端并激活，返回它的 id。 */
  openInCurrentRoot: (sessionId: number) => string;
  /** 把转录卡片上在跑的进程接成新标签并激活；已接入过则只激活。 */
  attach: (
    sessionId: number,
    args: { terminalId: string; command?: string },
  ) => void;
  close: (sessionId: number, id: string) => void;
  setActive: (sessionId: number, id: string) => void;
  markExited: (sessionId: number, id: string) => void;
};

const EMPTY: Entry = { tabs: [], activeId: null, nextSeq: 1 };

let idSeq = 0;
function nextTerminalId(): string {
  idSeq += 1;
  return `sess-term-${Date.now().toString(36)}-${idSeq}`;
}

function entryOf(state: State, sessionId: number): Entry {
  return state.bySession[sessionId] ?? EMPTY;
}

export const useSessionTerminalsStore = create<State>()((set) => {
  const put = (state: State, sessionId: number, entry: Entry) => ({
    bySession: { ...state.bySession, [sessionId]: entry },
  });

  return {
    bySession: {},
    viewing: {},
    setViewing: (sessionId, on) =>
      set((state) =>
        (state.viewing[sessionId] ?? false) === on
          ? state
          : { viewing: { ...state.viewing, [sessionId]: on } },
      ),
    openInCurrentRoot: (sessionId) => {
      const id = nextTerminalId();
      const { arg, path } = readSessionRoot(sessionId);
      set((state) => {
        const e = entryOf(state, sessionId);
        const tab: SessionTerminal = {
          id,
          root: arg,
          rootPath: path,
          seq: e.nextSeq,
          exited: false,
        };
        return put(state, sessionId, {
          tabs: [...e.tabs, tab],
          activeId: id,
          nextSeq: e.nextSeq + 1,
        });
      });
      return id;
    },
    attach: (sessionId, { terminalId, command }) =>
      set((state) => {
        const e = entryOf(state, sessionId);
        if (e.tabs.some((t) => t.id === terminalId)) {
          return put(state, sessionId, { ...e, activeId: terminalId });
        }
        const tab: SessionTerminal = {
          id: terminalId,
          root: "",
          rootPath: "",
          seq: e.nextSeq,
          attach: true,
          command,
          exited: false,
        };
        return put(state, sessionId, {
          tabs: [...e.tabs, tab],
          activeId: terminalId,
          nextSeq: e.nextSeq + 1,
        });
      }),
    close: (sessionId, id) =>
      set((state) => {
        const e = entryOf(state, sessionId);
        const index = e.tabs.findIndex((t) => t.id === id);
        if (index < 0) return state;
        const tabs = e.tabs.filter((t) => t.id !== id);
        // 关掉的是当前标签时落到相邻的一个（优先右边），与浏览器标签栏一致。
        const activeId =
          e.activeId === id
            ? (tabs[Math.min(index, tabs.length - 1)]?.id ?? null)
            : e.activeId;
        return put(state, sessionId, { ...e, tabs, activeId });
      }),
    setActive: (sessionId, id) =>
      set((state) => {
        const e = entryOf(state, sessionId);
        if (e.activeId === id || !e.tabs.some((t) => t.id === id)) return state;
        return put(state, sessionId, { ...e, activeId: id });
      }),
    markExited: (sessionId, id) =>
      set((state) => {
        const e = entryOf(state, sessionId);
        const tab = e.tabs.find((t) => t.id === id);
        if (!tab || tab.exited) return state;
        return put(state, sessionId, {
          ...e,
          tabs: e.tabs.map((t) => (t.id === id ? { ...t, exited: true } : t)),
        });
      }),
  };
});

/** 该会话此刻有没有存活的终端进程（图标条圆点的依据）。 */
export function hasLiveSessionTerminal(entry: Entry | undefined): boolean {
  return entry?.tabs.some((t) => !t.exited) ?? false;
}
