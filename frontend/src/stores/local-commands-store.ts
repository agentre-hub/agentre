import { isLocalCommandCollapsed } from "@agentre-ai/agentre-ui";
import { create } from "zustand";

export type LocalCommandStatus = "running" | "done" | "failed" | "stopped";
export interface LocalCommandEntry {
  id: string;
  sessionId: number;
  command: string;
  createdAt: number;
  status: LocalCommandStatus;
  exitCode?: number;
  output: string;
  finishedAt?: number;
  expanded?: boolean;
}
interface State {
  entries: Record<string, LocalCommandEntry>;
  start(e: {
    id: string;
    sessionId: number;
    command: string;
    createdAt: number;
  }): void;
  appendOutput(id: string, chunk: string): void;
  finish(
    id: string,
    status: Exclude<LocalCommandStatus, "running">,
    exitCode?: number,
  ): void;
  toggleExpanded(id: string): void;
  remove(id: string): void;
  get(id: string): LocalCommandEntry | undefined;
  listForSession(sessionId: number): LocalCommandEntry[];
}

export const useLocalCommandsStore = create<State>((set, get) => ({
  entries: {},
  start: (e) =>
    set((s) => ({
      entries: {
        ...s.entries,
        [e.id]: { ...e, status: "running", output: "" },
      },
    })),
  appendOutput: (id, chunk) =>
    set((s) => {
      const cur = s.entries[id];
      if (!cur) return s;
      return {
        entries: { ...s.entries, [id]: { ...cur, output: cur.output + chunk } },
      };
    }),
  finish: (id, status, exitCode) =>
    set((s) => {
      const cur = s.entries[id];
      if (!cur) return s;
      return {
        entries: {
          ...s.entries,
          [id]: { ...cur, status, exitCode, finishedAt: Date.now() },
        },
      };
    }),
  toggleExpanded: (id) =>
    set((s) => {
      const cur = s.entries[id];
      if (!cur) return s;
      const collapsed = isLocalCommandCollapsed(cur);
      // 折叠中 → 展开(expanded=true);展开中 → 折叠(expanded=false)。
      return {
        entries: { ...s.entries, [id]: { ...cur, expanded: collapsed } },
      };
    }),
  remove: (id) =>
    set((s) => {
      if (!s.entries[id]) return s;
      const { [id]: _removed, ...rest } = s.entries;
      return { entries: rest };
    }),
  get: (id) => get().entries[id],
  listForSession: (sessionId) =>
    Object.values(get().entries)
      .filter((e) => e.sessionId === sessionId)
      .sort((a, b) => a.createdAt - b.createdAt),
}));
