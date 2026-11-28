import { create } from "zustand";
import { persist } from "zustand/middleware";

/**
 * 「变更」工具窗内的两档（决策 2）：两档口径不同是有意为之——「本次会话」回答工具
 * 改了什么（消息派生、不读 git），「未提交」回答工作区还有什么没提交。
 *
 * 侧栏「开没开、停在哪个 tab」已经换成工具窗布局（tool-window-layout-store）；
 * 这里只剩工具窗内容自己的偏好，持久化键沿用旧侧栏的，升级后偏好原样保留。
 */
export type ChangesScope = "session" | "uncommitted";

type ChatSidebarState = {
  changesScope: ChangesScope;
  showIgnored: boolean;
  setChangesScope: (scope: ChangesScope) => void;
  setShowIgnored: (showIgnored: boolean) => void;
};

const VALID_CHANGES_SCOPES: ReadonlySet<ChangesScope> = new Set([
  "session",
  "uncommitted",
]);

// 档位与「显示忽略项」都是用户偏好；持久化值可能来自更早的版本或被手改过的
// localStorage，非法或缺失时一律回落到默认（档位回落到「本次会话」，忽略项回落
// 到隐藏）。已废弃的 filesMode / gitBaselineBySession 不再是本 store 的字段，读回
// 来也无处安放（决策 13，不为旧的持久化形态写兼容层）；旧侧栏的 open / activeTab
// 由工具窗布局在首次读回时迁走（tool-window-layout-store）。
function sanitize(state: ChatSidebarState): ChatSidebarState {
  const changesScope = VALID_CHANGES_SCOPES.has(state.changesScope)
    ? state.changesScope
    : "session";
  const showIgnored =
    typeof state.showIgnored === "boolean" ? state.showIgnored : false;
  return {
    ...state,
    changesScope,
    showIgnored,
  };
}

export const useChatSidebarStore = create<ChatSidebarState>()(
  persist(
    (set) => ({
      changesScope: "session",
      showIgnored: false,
      setChangesScope: (scope) => {
        if (!VALID_CHANGES_SCOPES.has(scope)) return;
        set({ changesScope: scope });
      },
      setShowIgnored: (showIgnored) => set({ showIgnored }),
    }),
    {
      name: "chat-sidebar-state",
      // 只从持久化数据里取本 store 现在还认的字段：已废弃的旧字段（filesMode、
      // gitBaselineBySession、已拆分到 file-preview-tabs-store 的
      // previewTabsBySession，以及迁进工具窗布局的 open / activeTab）就此丢掉，不会被读回内存、更不会被再写回
      // localStorage（决策 13，不为旧的持久化形态写兼容层）。取回来的值仍要过
      // sanitize——它们可能来自更早的版本或被手改过。
      merge: (persisted, current) => {
        const saved = (persisted ?? {}) as Partial<ChatSidebarState>;
        return sanitize({
          ...current,
          changesScope: saved.changesScope as ChangesScope,
          showIgnored: saved.showIgnored as boolean,
        });
      },
    },
  ),
);
