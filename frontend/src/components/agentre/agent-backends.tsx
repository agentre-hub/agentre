import type React from "react";

import {
  AgentBackendsPanel as SharedAgentBackendsPanel,
  type ListCliModels,
} from "@agentre-hub/agentre-ui";
import { ListCLIModels } from "../../../wailsjs/go/app/App";
import { EventsOn } from "../../../wailsjs/runtime/runtime";

import { createDesktopEngineSettingsPorts } from "./engine-ports-desktop";
import { useRefreshSignal } from "./use-refresh-signal";

// Wails 的模型字段在宿主边界转换为共享 Picker 的目录契约。
const listCLIModels: ListCliModels = async (request) => {
  const result = await ListCLIModels({
    backendId: Number(request.backendId),
    refresh: request.refresh,
  });
  return {
    models: result.models.map((model) => ({
      value: model.value,
      label: model.displayName,
      modelId: model.modelId,
      group: model.group,
      legacy: model.legacy,
    })),
    effectiveValue: result.effectiveValue,
    effectiveModelId: result.effectiveModelId,
    error: result.error,
  };
};

/** Desktop composition root: rendering lives in @agentre-hub/agentre-ui. */
export function AgentBackendsPanel(props: {
  onOpenLlmProviders?: () => void;
  onOpenProxySettings?: () => void;
  renderHeader?: (actions: React.ReactNode) => React.ReactNode;
}) {
  // config:changed（本机写入）/ sync:applied（多端同步落地）到达时就地重拉，走面板
  // 既有的 refreshSignal 端口——不显示提示条或 loading 横幅（「Real-time refresh」）。
  const refreshSignal = useRefreshSignal();
  return (
    <SharedAgentBackendsPanel
      listCLIModels={listCLIModels}
      ports={createDesktopEngineSettingsPorts({
        onRuntimeDeviceState: (listener) =>
          EventsOn("remote.device.state", listener),
      })}
      refreshSignal={refreshSignal}
      {...props}
    />
  );
}
