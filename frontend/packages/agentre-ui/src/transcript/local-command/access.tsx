import * as React from "react";

import type { TranscriptLocalCommand } from "../dto";

/**
 * 本地命令条目的**宿主接缝** —— 卡片 / 输出终端 / attach 终端视图与宿主状态之间
 * 唯一的边界。桌面端那侧是 `local-commands-store` 这个 zustand store，而 zustand
 * 按设计不属于本包（见 `boundary.test.ts` 头注释：状态归宿主）。
 *
 * ### 为什么这层长这样：一个条目上有**两种性质不同的读**
 *
 * 1. **反应式读**（`useLocalCommand`）—— 命令跑完了没有、退出码是几、是否折叠。
 *    这些要触发重渲染，所以**必须是 hook**，理由与 `live-state.tsx` 完全一致：
 *    端口是个普通对象，从上面取值只能拿到调用那一刻的快照，状态变了没人通知 React。
 * 2. **订阅式增量读**（`subscribeOutput`）—— PTY 吐出来的输出文本。这一路刻意
 *    **不反应式**：输出是流，每来一个 chunk 就重渲染一次会把长跑命令的界面拖垮，
 *    而它的去处是 xterm 的 `write()`，压根不需要经过 React。
 *
 * 所以 `LocalCommandView` **刻意不含 `output`**。这不是省字段，是让第 2 类读
 * 无法从第 1 类读里漏出去：只要投影里有 `output`，宿主用一个再普通不过的
 * selector 实现 `useLocalCommand` 就会每 chunk 重渲染一次，而那种退化在桌面端
 * 只表现为"有点卡"，没有任何机械信号。形状上砍掉，退化就写不出来。
 * （守卫见 `output-terminal.test.tsx` 里的"渲染次数不随 chunk 数增长"。）
 *
 * 写入侧（折叠 / 移除）是**一次性动作**，不需要反应式，所以是普通方法 ——
 * 与 `live-state.tsx` 的 `markToolPermissionResolved` 同一条判据。
 *
 * 桌面端实现见 `src/components/agentre/local-commands-access-desktop.ts`。
 */

/**
 * 卡片要读的**非流式投影**。字段与 `TranscriptLocalCommand` 同源，唯独抽掉
 * `output`（理由见上），换成一个只会翻一次的 `hasOutput`。
 */
export type LocalCommandView = Omit<TranscriptLocalCommand, "output"> & {
  /** 这条命令是否产出过输出。用来区分「还没输出」与「跑完了什么都没输出」。 */
  hasOutput: boolean;
};

/** 输出增量的接收者。收到的是**已解码文本**，直接喂 xterm。 */
export type LocalCommandOutputListener = (chunk: string) => void;

/** 退订句柄。契约是只摘自己，重复调用幂等（与终端传输端口一致）。 */
export type LocalCommandUnsubscribe = () => void;

export interface LocalCommandsAccess {
  /**
   * 反应式读一条命令的非流式投影；条目不存在时返回 undefined（卡片据此不渲染）。
   *
   * **必须是 hook**：宿主用自己的订阅机制实现（桌面端是 zustand selector），
   * 命令结束 / 折叠态切换时消费方要跟着重渲染。
   */
  useLocalCommand(id: string): LocalCommandView | undefined;

  /**
   * 订阅输出增量。**订阅时同步交付一次已积累的全部输出**（若非空）作为首帧 seed，
   * 此后每次追加只交付新增的那一段。
   *
   * seed + 增量的游标由实现方按订阅持有，所以多个视图（转录里的输出终端、
   * attach 到终端标签的那份）各自从头拿到完整内容，互不影响。
   *
   * 交付的是**已解码文本**而不是字节：解码只能做一次 —— PTY 的块边界可能切在
   * 多字节字符中间，第二个 `TextDecoder` 从半个字符处起手会吐 U+FFFD
   * （宿主用 `makeStreamDecoder` 在写进条目前解一次，见 `decode.ts`）。
   */
  subscribeOutput(
    id: string,
    onAppend: LocalCommandOutputListener,
  ): LocalCommandUnsubscribe;

  /** 折叠 ⇄ 展开。派生规则是纯函数 `isLocalCommandCollapsed`，不归宿主解释。 */
  toggleExpanded(id: string): void;

  /** 从列表里移除这条命令（用户按「移除」）。 */
  remove(id: string): void;
}

/**
 * null 而不是 no-op 默认实现 —— 判据与 `transport-context.tsx` 同：空实现有没有
 * 一个**诚实**的答案。这里没有：no-op 的 access 会把卡片渲染出来，「移除」「折叠」
 * 点下去却毫无反应，只有用户能发现。
 *
 * 而「宿主压根没有本地命令能力」（agentre-server）不会走到这里：那种宿主传给
 * `buildTranscriptRows` 的 `localCommands` 恒为空数组，卡片根本不会被渲染。
 */
const LocalCommandsContext = React.createContext<LocalCommandsAccess | null>(
  null,
);

/**
 * `access` 应当是**模块级常量**：它的 `useLocalCommand` 成员被当作 hook 调用，
 * 每次渲染换一个新对象会让 hook 的调用来源在两次渲染之间漂移。
 */
export function LocalCommandsProvider({
  access,
  children,
}: {
  access: LocalCommandsAccess;
  children: React.ReactNode;
}) {
  return (
    <LocalCommandsContext.Provider value={access}>
      {children}
    </LocalCommandsContext.Provider>
  );
}

export function useLocalCommandsAccess(): LocalCommandsAccess {
  const access = React.useContext(LocalCommandsContext);

  if (!access) {
    // 英文文案：本仓库约定注释可中文、生产代码的字符串字面量一律英文。
    throw new Error(
      "useLocalCommandsAccess must be called inside a <LocalCommandsProvider>",
    );
  }

  return access;
}

/**
 * 能力探测口，语义对齐 `useOptionalPort` / `useOptionalTerminalTransport`：
 * 返回 null 表示这个宿主没有本地命令能力。
 *
 * 谁该用它：**不确定自己会不会用到这条能力**的调用方。终端面板就是 ——
 * 它无条件调用两个数据源 hook（hooks 规则），但 live 模式那条 PTY 与本地命令
 * 毫无关系。若它取用的是上面那个会炸的口，一个没有本地命令的宿主连开个普通
 * 终端都会在挂载期炸，这就把 attach 模式的依赖错误地摊派给了所有终端。
 */
export function useOptionalLocalCommandsAccess(): LocalCommandsAccess | null {
  return React.useContext(LocalCommandsContext);
}

/** 反应式读的取用口。无条件调用宿主那个 hook —— hook 数必须跨渲染稳定。 */
export function useLocalCommand(id: string): LocalCommandView | undefined {
  return useLocalCommandsAccess().useLocalCommand(id);
}
