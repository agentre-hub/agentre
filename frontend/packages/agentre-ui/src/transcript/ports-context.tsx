import * as React from "react";

import type { TranscriptPorts } from "./ports";

/**
 * null 而不是一份 no-op 默认实现：默认实现会让「忘了接线」表现为按钮点了没反应，
 * 那种 bug 只有用户能发现。这里让它在挂载期就抛。
 */
const TranscriptPortsContext = React.createContext<TranscriptPorts | null>(
  null,
);

export function TranscriptPortsProvider({
  ports,
  children,
}: {
  ports: TranscriptPorts;
  children: React.ReactNode;
}) {
  return (
    <TranscriptPortsContext.Provider value={ports}>
      {children}
    </TranscriptPortsContext.Provider>
  );
}

export function useTranscriptPorts(): TranscriptPorts {
  const ports = React.useContext(TranscriptPortsContext);

  if (!ports) {
    // 英文文案：本仓库的约定是注释可中文、生产代码的字符串字面量一律英文，
    // 由 i18n 守卫(src/__tests__/i18n.test.ts)机械保证。
    throw new Error(
      "useTranscriptPorts must be called inside a <TranscriptPortsProvider>",
    );
  }

  return ports;
}

/**
 * 可选端口取用口。返回 undefined 表示宿主没有这个能力，调用方应当**不渲染**
 * 对应的入口（而不是渲染出来点下去没反应）——例如浏览器端没有「在文件管理器
 * 中打开」这回事。
 */
export function useOptionalPort<K extends keyof TranscriptPorts>(
  name: K,
): TranscriptPorts[K] | undefined {
  const ports = useTranscriptPorts();
  const port = ports[name];

  return typeof port === "function" ? port : undefined;
}
