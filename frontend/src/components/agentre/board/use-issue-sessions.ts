import * as React from "react";

import type { BoardLinkedSession } from "@agentre-hub/agentre-ui";

import { useIssueLinksStore } from "@/stores/issue-links-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { IssueSessionsOf } from "../../../../wailsjs/go/app/App";
import type { app } from "../../../../wailsjs/go/models";

type Row = app.IssueLinkedSession;

function toStatus(raw: string | undefined): BoardLinkedSession["status"] {
  return raw === "running" || raw === "waiting" || raw === "error"
    ? raw
    : "idle";
}

/**
 * 看板卡片下方的关联会话：对每张可见的卡取 `IssueSessionsOf`，在卡片集合变化或关联
 * 变化（`useIssueLinksStore`）时重取；运行状态叠上会话运行态 store 的实时值，
 * 跑起来 / 出错不必等下一次重取。
 */
export function useIssueSessions(
  issueIds: number[],
): (issueId: number) => BoardLinkedSession[] | undefined {
  const revision = useIssueLinksStore((s) => s.revision);
  const live = useSessionStatusStore((s) => s.statuses);
  const [rows, setRows] = React.useState<ReadonlyMap<number, Row[]>>(
    () => new Map(),
  );
  const idsKey = issueIds.join(",");

  React.useEffect(() => {
    const ids = idsKey ? idsKey.split(",").map(Number) : [];
    let cancelled = false;
    void Promise.all(
      ids.map(async (id): Promise<[number, Row[]]> => {
        try {
          return [id, (await IssueSessionsOf(id)) ?? []];
        } catch (cause) {
          // 取不到就不画这一块，不拖累看板本身。
          console.error("[board] issue sessions load failed", id, cause);
          return [id, []];
        }
      }),
    ).then((entries) => {
      if (!cancelled) setRows(new Map(entries));
    });
    return () => {
      cancelled = true;
    };
    // revision 变 = 关联变了，必须重取。
  }, [idsKey, revision]);

  return React.useCallback(
    (issueId: number) => {
      const list = rows.get(issueId);
      if (!list || list.length === 0) return undefined;
      return list.map((row) => ({
        id: row.sessionID,
        title: row.title,
        agentName: row.agentName || undefined,
        status: toStatus(
          live.get(row.sessionID)?.agentStatus ?? row.agentStatus,
        ),
      }));
    },
    [rows, live],
  );
}
