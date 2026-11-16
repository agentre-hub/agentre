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
  // 来源会话在本机是否还在由后端回答：sessionMetas 只是局部缓存，证明不了会话不存在。
  // 已删的会话既不显示缓存里可能残留的标题，也不给点击入口，回落到 agent 名。
  const sessionAlive =
    creator?.kind === "ai" &&
    creator.sessionId > 0 &&
    item.creatorSessionExists;
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
              (sessionAlive
                ? ctx.sessionTitle(creator.sessionId)
                : undefined) || ctx.fallbackLabel(creator.agentId),
            onOpen: sessionAlive
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
