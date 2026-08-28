// ModelTargetPicker 共享类型（spec 2026-08-11-llm-provider-models「ModelTarget
// contract / UI, accessibility and recent targets」）。
//
// 业务身份只有 providerKey + modelKey：
//   - 两个都空 = native（backend）/ inherit-agent（chat）/ inherit-main（route）；
//   - ProviderKey 非空且 ModelKey 空 = provider-default；
//   - 两个都非空 = fixed-model。
export type ModelTarget = { providerKey: string; modelKey: string };

// PickerScenario 决定顶部特殊项与最近使用的作用域：
//   - backend：顶部特殊项是「CLI 自身登录态」（native）；
//   - chat：顶部特殊项是「跟随 Agent 绑定」（inherit-agent）；
//   - route：顶部特殊项是「继承主绑定」（inherit-main）。
export type PickerScenario = "backend" | "chat" | "route";

// PickerModel 一条可选的固定模型。enabled=false 的模型不可选（目标已失效）。
// contextWindow / maxOutput 供选项行右侧展示上下文窗口与最大输出（纯前端展示）。
export type PickerModel = {
  modelKey: string;
  modelId: string;
  name?: string;
  enabled: boolean;
  contextWindow?: number;
  maxOutput?: number;
};

// PickerProvider 一个 Provider 组。defaultModel 是 provider-default 项（当前默认模型的
// 解析结果，可能为 nil = 默认模型缺失/停用）。models 是 fixed-model 列表。
export type PickerProvider = {
  providerKey: string;
  id: number;
  name: string;
  type: string;
  enabled: boolean;
  defaultModel: PickerModel | null;
  models: PickerModel[];
};

// providerCompatibleForBackend 按 effective backend 类型过滤 provider.type 兼容性：
//   - claudecode 只收 anthropic；
//   - codex 只收 openai-response；
//   - piagent 收 anthropic / openai-chat / openai-response；
//   - builtin（与 chat/route 的继承来源）收所有 Agentre provider。
export function providerCompatibleForBackend(
  backendType: string,
  providerType: string,
): boolean {
  switch (backendType) {
    case "claudecode":
      return providerType === "anthropic";
    case "codex":
      return providerType === "openai-response";
    case "piagent":
      return (
        providerType === "anthropic" ||
        providerType === "openai-chat" ||
        providerType === "openai-response"
      );
    default:
      return true;
  }
}

export function isNativeTarget(t: ModelTarget | null | undefined): boolean {
  return !t || (t.providerKey === "" && t.modelKey === "");
}

export function sameTarget(
  a: ModelTarget | null | undefined,
  b: ModelTarget | null | undefined,
): boolean {
  const pa = a?.providerKey ?? "";
  const pb = b?.providerKey ?? "";
  const ma = a?.modelKey ?? "";
  const mb = b?.modelKey ?? "";
  return pa === pb && ma === mb;
}
