// frontend/src/stores/sidebar-reload.ts
//
// reloadSidebarSources 是「需要全栈刷新左侧 sidebar 数据」的统一入口。
// chat-panel ChatPanel.onSidebarShouldReload (新建会话 / turn done / steer 等)
// 都通过这一处触发。
//
// 把这个 helper 单独抽出来的好处:
//   - 调用方 (chat-panel-host) 一行调用即可, 不需要 import 多个 store。
//   - 后续再加新数据源 (例如 issues 侧栏) 只改这里, 不动散落各处的 callback。
//   - 测试: 可以一次断言这条统一信号确实把所有来源都刷新了。

import {
  isProjectTreeCacheLoaded,
  reloadProjectTreeCache,
} from "@/hooks/use-project-tree";

import { useChatAgentsStore } from "./chat-agents-store";
import { useSessionIndexStore } from "./session-index-store";

export function reloadSidebarSources(): void {
  void useChatAgentsStore.getState().reload();
  // 已经打开过的索引 scope（时间轴 / 随手对话 / 各项目组）各自重拉；没打开过的
  // 一个 RPC 都不发。
  void useSessionIndexStore.getState().reloadLoaded();
  // 项目树**没有任何推送通道** —— 全仓库的 EventsOn 里没有一条是项目变更，此前
  // 只靠项目页那条 1 秒轮询兜着。轮询删掉后它必须挂在这个统一入口上，否则另一台
  // 设备同步过来的项目要等一次别的交互才出现。
  //
  // 与索引 scope 同一条判据：树没被订阅过就不刷 —— 用户没用过项目侧栏时完全静默，
  // 不为对话侧凭空拉项目数据。
  if (isProjectTreeCacheLoaded()) {
    void reloadProjectTreeCache();
  }
}

// isSessionKnownToSidebar 判断某 session 是否已经被左栏收录。chat-agents-store 是
// 「agent → 其会话」的唯一索引 (sessionIds 是去重后的全量 session id), 每个 agent 的
// 所有会话都归在其名下, 所以这里只需问 chat-agents-store。
export function isSessionKnownToSidebar(sessionId: number): boolean {
  if (sessionId <= 0) return false;
  for (const a of useChatAgentsStore.getState().agents) {
    if (a.sessionIds.includes(sessionId)) return true;
  }
  return false;
}

// ensureSessionInSidebar 保证「带外创建」的会话能进左栏: 若该 session 还没被左栏
// 收录, 触发一次 reloadSidebarSources() 把它整刷进来; 已收录则跳过, 避免每轮都发
// 无谓 RPC。
//
// 为什么需要它: 普通单聊靠 ChatPanel.onSidebarShouldReload 在 turn 起手/落定时
// reload 左栏。但有些会话是在 ChatPanel 之外被创建的 —— 如 subagent 委派惰性新建的
// backing session, 后续也可能是「远程调用创建会话」等路径。这些都绕开了 onSidebarShouldReload,
// 左栏拿不到新行 (行不在 → 列表里没有, running 状态也无处挂)。统一走这一个入口即可。
export function ensureSessionInSidebar(sessionId: number): void {
  if (sessionId <= 0) return;
  if (isSessionKnownToSidebar(sessionId)) return;
  reloadSidebarSources();
}
