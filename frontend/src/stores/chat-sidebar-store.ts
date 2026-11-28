import { create } from "zustand";
import { persist } from "zustand/middleware";

/**
 * 对话上下文工具窗内容自己的偏好。侧栏「开没开、停在哪个 tab」已经换成工具窗布局
 * （tool-window-layout-store）；持久化键沿用旧侧栏的，升级后偏好原样保留。
 */
type ChatSidebarState = {
  showIgnored: boolean;
  setShowIgnored: (showIgnored: boolean) => void;
};

// 「显示忽略项」是用户偏好；持久化值可能来自更早的版本或被手改过的 localStorage，
// 非法或缺失时一律回落到隐藏。已废弃的 changesScope（「本次会话 / 未提交」两档已
// 去掉）/ filesMode / gitBaselineBySession 不再是本 store 的字段，读回来也无处安放
// （不为旧的持久化形态写兼容层）；旧侧栏的 open / activeTab 由工具窗布局在首次读回
// 时迁走（tool-window-layout-store）。
function sanitize(state: ChatSidebarState): ChatSidebarState {
  const showIgnored =
    typeof state.showIgnored === "boolean" ? state.showIgnored : false;
  return { ...state, showIgnored };
}

export const useChatSidebarStore = create<ChatSidebarState>()(
  persist(
    (set) => ({
      showIgnored: false,
      setShowIgnored: (showIgnored) => set({ showIgnored }),
    }),
    {
      name: "chat-sidebar-state",
      // 只从持久化数据里取本 store 现在还认的字段：已废弃的旧字段（changesScope、
      // filesMode、gitBaselineBySession、已拆分到 file-preview-tabs-store 的
      // previewTabsBySession，以及迁进工具窗布局的 open / activeTab）就此丢掉，不会被读回内存、更不会被再写回
      // localStorage（决策 13，不为旧的持久化形态写兼容层）。取回来的值仍要过
      // sanitize——它们可能来自更早的版本或被手改过。
      merge: (persisted, current) => {
        const saved = (persisted ?? {}) as Partial<ChatSidebarState>;
        return sanitize({
          ...current,
          showIgnored: saved.showIgnored as boolean,
        });
      },
    },
  ),
);
