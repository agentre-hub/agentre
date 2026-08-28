import { create } from "zustand";
import { persist } from "zustand/middleware";

/**
 * 顶层三段：大纲 / 变更 / 目录（决策 1——Git 不再是一级 tab，它被拆成「变更集」
 * 与「全集 ＋ 状态」两个视角，分别并入「变更」页与「目录」页）。
 */
export type ChatSidebarTab = "outline" | "changes" | "directory";

/**
 * 「变更」页内的两档（决策 2）：两档口径不同是有意为之——「本次会话」回答工具
 * 改了什么（消息派生、不读 git），「未提交」回答工作区还有什么没提交。
 */
export type ChangesScope = "session" | "uncommitted";

type ChatSidebarState = {
  open: boolean;
  activeTab: ChatSidebarTab;
  changesScope: ChangesScope;
  showIgnored: boolean;
  /**
   * 每个会话当前的工作根绝对路径（spec「工作根 · 切换与跟随」）。侧栏是它唯一
   * 的持有者与写入者，这里只是转发给**兄弟节点**预览面板——两者由 chat-panel
   * 并排渲染，没有共同的父组件能把这个值当 prop 传下去。
   *
   * 刻意不进持久化（`merge` 不读它）：工作根是运行期认领出来的，重启后那个
   * worktree 可能已经不存在，读回一个失效的根只会让预览读到别处的同名文件。
   */
  workRootBySession: Record<number, string>;
  setOpen: (open: boolean) => void;
  setActiveTab: (tab: ChatSidebarTab) => void;
  setChangesScope: (scope: ChangesScope) => void;
  setShowIgnored: (showIgnored: boolean) => void;
  /** 侧栏切换工作根时写入当前根的绝对路径；空串 = 这个会话没有工作目录。 */
  setWorkRoot: (sessionId: number, root: string) => void;
};

const VALID_TABS: ReadonlySet<ChatSidebarTab> = new Set([
  "outline",
  "changes",
  "directory",
]);

const VALID_CHANGES_SCOPES: ReadonlySet<ChangesScope> = new Set([
  "session",
  "uncommitted",
]);

// activeTab、档位与「显示忽略项」都是用户偏好，随侧栏状态一起持久化;持久化值
// 可能来自更早的版本或被手改过的 localStorage，非法或缺失时一律回落到默认
// （顶层 tab 回落到「大纲」，档位回落到「本次会话」，忽略项回落到隐藏）。旧版本
// 持久化下来的一级 tab "git" / "files" 与已废弃的 filesMode / gitBaselineBySession
// 都直接丢弃：前者在 VALID_TABS 收窄后自然落入这条回落路径，后两者不再是本 store
// 的字段，读回来也无处安放（决策 13，项目未发布，不写兼容层）。
function sanitize(state: ChatSidebarState): ChatSidebarState {
  const activeTab = VALID_TABS.has(state.activeTab)
    ? state.activeTab
    : "outline";
  const changesScope = VALID_CHANGES_SCOPES.has(state.changesScope)
    ? state.changesScope
    : "session";
  const showIgnored =
    typeof state.showIgnored === "boolean" ? state.showIgnored : false;
  return {
    ...state,
    activeTab,
    changesScope,
    showIgnored,
  };
}

export const useChatSidebarStore = create<ChatSidebarState>()(
  persist(
    (set) => ({
      open: true,
      activeTab: "outline",
      changesScope: "session",
      showIgnored: false,
      workRootBySession: {},
      setOpen: (open) => set({ open }),
      setActiveTab: (tab) => {
        if (!VALID_TABS.has(tab)) return;
        set({ activeTab: tab });
      },
      setChangesScope: (scope) => {
        if (!VALID_CHANGES_SCOPES.has(scope)) return;
        set({ changesScope: scope });
      },
      setShowIgnored: (showIgnored) => set({ showIgnored }),
      setWorkRoot: (sessionId, root) =>
        set((state) =>
          state.workRootBySession[sessionId] === root
            ? state
            : {
                workRootBySession: {
                  ...state.workRootBySession,
                  [sessionId]: root,
                },
              },
        ),
    }),
    {
      name: "chat-sidebar-state",
      // 只从持久化数据里取本 store 现在还认的字段：已废弃的旧字段（filesMode、
      // gitBaselineBySession，以及已拆分到 file-preview-tabs-store 的
      // previewTabsBySession）就此丢掉，不会被读回内存、更不会被再写回
      // localStorage（决策 13，项目未发布，不写兼容层）。取回来的值仍要过
      // sanitize——它们可能来自更早的版本或被手改过。
      merge: (persisted, current) => {
        const saved = (persisted ?? {}) as Partial<ChatSidebarState>;
        return sanitize({
          ...current,
          open: typeof saved.open === "boolean" ? saved.open : current.open,
          activeTab: saved.activeTab as ChatSidebarTab,
          changesScope: saved.changesScope as ChangesScope,
          showIgnored: saved.showIgnored as boolean,
        });
      },
    },
  ),
);
