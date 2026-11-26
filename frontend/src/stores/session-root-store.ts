import * as React from "react";
import { create } from "zustand";
import { persist } from "zustand/middleware";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

export type WorkRoot = workspace_fs_svc.WorkRootView;

/** 「已跟随 / 已移除」提示在胶囊下方停留多久（spec：约 5 秒）。 */
export const SESSION_ROOT_NOTICE_MS = 5000;

/** 本机存储里这份状态的键。只存「已移除」清单，见 persist 配置。 */
export const SESSION_ROOTS_STORAGE_KEY = "session-roots-state";

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
  /**
   * 从集合中消失、至今没被认领回来的根（变更窗的「已移除」分组）：这一页看着它消失的，
   * 加上后端取数带回的、本进程里已移除的根（会话没开着时被移除）。后端在根消失时删掉
   * 它的认领行、移除清单只在它的内存里，所以这一项跨重新加载与重启保存在本机；其余
   * 字段不保存（spec：跟随与当前根重启后回到默认）。
   */
  removed: WorkRoot[];
  /**
   * 集合每落一次加一（取数、推送、点链接认领都走 applyRoots）。在途的取数据此判断
   * 自己发出之后有没有更新的集合落下——晚到的旧结果必须作废。
   */
  rev: number;
};

type State = {
  bySession: Record<number, SessionRootEntry>;
  /**
   * 认下会话的工作目录：没有状态、或目录变了时从默认重新开始（跟随开、主根）；
   * 目录没变则什么也不改。集合取回之前当前根因此已经是 cwd。
   */
  attach: (sessionId: number, cwd: string) => void;
  /**
   * 一份新的认领集合（首次取数、轮末重取或 `chat:workroots` 推送）。取数在集合之后
   * 可能接着 `removed: true` 的项：它们不是可用的根，只进「已移除」清单。
   */
  applyRoots: (sessionId: number, cwd: string, roots: WorkRoot[]) => void;
  /** 用户在下拉里手动选根：切过去并关闭跟随。 */
  select: (sessionId: number, path: string) => void;
  /** 开关「跟随 AI 切换」；打开时立即切到 AI 所在的根。 */
  setFollow: (sessionId: number, on: boolean) => void;
  /** 已跟随提示上的「留在 <原根>」：回到原根并关闭跟随。 */
  stay: (sessionId: number) => void;
  dismissNotice: (sessionId: number, id: number) => void;
  /** 会话被删除：丢掉它的整份状态。 */
  forget: (sessionId: number) => void;
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
    rev: 0,
  };
}

const EMPTY_ENTRY = freshEntry("");

/** 存进本机的那一部分：会话 cwd（目录变了清单作废）与已移除的根。 */
type PersistedEntry = { cwd: string; removed: WorkRoot[] };

function isWorkRoot(v: unknown): v is WorkRoot {
  const r = v as Partial<WorkRoot> | null;
  return (
    typeof r === "object" &&
    r !== null &&
    typeof r.path === "string" &&
    r.path !== "" &&
    typeof r.name === "string"
  );
}

/** 读回的值可能来自更早的版本或被手改过：逐项丢掉坏值，只留下有已移除根的会话。 */
function restoreEntries(saved: unknown): Record<number, SessionRootEntry> {
  const out: Record<number, SessionRootEntry> = {};
  if (typeof saved !== "object" || saved === null) return out;
  for (const [key, value] of Object.entries(saved)) {
    const id = Number(key);
    const e = value as Partial<PersistedEntry> | null;
    if (!Number.isInteger(id) || typeof e !== "object" || e === null) continue;
    if (typeof e.cwd !== "string" || !Array.isArray(e.removed)) continue;
    const removed = e.removed.filter(isWorkRoot).map(asRemoved);
    if (removed.length === 0) continue;
    out[id] = { ...freshEntry(e.cwd), removed };
  }
  return out;
}

/** 「已移除」清单里的一项：只留名称与类型。 */
function asRemoved(r: WorkRoot): WorkRoot {
  return {
    path: r.path,
    name: r.name,
    isWorktree: r.isWorktree === true,
    isPrimary: false,
    aiHere: false,
  };
}

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
 *   - 消失的根与后端报告已移除的根进「已移除」清单，被认领回来时撤掉。
 */
function reduceRoots(
  prev: SessionRootEntry,
  reported: WorkRoot[],
): SessionRootEntry {
  const roots = reported.filter((r) => !r.removed);
  const paths = new Set(roots.map((r) => r.path));
  const primary = roots.find((r) => r.isPrimary) ?? null;
  const ai = roots.find((r) => r.aiHere) ?? null;
  const gone = prev.roots.filter((r) => !paths.has(r.path) && !r.isPrimary);
  const removed: WorkRoot[] = [];
  for (const r of [
    ...prev.removed,
    ...gone,
    ...reported.filter((x) => x.removed),
  ]) {
    if (paths.has(r.path) || removed.some((x) => x.path === r.path)) continue;
    removed.push(asRemoved(r));
  }
  const next: SessionRootEntry = {
    ...prev,
    roots,
    loaded: true,
    removed,
    rev: prev.rev + 1,
  };

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

export const useSessionRootStore = create<State>()(
  persist(
    (set) => {
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
                  bySession: {
                    ...state.bySession,
                    [sessionId]: freshEntry(cwd),
                  },
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
        forget: (sessionId) =>
          set((state) => {
            if (!(sessionId in state.bySession)) return state;
            const bySession = { ...state.bySession };
            delete bySession[sessionId];
            return { bySession };
          }),
      };
    },
    {
      name: SESSION_ROOTS_STORAGE_KEY,
      partialize: (state) => {
        const bySession: Record<number, PersistedEntry> = {};
        for (const [id, e] of Object.entries(state.bySession)) {
          if (e.removed.length > 0) {
            bySession[Number(id)] = { cwd: e.cwd, removed: e.removed };
          }
        }
        return { bySession };
      },
      merge: (persisted, current) => ({
        ...current,
        bySession: restoreEntries(
          (persisted as { bySession?: unknown } | null)?.bySession,
        ),
      }),
    },
  ),
);

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

/** 会话集合的当前版本（见 SessionRootEntry.rev）；没有状态的会话为 0。 */
export function readSessionRootsRev(sessionId: number): number {
  return useSessionRootStore.getState().bySession[sessionId]?.rev ?? 0;
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
