// frontend/src/stores/sidebar-axis-store.ts
//
// 会话索引的分组维度（axis）的**活状态**。持久化仍归 lib/sidebar-axis-state.ts
// （键、默认值、非法值回落都在那里），这里只负责「现在是哪个轴」以及订阅。
//
// 之所以要一个 store 而不是索引页自己的 useState：设置这个值的地方不止一处。
// AxisPicker 在索引页里，⌘D 在 ShortcutsProvider 里 —— 后者不在索引页的组件树上，
// 只写 localStorage 是叫不动已经挂载的索引的。
import { create } from "zustand";

import type { IndexAxis } from "@/lib/session-axis";
import { readSidebarAxis, writeSidebarAxis } from "@/lib/sidebar-axis-state";

type SidebarAxisState = {
  axis: IndexAxis;
  setAxis: (axis: IndexAxis) => void;
  /** 测试用：回到 localStorage 当下的值（清过 storage 就是默认轴）。 */
  __reset: () => void;
};

export const useSidebarAxisStore = create<SidebarAxisState>()((set) => ({
  axis: readSidebarAxis(),
  setAxis: (axis) => {
    writeSidebarAxis(axis);
    set({ axis });
  },
  __reset: () => set({ axis: readSidebarAxis() }),
}));
