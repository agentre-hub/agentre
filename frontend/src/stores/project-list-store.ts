// frontend/src/stores/project-list-store.ts
//
// project-list-store 是「扁平化项目列表」唯一数据源。ChatComposer 的 @-mention
// 菜单、issues-page 的项目过滤、命令面板的项目下拉都消费这份数据 —— 在
// chat-panel-host 把所有已打开 chat tab 同时挂载 (CSS 隐藏而非卸载) 之后,
// 每个 tab 各自的 ChatComposer 都会 mount 一次 useProjectList, 原先 hook 内
// 各自 useState+useEffect 的写法会导致 N 个 tab 打 N 次 ProjectListTree IPC。
// 改成 store 之后所有消费方共享同一份 state, 只有第一个 mount 触发拉取。
//
// reload 并发去重: 同一时刻只跑一个 ProjectListTree in-flight; 后续 reload 调用
// 复用同一个 promise, 避免多个 chat tab 同时挂载时各打一遍。

import { create } from "zustand";

import { ProjectListTree } from "../../wailsjs/go/app/App";
import type { app } from "../../wailsjs/go/models";

// 平铺投影：命令面板项目下拉只需要 id/name；mention 菜单额外需要 path/color。
// 父子关系命令面板里不显示。
export type ProjectFlat = {
  id: number;
  name: string;
  path: string;
  color: string;
  /** icon-registry 的图标 key；看板的项目字形与范围选择器都要它。 */
  icon: string;
  depth?: number;
};

function flatten(nodes: app.ProjectTreeNode[]): ProjectFlat[] {
  const out: ProjectFlat[] = [];
  const walk = (ns: app.ProjectTreeNode[], depth: number) => {
    for (const n of ns) {
      if (n.project) {
        out.push({
          id: n.project.id,
          name: n.project.name,
          path: n.project.path,
          color: n.project.color,
          icon: n.project.icon,
          depth,
        });
      }
      if (n.children) walk(n.children, depth + 1);
    }
  };
  walk(nodes, 0);
  return out;
}

export { flatten as flattenProjects };

type State = {
  projects: ProjectFlat[];
  loading: boolean;
  error: string | null;
};

type Actions = {
  reload: () => Promise<void>;
  // 测试隔离用, 生产代码不该调。
  __reset: () => void;
};

// in-flight reload promise: 并发调用 reload() 时复用, 避免重复 RPC。
let inflight: Promise<void> | null = null;

// 初始 loading=false: 与原 hook 行为对齐 (原 useState(false)) —— 项目列表不像
// agents 列表那样在首屏就阻塞渲染, 保持既有语义不变。
export const useProjectListStore = create<State & Actions>((set) => ({
  projects: [],
  loading: false,
  error: null,
  reload: () => {
    if (inflight) return inflight;
    set({ loading: true, error: null });
    inflight = (async () => {
      try {
        const tree = (await ProjectListTree()) ?? [];
        set({ projects: flatten(tree), loading: false, error: null });
      } catch (e: unknown) {
        const msg = e instanceof Error ? e.message : String(e);
        set({ loading: false, error: msg });
      } finally {
        inflight = null;
      }
    })();
    return inflight;
  },
  __reset: () => {
    inflight = null;
    set({ projects: [], loading: false, error: null });
  },
}));
