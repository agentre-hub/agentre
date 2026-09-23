import type React from "react";

import { AgentBackendsPanel as SharedAgentBackendsPanel } from "@agentre-hub/agentre-ui";
import { EventsOn } from "../../../wailsjs/runtime/runtime";

import { createDesktopEngineSettingsPorts } from "./engine-ports-desktop";
import { useRefreshSignal } from "./use-refresh-signal";

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
      ports={createDesktopEngineSettingsPorts({
        onRuntimeDeviceState: (listener) =>
          EventsOn("remote.device.state", listener),
      })}
      refreshSignal={refreshSignal}
      {...props}
    />
  );
}
