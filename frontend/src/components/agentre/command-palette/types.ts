import type { NavigateFunction } from "react-router-dom";

import type { ChatAgentItem } from "@/hooks/use-chat-agents";

import type { PaletteMode } from "./mode";

// 命令源每个 item 必须有的两条基础字段。具体 source 可以扩展（如 chat-sessions 会带 status / agent 等）。
export type CommandItemBase = {
  // cmdk 内部用的稳定 id（"chat-session-42" 这种），不渲染。
  key: string;
  // 可选的次级分组标签。同一个 source 可以输出多个 subHeading 分组；
  // SourceGroup 按相邻 item 的 subHeading 值切段渲染。
  // 留空（undefined / ""）= 不进任何子分组（默认行为）。
  subHeading?: string;
};

// onSelect 时注入的依赖，避免 source 直接 import store / router。
// chat tab 写入用显式动作:openSession 打开/复用已有会话 tab(可选 newTab=true
// 在新 tab 里);openNewSession 打开 kind:"new" 占位 tab(新建会话首发上下文)。
// openNotChattableDialog 供不可对话分支打开引导弹窗（面板宿主负责渲染弹窗）。
export type OnSelectCtx = {
  navigate: NavigateFunction;
  close: () => void;
  openSession: (sessionId: number, opts?: { newTab?: boolean }) => void;
  openNewSession: (agentId: number) => void;
  openNotChattableDialog: (agent: ChatAgentItem) => void;
};

// 单个命令源契约：
//   - id：组标识（cmdk CommandGroup 的 value）。
//   - heading：分组标题文案。
//   - modes：该源在哪些模式下激活（"default" = ⌘P 搜索；"command" = ">" 前缀命令）。
//   - activeFor：可选上下文门 —— 未提供则永远 active；提供时 false 会被 SOURCES.filter 跳过。
//     用于互斥的命令（选了项目上下文用 "New project chat with"，否则用 "New chat with"）。
//   - useItems：React hook，返回最终要渲染的 items + loading 状态。
//   - getScore：传入 query 与 item，返回评分（0=不命中，>0 排序权重）。
//   - renderItem：渲染单行（包括 Avatar / Title / Meta / Right）。
//   - isDisabled：可选。返回 true 的 item 渲染成不可选 / 不可点（cmdk disabled），
//     键盘导航跳过、hover 显示禁用光标；onSelect 不会被触发。未提供 = 全部可选。
//   - onSelect：用户回车 / 点击时回调。
export type CommandSource<T extends CommandItemBase> = {
  id: string;
  heading: string;
  modes: readonly PaletteMode[];
  activeFor?: (ctx: { hasProjectContext: boolean }) => boolean;
  useItems: () => { items: T[]; loading: boolean };
  getScore: (query: string, item: T) => number;
  renderItem: (item: T, opts: { active: boolean }) => React.ReactNode;
  isDisabled?: (item: T) => boolean;
  onSelect: (item: T, ctx: OnSelectCtx) => void;
};
