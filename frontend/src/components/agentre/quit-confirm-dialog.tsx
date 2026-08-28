// QuitConfirmDialog 是退出 App 的二次确认框。后端 OnBeforeClose 检测到还有
// 进行中(running|waiting)的会话时拦截退出并 emit "app:quit-blocked"{count},
// 本组件据此弹框;用户点「仍然退出」→ App.ConfirmQuit() 真正退出,点「取消」→ 关闭。
//
// 常驻挂载在 App 根(<ChatStreamsHost/> 旁),跨路由存活。会话列表从 session-status-store
// (运行态)+ session-meta-store(展示名)就地派生 —— count 以后端 payload 为准。

import { LogOut, TriangleAlert } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

import { AgentreDialog, Button } from "@agentre-hub/agentre-ui";
import { AgentAvatar, StatusPill } from "@/components/agentre/primitives";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { ConfirmQuit } from "../../../wailsjs/go/app/App";
import { EventsOn } from "../../../wailsjs/runtime/runtime";

import type { AgentColor } from "@/components/agentre/types";
import type { AgentStatus } from "@/stores/types";

const ACTIVE_STATUSES: AgentStatus[] = ["running", "waiting"];
const MAX_VISIBLE = 3;

type ActiveRow = {
  sessionId: number;
  status: AgentStatus;
  agentName: string;
  agentColor: string;
  title: string;
};

export function QuitConfirmDialog() {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [count, setCount] = useState(0);

  const statuses = useSessionStatusStore((s) => s.statuses);
  const metas = useSessionMetaStore((s) => s.metas);

  useEffect(() => {
    const off = EventsOn("app:quit-blocked", (payload?: { count?: number }) => {
      setCount(payload?.count ?? 0);
      setOpen(true);
    });
    return () => {
      if (typeof off === "function") off();
    };
  }, []);

  const active = useMemo<ActiveRow[]>(() => {
    const rows: ActiveRow[] = [];
    for (const [sessionId, status] of statuses) {
      if (!ACTIVE_STATUSES.includes(status.agentStatus)) continue;
      const meta = metas.get(sessionId);
      rows.push({
        sessionId,
        status: status.agentStatus,
        agentName: meta?.agentName ?? t("quitConfirm.unknownSession"),
        agentColor: meta?.agentColor ?? "neutral",
        title: meta?.title ?? "",
      });
    }
    return rows;
  }, [statuses, metas, t]);

  const visible = active.slice(0, MAX_VISIBLE);
  const overflow = active.length - visible.length;

  const handleConfirm = () => {
    setOpen(false);
    void ConfirmQuit();
  };

  return (
    <AgentreDialog
      open={open}
      onOpenChange={setOpen}
      title={
        <span className="flex items-center gap-2">
          <TriangleAlert className="size-[18px] text-status-waiting" />
          {t("quitConfirm.title")}
        </span>
      }
      description={t("quitConfirm.description", { count })}
      footer={
        <>
          <Button variant="outline" onClick={() => setOpen(false)}>
            {t("quitConfirm.cancel")}
          </Button>
          <Button variant="destructive" onClick={handleConfirm}>
            <LogOut />
            {t("quitConfirm.quitAnyway")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-2">
        <p className="font-mono text-2xs font-semibold tracking-wide text-muted-foreground">
          {t("quitConfirm.runningSessions", { count })}
        </p>
        <div className="flex max-h-[168px] flex-col gap-2 overflow-y-auto">
          {visible.map((row) => (
            <div
              key={row.sessionId}
              className="flex items-center gap-2.5 rounded-md border border-border bg-card px-3 py-2.5"
            >
              <AgentAvatar
                name={row.agentName}
                color={row.agentColor as AgentColor}
                size="sm"
              />
              <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="truncate text-sm font-semibold text-foreground">
                  {row.agentName}
                </span>
                {row.title ? (
                  <span className="truncate text-xs text-muted-foreground">
                    {row.title}
                  </span>
                ) : null}
              </div>
              <StatusPill
                status={row.status}
                label={
                  row.status === "running"
                    ? t("quitConfirm.statusRunning")
                    : t("quitConfirm.statusWaiting")
                }
              />
            </div>
          ))}
          {overflow > 0 ? (
            <p className="px-1 py-1 text-center text-xs font-medium text-muted-foreground">
              {t("quitConfirm.more", { count: overflow })}
            </p>
          ) : null}
        </div>
      </div>
    </AgentreDialog>
  );
}
