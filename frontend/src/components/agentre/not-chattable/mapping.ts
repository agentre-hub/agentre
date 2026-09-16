export type BlockReason =
  | "no-backend"
  | "backend-requires-provider"
  | "provider-inactive"
  | "remote-provider-missing"
  | "gateway-not-running"
  | "remote-openclaw-unavailable"
  | "remote-hermes-unavailable"
  | "unknown-backend";

export type GuidanceTarget =
  | "org-agent:<id>"
  | "settings:agent-backend"
  | "settings:llm-providers"
  | "settings:remote-devices";

export type BlockReasonCta = {
  primaryLabel: string;
  primaryTarget: GuidanceTarget;
  secondaryLabel: string;
  copyKey: string;
};

const ctaByReason: Record<BlockReason, BlockReasonCta> = {
  "no-backend": {
    primaryLabel: "chatPage.notChattable.actions.configureBackend",
    primaryTarget: "org-agent:<id>",
    secondaryLabel: "chatPage.notChattable.actions.backendSettings",
    copyKey: "chatPage.notChattable.reasons.noBackend",
  },
  "backend-requires-provider": {
    primaryLabel: "chatPage.notChattable.actions.configureProvider",
    primaryTarget: "settings:llm-providers",
    secondaryLabel: "chatPage.notChattable.actions.backendSettings",
    copyKey: "chatPage.notChattable.reasons.backendRequiresProvider",
  },
  "provider-inactive": {
    primaryLabel: "chatPage.notChattable.actions.configureProvider",
    primaryTarget: "settings:llm-providers",
    secondaryLabel: "chatPage.notChattable.actions.backendSettings",
    copyKey: "chatPage.notChattable.reasons.providerInactive",
  },
  "remote-provider-missing": {
    primaryLabel: "chatPage.notChattable.actions.configureRemoteDevice",
    primaryTarget: "settings:remote-devices",
    secondaryLabel: "chatPage.notChattable.actions.backendSettings",
    copyKey: "chatPage.notChattable.reasons.remoteProviderMissing",
  },
  "gateway-not-running": {
    primaryLabel: "chatPage.notChattable.actions.startGateway",
    primaryTarget: "settings:agent-backend",
    secondaryLabel: "chatPage.notChattable.actions.backendSettings",
    copyKey: "chatPage.notChattable.reasons.gatewayNotRunning",
  },
  "remote-openclaw-unavailable": {
    primaryLabel: "chatPage.notChattable.actions.configureRemoteDevice",
    primaryTarget: "settings:remote-devices",
    secondaryLabel: "chatPage.notChattable.actions.backendSettings",
    copyKey: "chatPage.notChattable.reasons.remoteOpenclawUnavailable",
  },
  "remote-hermes-unavailable": {
    primaryLabel: "chatPage.notChattable.actions.backendSettings",
    primaryTarget: "settings:agent-backend",
    secondaryLabel: "chatPage.notChattable.actions.backendSettings",
    copyKey: "chatPage.notChattable.reasons.remoteHermesUnavailable",
  },
  "unknown-backend": {
    primaryLabel: "chatPage.notChattable.actions.backendSettings",
    primaryTarget: "settings:agent-backend",
    secondaryLabel: "chatPage.notChattable.actions.backendSettings",
    copyKey: "chatPage.notChattable.reasons.unknownBackend",
  },
};

export function blockReasonToCta(blockReason: string): BlockReasonCta {
  return (
    ctaByReason[blockReason as BlockReason] ?? ctaByReason["unknown-backend"]
  );
}

export const ORG_SELECTED_STORAGE_KEY = "agentre.orgTree.selected";

export function isBlockReason(value: string): value is BlockReason {
  return value in ctaByReason;
}
