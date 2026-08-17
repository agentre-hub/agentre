import type { AgentStatus } from "../transcript/agent-status";

/**
 * 行的 attention 归类。**对包是不透明的宿主 token** —— 桌面端传
 * `AttentionReason`（needs_attention / running / error / bg_running / unread），
 * 别的宿主可以传别的词。
 *
 * 包只解释 `"selected"` 一个值：展开态把它从气泡里剔除，让当前打开的那条回到常规
 * 列表它本来的时间序位置。刻意**不**把宿主的联合类型搬进来 —— 那等于让 UI 包反向
 * 认识宿主的 store 词汇（`@/stores/attention-store` 正是边界守卫拦的东西，
 * 且守卫是文本扫描，`import type` 一样拦）。
 */
export type SessionAttentionRank = "selected" | (string & {});

/**
 * 一条会话在索引里的**展示模型**。宿主各自投影：桌面端从 session-meta-store 来，
 * agentre-server 从 wire 的 `SessionSummary` 来（连同「老会话没标题就退化成
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
  secondaryLabel?: string;
  trailing?: import("react").ReactNode;
  rowActions?: import("react").ReactNode;
  /**
   * 行的目标地址。给了就把行渲染成链接而不是按钮 —— 浏览器宿主靠它保住
   * 中键 / ⌘ 点击 / 「复制链接地址」。桌面端不给（点击是开标签页，没有地址可言）。
   */
  href?: string;
};
