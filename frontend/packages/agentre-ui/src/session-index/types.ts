import type { AgentStatus } from "../transcript/agent-status";

import type { AttentionReason } from "./attention";

/**
 * 行的 attention 归类：包自己的 `AttentionReason`，外加 `"selected"` 这个锚点。
 *
 * 包只解释 `"selected"` 一个值：展开态把它从气泡里剔除，让当前打开的那条回到常规
 * 列表它本来的时间序位置。其余各档对**排布**而言仍是不透明的 —— 包不按 rank 排序，
 * 也不按它取色，那些是 `attention.ts` 里的投影函数干的事。
 *
 * 这里从前写着「刻意不把宿主的联合类型搬进来，那等于让 UI 包反向认识宿主的 store
 * 词汇」。那句话在当时是对的：`AttentionReason` 住在桌面端的
 * `@/stores/attention-store` 里，包去 import 它就是把依赖方向拧反。现在判定与投影
 * 整族都住进了包（见 `./attention`），依赖方向是宿主 → 包，那条理由不再成立 ——
 * 而保留 `string & {}` 的代价是实打实的：agentre-server 因此把 rank 硬写成了一个
 * 拼出来的字符串常量，两端对「这一行为什么冒出来」各说各的。
 */
export type SessionAttentionRank = AttentionReason | "selected";

/**
 * 一条会话在索引里的**展示模型**。宿主各自投影：桌面端从 session-meta-store 来，
 * agentre-server 从 wire 的 `SessionSummary` 来（连同「没有标题的会话退化成
 * cwd · backend · 状态」那套规则，都留在宿主 —— 那是 server 独有的真相）。
 */
export type SessionRowModel = {
  id: string;
  selected?: boolean;
  status: AgentStatus;
  title: string;
  trailingLabel?: string;
  attentionRank?: SessionAttentionRank;
  /**
   * 行首 / 第二行 / 行尾三个插槽。它们是**这一行显示什么**的一部分（`trailingLabel`
   * 已经是一个拼好的字符串，同理），所以留在行模型里而不是另开一个 render prop ——
   * 宿主本来就是逐行投影出这个模型的。见 `SessionRowProps` 上的说明，尤其是
   * `trailing`（链接内）与 `rowActions`（链接外）为什么必须分成两个。
   */
  leading?: import("react").ReactNode;
  secondaryLabel?: import("react").ReactNode;
  overline?: import("react").ReactNode;
  trailing?: import("react").ReactNode;
  rowActions?: import("react").ReactNode;
  /**
   * 行的目标地址。给了就把行渲染成链接而不是按钮 —— 浏览器宿主靠它保住
   * 中键 / ⌘ 点击 / 「复制链接地址」。桌面端不给（点击是开标签页，没有地址可言）。
   */
  href?: string;
};
