import type {
  LocalCommandsAccess,
  LocalCommandView,
} from "@agentre-ai/agentre-ui";
import { useShallow } from "zustand/react/shallow";

import {
  useLocalCommandsStore,
  type LocalCommandEntry,
} from "@/stores/local-commands-store";

/**
 * 桌面端的本地命令适配层 —— 共享包与 `local-commands-store` 之间唯一的接缝。
 *
 * 与 `transcript-ports-desktop.ts` / `transcript-live-state-desktop.ts` 同为
 * 模块级常量：包里会把 `useLocalCommand` 成员当 hook 调用，每次渲染换新对象会让
 * hook 的调用来源漂移。
 *
 * 三种读写各按其性质接线：
 *   - 反应式读走 selector + `useShallow`；
 *   - 输出增量走 `store.subscribe`，**不经 React**；
 *   - 写动作走 `getState()`（动作不需要订阅，也不该让调用方跟着重渲染）。
 */

/**
 * `useShallow` 不是可有可无的优化，是这条接缝的正确性条件。
 *
 * `appendOutput` 每来一段输出都会重建条目对象，裸 selector 于是每 chunk 返回一个
 * 新引用 —— 卡片就跟着每 chunk 重渲染一次。投影本身已经把 `output` 摘掉（包里
 * `LocalCommandView` 的形状决定的），字段全是原始值，浅比之后一条长跑命令
 * 从头到尾只会在状态/折叠态真的变化时推一次重渲染。
 */
function project(entry: LocalCommandEntry | undefined) {
  if (!entry) return undefined;

  const { output, ...rest } = entry;

  return { ...rest, hasOutput: output.length > 0 } satisfies LocalCommandView;
}

export const desktopLocalCommandsAccess: LocalCommandsAccess = {
  useLocalCommand(id) {
    // 这个成员按契约只在渲染期被当作 hook 调用（见包里的 access.tsx）。
    return useLocalCommandsStore(useShallow((s) => project(s.entries[id])));
  },

  subscribeOutput(id, onAppend) {
    // 游标按订阅持有：同一条命令上的多个视图（转录里的输出终端 + attach 到终端
    // 标签的那份）各自从头拿到完整内容，互不影响。
    let written = 0;
    const flush = () => {
      const entry = useLocalCommandsStore.getState().get(id);
      if (!entry || entry.output.length <= written) return;
      onAppend(entry.output.slice(written));
      written = entry.output.length;
    };

    flush(); // 订阅当场同步 seed 一次积压输出（契约见 access.ts）。

    return useLocalCommandsStore.subscribe(flush);
  },

  toggleExpanded(id) {
    useLocalCommandsStore.getState().toggleExpanded(id);
  },

  remove(id) {
    useLocalCommandsStore.getState().remove(id);
  },
};
