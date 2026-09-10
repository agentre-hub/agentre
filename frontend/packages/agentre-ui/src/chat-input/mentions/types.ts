import type { MentionKind } from "./xml";

export type MentionItem = {
  kind: MentionKind;
  refId: number; // agent/project primary key; device 恒为 0(设备的身份是 fp)
  label: string;
  path?: string; // project only
  color?: string; // agent/project color token, e.g. "agent-3"
  depth?: number; // project tree depth, root = 0
  fp?: string; // device only: 设备指纹
  online?: boolean; // device only: 此刻是否可达
};

export type MentionSources = {
  agents: MentionItem[];
  projects: MentionItem[];
  devices: MentionItem[];
};

export type MentionMenuState = {
  open: boolean;
  anchorRect: { left: number; top: number; bottom: number } | null;
  items: MentionItem[];
  selectedIndex: number;
  query: string;
};
