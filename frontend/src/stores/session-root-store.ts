import * as React from "react";
import { create } from "zustand";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

export type WorkRoot = workspace_fs_svc.WorkRootView;

/** 「已跟随 / 已移除」提示在胶囊下方停留多久（spec：约 5 秒）。 */
export const SESSION_ROOT_NOTICE_MS = 5000;

/**
 * 胶囊下方的一次性提示。`id` 让到期计时只收它自己那一条：计时期间换上来的新提示
 * 不会被旧计时收走。
 */
export type RootNotice =
  | { id: number; kind: "followed"; to: WorkRoot; from: WorkRoot }
  | { id: number; kind: "removed"; removed: WorkRoot; primary: WorkRoot };

type SessionRootEntry = {
  /** 这份状态属于哪个会话工作目录；目录变了整份重来。 */
  cwd: string;
  /** 最近一次的认领集合（后端顺序，主根在前）。 */
  roots: WorkRoot[];
  /** 是否已经拿到过一次集合：第一次落到 AI 所在根不算「切换」，不提示。 */
  loaded: boolean;
  /** 被选中的根的绝对路径；null = 主根（没有集合时即会话 cwd）。 */
  selected: string | null;
  follow: boolean;
  notice: RootNotice | null;
  /** 本次运行里从集合中消失、至今没被认领回来的根（变更窗的「已移除」分组）。 */
  removed: WorkRoot[];
};

type State = {
  bySession: Record<number, SessionRootEntry>;
  /**
   * 认下会话的工作目录：没有状态、或目录变了时从默认重新开始（跟随开、主根）；
   * 目录没变则什么也不改。集合取回之前当前根因此已经是 cwd。
   */
  attach: (sessionId: number, cwd: string) => void;
  /** 一份新的认领集合（首次取数、轮末重取或 `chat:workroots` 推送）。 */
  applyRoots: (sessionId: number, cwd: string, roots: WorkRoot[]) => void;
  /** 用户在下拉里手动选根：切过去并关闭跟随。 */
  select: (sessionId: number, path: string) => void;
  /** 开关「跟随 AI 切换」；打开时立即切到 AI 所在的根。 */
  setFollow: (sessionId: number, on: boolean) => void;
  /** 已跟随提示上的「留在 <原根>」：回到原根并关闭跟随。 */
  stay: (sessionId: number) => void;
  dismissNotice: (sessionId: number, id: number) => void;
};

function freshEntry(cwd: string): SessionRootEntry {
  return {
    cwd,
    roots: [],
    loaded: false,
    selected: null,
    follow: true,
    notice: null,
    removed: [],
  };
}

const EMPTY_ENTRY = freshEntry("");

/** 当前根：被选中且仍在集合里的那个根；否则主根（还没有集合时为 null）。 */
function currentRootOf(entry: SessionRootEntry): WorkRoot | null {
  if (entry.selected !== null) {
    const picked = entry.roots.find((r) => r.path === entry.selected);
    if (picked) return picked;
  }
  return entry.roots.find((r) => r.isPrimary) ?? null;
}

let noticeSeq = 0;

function nextNoticeId(): number {
  noticeSeq += 1;
  return noticeSeq;
}

/**
 * 一份新集合落到会话状态上。规则（spec「跟随」「worktree 被移除」）：
 *   - 第一次拿到集合：跟随开着就落在 AI 所在的根，不提示；
 *   - 当前根从集合里消失：回到主根，提示一次移除；
 *   - 跟随开着且 AI 所在的根不是当前根：切过去，提示「已跟随」并记下原根；
 *   - 消失的根进「已移除」清单，被认领回来时撤掉。
 */
function reduceRoots(
  prev: SessionRootEntry,
  roots: WorkRoot[],
): SessionRootEntry {
  const paths = new Set(roots.map((r) => r.path));
  const primary = roots.find((r) => r.isPrimary) ?? null;
  const ai = roots.find((r) => r.aiHere) ?? null;
  const gone = prev.roots.filter((r) => !paths.has(r.path) && !r.isPrimary);
  const removed = [
    ...prev.removed.filter(
      (r) => !paths.has(r.path) && !gone.some((g) => g.path === r.path),
    ),
    ...gone,
  ];
  const next: SessionRootEntry = { ...prev, roots, loaded: true, removed };

  if (!prev.loaded) {
    if (prev.follow && ai) next.selected = ai.path;
    return next;
  }

  const prevCurrent = currentRootOf(prev);
  if (prevCurrent && !paths.has(prevCurrent.path) && !prevCurrent.isPrimary) {
    next.selected = null;
    next.notice = primary
      ? { id: nextNoticeId(), kind: "removed", removed: prevCurrent, primary }
      : null;
    return next;
  }

  if (prev.follow && ai && ai.path !== prevCurrent?.path) {
    next.selected = ai.path;
    const from = prevCurrent ?? primary;
    next.notice = from
      ? { id: nextNoticeId(), kind: "followed", to: ai, from }
      : null;
  }
  return next;
}

