import * as React from "react";

import type { TerminalTransport } from "./transport";

/**
 * null 而不是一份 no-op 默认实现 —— 这里跟 `TranscriptPorts` 走，不跟
 * `TranscriptLiveState` 走。
 *
 * 判据是「空实现有没有一个**诚实**的答案」：live-state 的 `useIsStreamActive`
 * 有 —— 宿主没有实时流时，`false` 就是事实。终端没有：一个 no-op transport 会
 * 开出一个永不吐字节、也永不退出的终端，和 PTY 卡死在视觉上完全一样，只有用户
 * 能发现。所以「没接线」在挂载期就炸，「宿主没这能力」用下面的可选取用口表达。
 */
const TerminalTransportContext = React.createContext<TerminalTransport | null>(
  null,
);

/**
 * `transport` 应当是**模块级常量**：它承载的订阅表要跨渲染存活，每次渲染换一个
 * 新实现会让已挂上的订阅者失联。
 */
export function TerminalTransportProvider({
  transport,
  children,
}: {
  transport: TerminalTransport;
  children: React.ReactNode;
}) {
  return (
    <TerminalTransportContext.Provider value={transport}>
      {children}
    </TerminalTransportContext.Provider>
  );
}

export function useTerminalTransport(): TerminalTransport {
  const transport = React.useContext(TerminalTransportContext);

  if (!transport) {
    // 英文文案：本仓库约定注释可中文、生产代码的字符串字面量一律英文。
    throw new Error(
      "useTerminalTransport must be called inside a <TerminalTransportProvider>",
    );
  }

  return transport;
}

/**
 * 能力探测口，语义对齐 `useOptionalPort`：返回 null 表示这个宿主**没有终端**
 * （agentre-server 首版就是），调用方应当据此**不渲染**开终端的入口，而不是渲染
 * 出来点下去挂起。真要渲染终端视图，就该用上面那个会炸的取用口。
 */
export function useOptionalTerminalTransport(): TerminalTransport | null {
  return React.useContext(TerminalTransportContext);
}
