import * as React from "react";

import { UserAskCard } from "./user-ask/card";
import { PlanApproveCard } from "./plan-approve-request/card";
import { AgentSpawnCard } from "./agent-spawn/card";
import { ToolPermissionCard } from "./tool-permission/card";
import { RawToolCard } from "./raw/card";
import type { CanonicalCardProps } from "./props";
import type { CanonicalDTO, CanonicalKind } from "./types";

const REGISTRY: Partial<Record<CanonicalKind, React.FC<CanonicalCardProps>>> = {
  "user.ask": UserAskCard,
  "plan.approve_request": PlanApproveCard,
  "agent.spawn": AgentSpawnCard,
  "tool.permission": ToolPermissionCard,
};

// CanonicalToolRouter 根据 toolBlock.canonical.kind 路由到对应卡;
// 无 canonical 字段或 kind 未注册时回落 RawToolCard。
//
// 注册表只剩「出组档」这几种 —— file.write / file.edit 这类会折进活动块,
// 它们的正文由活动行的展开体直接调 content-renderer / hunk-renderer 渲染,
// 不再经过卡壳(那两个卡壳已随聚合改动删除)。plan.update 同样刻意不注册:
// tool_use 形态的 plan.update 走通用工具路径;type="plan" 且带 actions 的
// plan.update 在 chat.tsx 里直接复用 PlanCard。
export function CanonicalToolRouter(props: CanonicalCardProps) {
  const canonical = (props.toolBlock as { canonical?: CanonicalDTO }).canonical;
  if (!canonical) {
    return <RawToolCard {...props} />;
  }
  const Card = REGISTRY[canonical.kind];
  if (!Card) return <RawToolCard {...props} />;
  return <Card {...props} />;
}
