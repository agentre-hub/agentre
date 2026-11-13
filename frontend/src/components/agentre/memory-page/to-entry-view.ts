import type { MemoryEntryView } from "@agentre-hub/agentre-ui";

import type { memory_svc } from "../../../../wailsjs/go/models";

export type CreatorContext = {
  /** 会话标题（本机已知时）。 */
  sessionTitle: (sessionId: number) => string | undefined;
  /** 拿不到会话标题时的回落文字，由 agent 名拼成。 */
  fallbackLabel: (agentId: number) => string;
  openSession: (sessionId: number) => void;
};

/** Wails 的 `memory_svc.MemoryItem` → 共享包的行视图。 */
export function toEntryView(
  item: memory_svc.MemoryItem,
  ctx: CreatorContext,
): MemoryEntryView {
  const creator = item.creator;
  return {
    id: item.id,
    summary: item.summary,
    detail: item.detail ?? "",
    pinned: item.pinned,
    archived: item.state === "archived",
    hasPrevious: item.hasPrevious,
    creator:
      creator?.kind === "ai"
        ? {
            kind: "ai",
            label:
              ctx.sessionTitle(creator.sessionId) ||
              ctx.fallbackLabel(creator.agentId),
            onOpen:
              creator.sessionId > 0
                ? () => ctx.openSession(creator.sessionId)
                : undefined,
          }
        : { kind: "user" },
    createdAt: item.createtime,
    // 最近修改 = 最近一次变更（置顶、同步落地也会刷新 updatetime，那不是修改）。
    updatedAt: item.lastChange.at,
    lastChange: {
      action: item.lastChange.action,
      actor: item.lastChange.actor.kind === "ai" ? "ai" : "user",
    },
  };
}
