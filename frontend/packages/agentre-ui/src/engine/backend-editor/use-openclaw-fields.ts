// OpenClaw 网关后端那一族字段的状态：网关地址 / Agent / 默认模型 / token 与
// 「清空 token」勾选，外加最近一次探测结果 —— 任何一个字段被改动都会让探测结论作废。
import * as React from "react";

import { OPENCLAW_DEFAULT_GATEWAY_URL } from "../openclaw-backend-fields";
import type { agent_backend_svc } from "../port-bridge";
import type { Backend, BackendType } from "../agent-backends-shared";

export type OpenClawFields = ReturnType<typeof useOpenClawFields>;

export function useOpenClawFields(editing: Backend | null) {
  const [gatewayURL, setGatewayURL] = React.useState(
    editing?.openClawGatewayUrl || OPENCLAW_DEFAULT_GATEWAY_URL,
  );
  const [agentID, setAgentID] = React.useState(editing?.openClawAgentId ?? "");
  const [defaultModel, setDefaultModel] = React.useState(
    editing?.openClawDefaultModel ?? "",
  );
  const [token, setToken] = React.useState("");
  const [clearToken, setClearToken] = React.useState(false);
  const [probe, setProbe] =
    React.useState<agent_backend_svc.TestBackendResponse | null>(null);

  // 切换后端类型时的重置：探测结论与 token 一律作废；切到 openclaw 才把三个
  // 网关字段恢复成新建默认值（网关后端同样要指明由哪台机器去拨它：配对用的设备
  // 身份种子就存在那台机器的 keychain 里，配对关系本来就钉在一台机器上，规格决策 6）。
  function resetForType(nextType: BackendType) {
    setProbe(null);
    setToken("");
    setClearToken(false);
    if (nextType === "openclaw") {
      setGatewayURL(OPENCLAW_DEFAULT_GATEWAY_URL);
      setAgentID("");
      setDefaultModel("");
    }
  }

  // 探测通过后把网关报上来的可用 Agent / 模型补进还空着的字段：只填用户没填过的，
  // 不覆盖他自己的选择。
  function applyProbe(res: agent_backend_svc.TestBackendResponse) {
    setProbe(res);
    if (agentID === "" && (res.openClawAgents ?? []).length > 0) {
      const selected =
        res.openClawAgents.find((agent) => agent.default) ??
        res.openClawAgents[0];
      setAgentID(selected.id);
    }
    if (defaultModel === "" && (res.openClawModels ?? []).length > 0) {
      const available =
        res.openClawModels.find((model) => model.available) ??
        res.openClawModels[0];
      setDefaultModel(available.id);
    }
  }

  return {
    gatewayURL,
    setGatewayURL,
    agentID,
    setAgentID,
    defaultModel,
    setDefaultModel,
    token,
    setToken,
    clearToken,
    setClearToken,
    probe,
    setProbe,
    resetForType,
    applyProbe,
  };
}