export const useSessionRootStore = create<State>()((set) => {
  const update = (
    sessionId: number,
    fn: (entry: SessionRootEntry) => SessionRootEntry,
  ) =>
    set((state) => {
      const entry = state.bySession[sessionId];
      if (!entry) return state;
      return { bySession: { ...state.bySession, [sessionId]: fn(entry) } };
    });

  return {
    bySession: {},
    attach: (sessionId, cwd) =>
      set((state) =>
        state.bySession[sessionId]?.cwd === cwd
          ? state
          : {
              bySession: { ...state.bySession, [sessionId]: freshEntry(cwd) },
            },
      ),
    applyRoots: (sessionId, cwd, roots) =>
      set((state) => {
        const existing = state.bySession[sessionId];
        const prev =
          existing && existing.cwd === cwd ? existing : freshEntry(cwd);
        return {
          bySession: {
            ...state.bySession,
            [sessionId]: reduceRoots(prev, roots),
          },
        };
      }),
    select: (sessionId, path) =>
      update(sessionId, (e) => ({
        ...e,
        selected: path,
        follow: false,
        notice: null,
      })),
    setFollow: (sessionId, on) =>
      update(sessionId, (e) => {
        if (!on) return { ...e, follow: false };
        const ai = e.roots.find((r) => r.aiHere);
        return {
          ...e,
          follow: true,
          selected: ai ? ai.path : e.selected,
          notice: null,
        };
      }),
    stay: (sessionId) =>
      update(sessionId, (e) => {
        if (e.notice?.kind !== "followed") return e;
        return {
          ...e,
          selected: e.notice.from.path,
          follow: false,
          notice: null,
        };
      }),
    dismissNotice: (sessionId, id) =>
      update(sessionId, (e) =>
        e.notice?.id === id ? { ...e, notice: null } : e,
      ),
  };
});

/**
 * 会话级当前工作根（spec 决策 7、12）。
 *
 * - `current` 是当前根的**绝对路径**（拼本机路径、显示用）；还没有集合时是会话 cwd。
 * - `rootArg` 是当前根的**规范标识**：主根恒为空串，其余根是绝对路径。交给
 *   `workspacefs.*` 绑定与预览标签（`FilePreviewTab.root`）的都是它——主根只有这一种
 *   表示，同一个文件不会因为「空串 / 绝对 cwd」两种写法开出两个标签。
 */
export type SessionRoot = {
  roots: WorkRoot[];
  current: string;
  currentRoot: WorkRoot | null;
  rootArg: string;
  /** AI 当前所在的根；位置不在任何根内时为 null。 */
  aiRoot: WorkRoot | null;
  follow: boolean;
  notice: RootNotice | null;
  removed: WorkRoot[];
  select: (path: string) => void;
  setFollow: (on: boolean) => void;
  stay: () => void;
  dismissNotice: (id: number) => void;
};

function rootArgOf(entry: SessionRootEntry): string {
  const root = currentRootOf(entry);
  return root && !root.isPrimary ? root.path : "";
}

/**
 * 非渲染场景（事件回调、store 动作）一次性读会话当前根：`arg` 是规范标识（主根为
 * 空串），`path` 是绝对路径（还没有集合时是会话 cwd，没记录过的会话为空串）。
 */
export function readSessionRoot(sessionId: number): {
  arg: string;
  path: string;
} {
  const entry = useSessionRootStore.getState().bySession[sessionId];
  if (!entry) return { arg: "", path: "" };
  return {
    arg: rootArgOf(entry),
    path: currentRootOf(entry)?.path ?? entry.cwd,
  };
}

/** 只要当前根的规范标识时用它：列表的每一行都订阅，不该为别的字段变化重渲染。 */
export function useSessionRootArg(sessionId: number): string {
  return useSessionRootStore((s) =>
    rootArgOf(s.bySession[sessionId] ?? EMPTY_ENTRY),
  );
}

export function useSessionRoot(sessionId: number): SessionRoot {
  const entry =
    useSessionRootStore((s) => s.bySession[sessionId]) ?? EMPTY_ENTRY;
  // 动作经 getState() 现取：它们是稳定的，不该作为依赖让返回值随任何会话的变化换身份。
  const actions = useSessionRootStore.getState;
  return React.useMemo(() => {
    const currentRoot = currentRootOf(entry);
    return {
      roots: entry.roots,
      current: currentRoot?.path ?? entry.cwd,
      currentRoot,
      rootArg: rootArgOf(entry),
      aiRoot: entry.roots.find((r) => r.aiHere) ?? null,
      follow: entry.follow,
      notice: entry.notice,
      removed: entry.removed,
      select: (path: string) => actions().select(sessionId, path),
      setFollow: (on: boolean) => actions().setFollow(sessionId, on),
      stay: () => actions().stay(sessionId),
      dismissNotice: (id: number) => actions().dismissNotice(sessionId, id),
    };
  }, [entry, sessionId, actions]);
}
