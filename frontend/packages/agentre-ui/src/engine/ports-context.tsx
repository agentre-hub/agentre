import * as React from "react";

import type { EngineSettingsPorts } from "./ports";

/**
 * null 而不是一份 no-op 默认实现：默认实现会让「忘了接线」表现为按钮点了没反应，
 * 那种 bug 只有用户能发现。这里让它在挂载期就抛。
 */
const EngineSettingsPortsContext =
  React.createContext<EngineSettingsPorts | null>(null);

export function EngineSettingsPortsProvider({
  ports,
  children,
}: {
  ports: EngineSettingsPorts;
  children: React.ReactNode;
}) {
  return (
    <EngineSettingsPortsContext.Provider value={ports}>
      {children}
    </EngineSettingsPortsContext.Provider>
  );
}

export function useEngineSettingsPorts(): EngineSettingsPorts {
  const ports = React.useContext(EngineSettingsPortsContext);

  if (!ports) {
    // 英文文案：本仓库的约定是注释可中文、生产代码的字符串字面量一律英文，
    // 由 i18n 守卫(src/__tests__/i18n.test.ts)机械保证。
    throw new Error(
      "useEngineSettingsPorts must be called inside a <EngineSettingsPortsProvider>",
    );
  }

  return ports;
}
