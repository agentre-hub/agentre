import type { LocalCommandHistoryAccess } from "@agentre-hub/agentre-ui";

import {
  deriveLocalCommandHistoryScopeKey,
  localCommandHistoryStore,
} from "@/stores/local-command-history-store";

/**
 * `!` Shell 历史接缝的**桌面端实现** —— 把宿主的 localStorage store 接到包的
 * `LocalCommandHistoryAccess` 上。接缝为什么长这样、为什么是可选的，见
 * `packages/agentre-ui/src/chat-input/local-command-history/access.tsx`。
 *
 * 必须是**模块级常量**：它是菜单 hook 订阅 effect 的依赖，每次渲染换新对象
 * 会让订阅每次提交都重订一遍。
 */
export const desktopLocalCommandHistoryAccess: LocalCommandHistoryAccess = {
  deriveScopeKey: deriveLocalCommandHistoryScopeKey,
  list: (scope) => localCommandHistoryStore.list(scope),
  subscribe: (listener) => localCommandHistoryStore.subscribe(listener),
  reserveLastUsedAt: () => localCommandHistoryStore.reserveLastUsedAt(),
  releaseLastUsedAt: (timestamp) =>
    localCommandHistoryStore.releaseLastUsedAt(timestamp),
  record: (scope, command, lastUsedAt) =>
    localCommandHistoryStore.record(scope, command, lastUsedAt),
  clear: (scope) => localCommandHistoryStore.clear(scope),
};
