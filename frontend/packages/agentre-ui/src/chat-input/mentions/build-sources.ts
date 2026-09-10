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
// 设备只按指纹认人 —— 两个宿主的设备 id 是各自的、含义不同,指纹才是同一台机器
// 在哪儿都成立的身份(见 mentions/xml.ts 的 MentionRef.fp)。
type DeviceLike = { fp: string; name: string; online?: boolean };

export function buildMentionSources(
  agents: AgentLike[],
  projects: ProjectLike[],
  devices: DeviceLike[],
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
    devices: devices.map((d) => ({
      kind: "device",
      refId: 0,
      label: d.name,
      fp: d.fp,
      online: d.online ?? false,
    })),
  };
}
