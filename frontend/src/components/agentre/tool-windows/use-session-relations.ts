import * as React from "react";

import {
  IssueListBySession,
  ListSessionDispatches,
  ListSessionsSharingRoot,
} from "@/../wailsjs/go/app/App";
import { EventsOn } from "@/../wailsjs/runtime/runtime";
import { useSessionRootStore } from "@/stores/session-root-store";
import {
  useSessionStatus,
  useSessionStatusStore,
} from "@/stores/session-status-store";

import type { app, chat_svc } from "../../../../wailsjs/go/models";

/**
 * 关联工具窗的四组数据（spec「关联工具窗」）：本会话关联的看板任务、派发我的、我派发的、
 * 同一份代码上在跑的其它会话。
 *
 * 数据在对话页的工具窗骨架里取一次（窗口收起时图标条也要数量与提醒圆点），窗口只读它。
 * 四组各取各的：某一组取不到时保留它已有的内容，不拖累其它组。
 */
export type SessionRelations = {
  issues: app.SessionLinkedIssue[];
  dispatchedBy: chat_svc.DispatchedByItem[];
  dispatched: chat_svc.DispatchedItem[];
  sharing: chat_svc.SharingRootSession[];
  /** 标题栏的数量：四组条目数之和。 */
  count: number;
  /** 同一份代码上有对方也写过的文件：图标条上亮提醒色圆点。 */
  overlapAlert: boolean;
  reload: () => void;
};

type Params = {
  sessionId: number;
  /** 新建会话提示页等不显示工具窗的场合不取数。 */
  enabled?: boolean;
};

type Groups = Pick<
  SessionRelations,
  "issues" | "dispatchedBy" | "dispatched" | "sharing"
>;

const NO_GROUPS: Groups = {
  issues: [],
  dispatchedBy: [],
  dispatched: [],
  sharing: [],
};

const NO_STIRRED: ReadonlySet<number> = new Set();

/**
 * 与 `chat_svc.RunStateEvent` 同名：本桌面端上某条会话开始 / 不再运行（轮次起止、后台
 * 活动集合空与非空翻转、停止翻回 idle、删除），载荷是会话 id。应用级事件，不依赖那条会话
 * 开着对话面板 —— 被 agrctl send、agent 派发或别的宿主拉起的会话也发。
 */
const RUN_STATE_EVENT = "chat:run-state";

/** 正在跑（含等待用户、后台活动）的其它会话 id，升序拼成键：集合变了键才变。 */
function runningPeersKey(
  statuses: ReadonlyMap<number, { agentStatus: string; bgRunning: boolean }>,
  selfId: number,
): string {
  const ids: number[] = [];
  statuses.forEach((v, id) => {
    if (
      id !== selfId &&
      (v.agentStatus === "running" ||
        v.agentStatus === "waiting" ||
        v.bgRunning)
    ) {
      ids.push(id);
    }
  });
  return ids.sort((a, b) => a - b).join(",");
}

