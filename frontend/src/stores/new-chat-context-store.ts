import { create } from "zustand";

import type { AgentSlim } from "@/hooks/use-chat-agents";

// 命令面板「New chat with …」的项目上下文。
// project-page mount 时写入；命令面板 NewChatSource / ContextBar 读取以决定：
//   - 列表分组（项目成员 vs 不在该项目）
//   - onSelect 分发（项目成员 → newSelectionHandler(pid, agent)；
//     非成员 → 静默兜底到 /chat）

export type ProjectContext = {
  projectID: number;
  projectName: string;
};

export type NewSelectionHandler = (projectID: number, agent: AgentSlim) => void;

type State = {
  projectContext: ProjectContext | null;
  newSelectionHandler: NewSelectionHandler | null;
};

type Actions = {
  setContext: (ctx: ProjectContext | null) => void;
  setNewSelectionHandler: (fn: NewSelectionHandler | null) => void;
  clear: () => void;
};

export const useNewChatContextStore = create<State & Actions>((set) => ({
  projectContext: null,
  newSelectionHandler: null,
  setContext: (projectContext) => set({ projectContext }),
  setNewSelectionHandler: (newSelectionHandler) => set({ newSelectionHandler }),
  clear: () => set({ projectContext: null, newSelectionHandler: null }),
}));
