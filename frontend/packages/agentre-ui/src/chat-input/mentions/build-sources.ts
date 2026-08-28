import type { MentionSources } from "./types";

// 只依赖字段子集(结构化类型),避免耦合完整 AgentSlim / ProjectFlat 形状。
type AgentLike = { id: number; name: string; avatarColor?: string | null };
type ProjectLike = {
  id: number;
  name: string;
  path?: string | null;
  color?: string | null;
  depth?: number;
};

export function buildMentionSources(
  agents: AgentLike[],
  projects: ProjectLike[],
): MentionSources {
  return {
    agents: agents.map((a) => ({
      kind: "agent",
      refId: a.id,
      label: a.name,
      color: a.avatarColor ?? "",
    })),
    projects: projects.map((p) => ({
      kind: "project",
      refId: p.id,
      label: p.name,
      path: p.path ?? "",
      color: p.color ?? "",
      depth: p.depth ?? 0,
    })),
  };
}