export function useSessionRelations({
  sessionId,
  enabled = true,
}: Params): SessionRelations {
  const [loaded, setLoaded] = React.useState<{
    sessionId: number;
    groups: Groups;
  }>({ sessionId, groups: NO_GROUPS });
  const [tick, setTick] = React.useState(0);
  const genRef = React.useRef(0);
  const reload = React.useCallback(() => setTick((n) => n + 1), []);
  // 经 RUN_STATE_EVENT 得知运行态变过的其它会话（按本会话记）：运行态 store 里不一定有
  // 它们（没挂对话面板），也要听它们的工作根推送，它们走进本会话的根时才重取得到。
  const [stirred, setStirred] = React.useState<{
    sessionId: number;
    ids: ReadonlySet<number>;
  }>({ sessionId, ids: new Set() });

  const doneTick = useSessionStatus(sessionId)?.doneTick ?? 0;
  const runningKey = useSessionStatusStore((s) =>
    runningPeersKey(s.statuses, sessionId),
  );

  // 本会话 AI 所在的工作根：它的变化来自会话根 store（useSessionRootSync 订阅了
  // chat:workroots:<本会话> 并写进去），在这里读 store 而不是再订阅一遍同名事件。
  const selfAiRoot = useSessionRootStore(
    (s) => s.bySession[sessionId]?.roots.find((r) => r.aiHere)?.path ?? "",
  );

  const active = enabled && sessionId > 0;

  React.useEffect(() => {
    if (!active) return;
    genRef.current += 1;
    const gen = genRef.current;
    // 绑定在 then 里才被调用：同步抛出（绑定缺失）也落成该组的取数失败，不炸掉渲染。
    const call = <T>(f: () => Promise<T>) => Promise.resolve().then(f);
    void Promise.allSettled([
      call(() => IssueListBySession(sessionId)),
      call(() => ListSessionDispatches(sessionId)),
      call(() => ListSessionsSharingRoot(sessionId)),
    ]).then(([issues, dispatches, sharing]) => {
      // 推送 / 状态变化触发的新一轮取数比在途的新：晚到的作废。
      if (genRef.current !== gen) return;
      setLoaded((prev) => {
        const base = prev.sessionId === sessionId ? prev.groups : NO_GROUPS;
        return {
          sessionId,
          groups: {
            issues:
              issues.status === "fulfilled"
                ? (issues.value ?? [])
                : base.issues,
            dispatchedBy:
              dispatches.status === "fulfilled"
                ? (dispatches.value?.dispatchedBy ?? [])
                : base.dispatchedBy,
            dispatched:
              dispatches.status === "fulfilled"
                ? (dispatches.value?.dispatched ?? [])
                : base.dispatched,
            sharing:
              sharing.status === "fulfilled"
                ? (sharing.value ?? [])
                : base.sharing,
          },
        };
      });
    });
  }, [active, sessionId, tick, doneTick, runningKey, selfAiRoot]);

  const groups = loaded.sessionId === sessionId ? loaded.groups : NO_GROUPS;

  // 其它会话开始 / 不再运行：重取。运行态 store 只覆盖挂着面板或侧栏快照里的会话，
  // 这条应用级事件覆盖其余的（agrctl send、agent 派发、别的宿主拉起的、后台活动收尾的）。
  React.useEffect(() => {
    if (!active) return;
    return EventsOn(RUN_STATE_EVENT, (payload: unknown) => {
      const id = Number(payload);
      if (!Number.isInteger(id) || id <= 0 || id === sessionId) return;
      setStirred((prev) => {
        const base =
          prev.sessionId === sessionId ? prev.ids : new Set<number>();
        if (prev.sessionId === sessionId && base.has(id)) return prev;
        return { sessionId, ids: new Set(base).add(id) };
      });
      reload();
    });
  }, [active, sessionId, reload]);

  const stirredIds = stirred.sessionId === sessionId ? stirred.ids : NO_STIRRED;

  // 订阅其它会话的工作根推送:列表里的同伴、此刻正在跑的会话(store 里的与经运行态事件
  // 得知的)——任何一个换了所在的根，「谁和我在同一份代码上」都可能变。每个推送只触发
  // 一次重取，不解析载荷。
  const watchKey = React.useMemo(() => {
    const ids = new Set<number>(stirredIds);
    for (const part of runningKey.split(",")) {
      if (part !== "") ids.add(Number(part));
    }
    for (const s of groups.sharing) ids.add(s.sessionId);
    return [...ids].sort((a, b) => a - b).join(",");
  }, [runningKey, groups.sharing, stirredIds]);

  React.useEffect(() => {
    if (!active) return;
    const offs = watchKey
      .split(",")
      .filter((part) => part !== "")
      .map((id) => EventsOn(`chat:workroots:${id}`, () => reload()));
    return () => {
      for (const off of offs) off();
    };
  }, [active, watchKey, reload]);

  return React.useMemo<SessionRelations>(
    () => ({
      ...groups,
      count:
        groups.issues.length +
        groups.dispatchedBy.length +
        groups.dispatched.length +
        groups.sharing.length,
      overlapAlert: groups.sharing.some((s) => s.overlapCount > 0),
      reload,
    }),
    [groups, reload],
  );
}
