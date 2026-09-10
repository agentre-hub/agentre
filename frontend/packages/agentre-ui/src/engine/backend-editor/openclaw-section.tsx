// OpenClaw 字段组与它的状态之间的接线：每一处改动都顺手作废上一次探测结论，
// 免得用户看着「已通过」实际上填的是另一套参数。

import { OpenClawBackendFields } from "../openclaw-backend-fields";

import type { OpenClawFields } from "./use-openclaw-fields";

export function OpenClawSection({
  fields,
  canEditToken,
  hasToken,
}: {
  fields: OpenClawFields;
  canEditToken: boolean;
  hasToken: boolean;
}) {
  return (
    <OpenClawBackendFields
      gatewayURL={fields.gatewayURL}
      onGatewayURLChange={(value) => {
        fields.setGatewayURL(value);
        fields.setProbe(null);
      }}
      token={fields.token}
      onTokenChange={(value) => {
        fields.setToken(value);
        if (value !== "") fields.setClearToken(false);
        fields.setProbe(null);
      }}
      canEditToken={canEditToken}
      hasToken={hasToken}
      clearToken={fields.clearToken}
      onClearTokenChange={(value) => {
        fields.setClearToken(value);
        if (value) fields.setToken("");
      }}
      agentID={fields.agentID}
      onAgentIDChange={(value) => {
        fields.setAgentID(value);
        fields.setProbe(null);
      }}
      defaultModel={fields.defaultModel}
      onDefaultModelChange={(value) => {
        fields.setDefaultModel(value);
        fields.setProbe(null);
      }}
      probe={fields.probe}
    />
  );
}
