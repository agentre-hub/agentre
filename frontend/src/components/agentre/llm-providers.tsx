import type React from "react";

import { LlmProvidersPanel as SharedLlmProvidersPanel } from "@agentre-hub/agentre-ui";

import { createDesktopEngineSettingsPorts } from "./engine-ports-desktop";

/** Desktop composition root: rendering lives in @agentre-hub/agentre-ui. */
export function LlmProvidersPanel(props: {
  onOpenAgentBackends?: () => void;
  renderHeader?: (actions: React.ReactNode) => React.ReactNode;
}) {
  return (
    <SharedLlmProvidersPanel
      ports={createDesktopEngineSettingsPorts()}
      {...props}
    />
  );
}
