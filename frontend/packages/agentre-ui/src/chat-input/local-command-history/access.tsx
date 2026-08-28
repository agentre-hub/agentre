import * as React from "react";

/**
 * `!` Shell 历史的**宿主接缝** —— 输入框与宿主持久化状态之间唯一的边界。
 *
 * ### 为什么是普通对象而不是 hook
 *
 * 判据同 `transcript/local-command/access.tsx`：**这条数据没有反应式读**。
 * 菜单不是「历史变了就重渲染」，而是「编辑器每次 update/selectionUpdate 重算一次
 * 候选」——重算的驱动来自 TipTap 事件，读取是命令式的 `list()`；跨会话的外部写入
 * （另一个 composer 记了一条）走 `subscribe()` 显式通知。所以这里是
 * `subscribeOutput` 那一路（命令式订阅），而不是 `useLocalCommand` 那一路。
 *
 * ### 为什么整条能力是**可选**的
 *
 * agentre-server 没有本地命令这回事：它既不跑 PTY，也没有「当前设备 + cwd」这个
 * 作用域可言。语义对齐 `ports.ts` 末尾那段——可选口是**能力探测**而不是静默失败：
 * 宿主不挂 Provider 时，`!` 开头依然进命令模式（那是编辑器自己的事），但历史弹层
 * 干脆不渲染，而不是弹出一个空列表 + 一个点了没反应的「清空」。
 *
 * 桌面端实现见 `src/components/agentre/local-command-history-access-desktop.ts`。
 */

/** 一条历史的归属：设备与工作目录共同隔离持久化的 Shell 历史。 */
export type LocalCommandHistoryScope = {
  readonly deviceId: string;
  readonly cwd: string;
};

export type LocalCommandHistoryEntry = {
  readonly command: string;
  readonly lastUsedAt: number;
};

/** 外部写入的通知。`scopeKey` 由 `deriveScopeKey` 产出，消费方据此只认自己那一档。 */
export type LocalCommandHistoryMutation = {
  readonly type: "record" | "clear";
  readonly scopeKey: string;
};

export interface LocalCommandHistoryAccess {
  /**
   * 把作用域编码成可比较的字符串。**编码规则归宿主**（它同时是持久化的键），
   * 包内只拿它跟 `subscribe` 推来的 `scopeKey` 做相等判断。
   */
  deriveScopeKey(scope: LocalCommandHistoryScope): string;

  /** 命令式读当前作用域的全部历史。可能抛（存储损坏），调用方自行降级。 */
  list(scope: LocalCommandHistoryScope): LocalCommandHistoryEntry[];

  /** 订阅外部写入。退订句柄只摘自己，重复调用幂等。 */
  subscribe(
    listener: (mutation: LocalCommandHistoryMutation) => void,
  ): () => void;

  /**
   * 预定一个 MRU 时间戳。命令**提交时**先占位、**执行作用域确定后**才落库 ——
   * 中间隔着一次 await，不预定就会让两条并发命令的先后顺序取决于谁先 resolve。
   */
  reserveLastUsedAt(): number;

  /** 释放一个不再会落库的预定（提交失败 / 宿主没给出执行作用域）。 */
  releaseLastUsedAt(timestamp: number): void;

  /** 记一条命令；`lastUsedAt` 省略时由实现自行取号。 */
  record(
    scope: LocalCommandHistoryScope,
    command: string,
    lastUsedAt?: number,
  ): void;

  /** 清空当前作用域；返回 false 表示写入失败（调用方据此提示用户）。 */
  clear(scope: LocalCommandHistoryScope): boolean;
}

const LocalCommandHistoryContext =
  React.createContext<LocalCommandHistoryAccess | null>(null);

/**
 * `access` 应当是**模块级常量**：菜单 hook 把它当作 effect 依赖的稳定来源，
 * 每次渲染换一个新对象会让订阅每次提交都重订一遍。
 */
export function LocalCommandHistoryProvider({
  access,
  children,
}: {
  access: LocalCommandHistoryAccess;
  children: React.ReactNode;
}) {
  return (
    <LocalCommandHistoryContext.Provider value={access}>
      {children}
    </LocalCommandHistoryContext.Provider>
  );
}

/** 能力探测口：返回 null 表示这个宿主没有本地命令历史。 */
export function useOptionalLocalCommandHistoryAccess(): LocalCommandHistoryAccess | null {
  return React.useContext(LocalCommandHistoryContext);
}
