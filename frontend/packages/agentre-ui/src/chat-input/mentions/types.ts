import type { MentionKind } from "./xml";

export type MentionItem = {
  kind: MentionKind;
  refId: number;
  label: string;
  path?: string; // project only
  color?: string; // agent/project color token, e.g. "agent-3"
  depth?: number; // project tree depth, root = 0
};

export type MentionSources = {
  agents: MentionItem[];
  projects: MentionItem[];
};

export type MentionMenuState = {
  open: boolean;
  anchorRect: { left: number; top: number; bottom: number } | null;
  items: MentionItem[];
  selectedIndex: number;
  query: string;
};
