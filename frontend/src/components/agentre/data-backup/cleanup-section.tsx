import * as React from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import {
  SessionCleanupSection,
  type CleanupExecuteView,
} from "@agentre-hub/agentre-ui";

import { useProjectList } from "@/hooks/use-project-list";
import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { reloadSidebarSources } from "@/stores/sidebar-reload";

import { createDesktopCleanupPorts, newCleanupMemory } from "./cleanup-ports";

/** 关掉指向已删会话的标签页（同 session-actions 的 confirmDelete），返回关了几个。 */
function closeTabsOf(deleted: number[]): number {
  const gone = new Set(deleted);
  const tabIds = useChatTabsStore
    .getState()
    .tabs.filter(
      (tab) => tab.meta.kind === "session" && gone.has(tab.meta.sessionId),
    )
    .map((tab) => tab.id);
  for (const id of tabIds) useChatTabsStore.getState().closeTab(id);
  return tabIds.length;
}

export function CleanupSection() {
  const { t } = useTranslation();
  const agents = useChatAgentsStore((s) => s.agents);
  const { projects } = useProjectList();

  const [memory] = React.useState(newCleanupMemory);
  const ports = React.useMemo(
    () =>
      createDesktopCleanupPorts({
        memory,
        t,
        agents: agents.map((a) => ({ id: String(a.id), label: a.name })),
        projects: projects.map((p) => ({ id: String(p.id), label: p.name })),
        onDone: (result: CleanupExecuteView, deletedIds: number[]) => {
          const closed = closeTabsOf(deletedIds);
          reloadSidebarSources();
          const lines = result.pending.map((p) =>
            t("dataBackup.cleanup.toast.pending", {
              machine: p.label,
              count: p.count,
            }),
          );
          if (closed > 0) {
            lines.push(
              t("dataBackup.cleanup.toast.closedTabs", { count: closed }),
            );
          }
          toast.success(
            t("dataBackup.cleanup.toast.deleted", { count: result.deleted }),
            lines.length > 0 ? { description: lines.join("\n") } : undefined,
          );
        },
      }),
    [t, memory, agents, projects],
  );

  return <SessionCleanupSection ports={ports} variant="desktop" />;
}
