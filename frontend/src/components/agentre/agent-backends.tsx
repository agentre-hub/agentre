import type React from "react";

import { AgentBackendsPanel as SharedAgentBackendsPanel } from "@agentre-ai/agentre-ui";

import { createDesktopEngineSettingsPorts } from "./engine-ports-desktop";

/** Desktop composition root: rendering lives in @agentre-ai/agentre-ui. */
export function AgentBackendsPanel(props: {
  onOpenLlmProviders?: () => void;
  onOpenProxySettings?: () => void;
  renderHeader?: (actions: React.ReactNode) => React.ReactNode;
}) {
  return <SharedAgentBackendsPanel ports={createDesktopEngineSettingsPorts()} {...props} />;
}
