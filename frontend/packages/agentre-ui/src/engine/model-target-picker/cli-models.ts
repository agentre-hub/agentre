import type { EngineID } from "../ports";
import type { ProviderPillState } from "./provider-pill-trigger";

// CLI 身份与 Agentre 供应商目标分离：value 原样交给对应 CLI，不伪装成 modelKey。
export type CliModelOption = {
  value: string;
  label: string;
  modelId: string;
  group?: string;
  legacy?: boolean;
};

export type CliModelChoice =
  | { mode: "cli-default" }
  | { mode: "fixed"; value: string };

export type CliModelCatalog = {
  models: CliModelOption[];
  effectiveValue?: string;
  effectiveModelId?: string;
  error?: string;
};

// 宿主负责 Wails / HTTP / 设备路由，Picker 不接触传输。
export type ListCliModels = (request: {
  backendId: EngineID;
  refresh: boolean;
}) => Promise<CliModelCatalog>;

// 一轮进行中改了选择：这一轮仍用 current 跑完，next 从下一轮起生效（spec「会话：composer
// 模型 pill」）。nextLabel 为空时按 nextMode 写跟随文案。
export type CliModelPendingSwitch = {
  currentModelId: string;
  currentLabel: string;
  nextLabel: string;
  nextMode: "follow-agent" | "cli-default" | "fixed";
};

export type CliModelPickerState = CliModelCatalog & {
  loading?: boolean;
  pending?: CliModelPendingSwitch;
  // 会话切换失败的 CLI 原文，与目录查询错误分开，在弹层底部呈现。
  switchError?: string;
  // 未选中 CLI 登录态时为空，不能把供应商选中态误画成 CLI 默认。
  choice?: CliModelChoice;
  onChange: (
    choice: CliModelChoice,
  ) => void | boolean | Promise<void | boolean>;
  onRefresh: () => void;
};

// resolveCLIPillState 只把会话报告当作实际模型；目录默认仅描述可选择的默认项。
export function resolveCLIPillState(input: {
  catalog: CliModelCatalog;
  choice?: CliModelChoice;
  backendType: string;
  boundValue?: string;
  actualModelId?: string;
  pending?: boolean;
  error?: string;
  loading?: boolean;
}): ProviderPillState {
  const { catalog, choice, actualModelId = "", boundValue = "" } = input;
  const selectedValue =
    choice?.mode === "fixed" ? choice.value : choice ? "" : boundValue;
  // 跟随 Agent 绑定的值还在等 CLI 目录（spec「尚未解析」）：原样值不是展示名，宁可转圈不写；
  // 目录到了仍查不到才是手输值，照原样显示。
  const awaitingCatalog =
    !choice &&
    !!input.loading &&
    !catalog.models.some((m) => m.value === selectedValue);
  const selected =
    selectedValue && !awaitingCatalog
      ? cliModelResolution(catalog, { mode: "fixed", value: selectedValue })
      : { label: "", modelId: "" };
  const actual = catalog.models.find((m) => m.modelId === actualModelId);
  const pending: ProviderPillState["pending"] =
    input.pending && !input.error
      ? { label: selected.label, mode: choice?.mode ?? "follow-agent" }
      : undefined;
  const modelId = actualModelId || (!pending ? selected.modelId : "");
  const modelLabel = actualModelId
    ? actual?.label || actualModelId
    : !pending
      ? selected.label
      : "";
  return {
    mode: input.error
      ? "invalid"
      : choice?.mode === "cli-default"
        ? "cli-default"
        : choice
          ? "fixed"
          : "follow-agent",
    modelId,
    modelLabel,
    resolutionLabel: modelLabel,
    providerLabel: "",
    providerType: "",
    dynamic:
      choice?.mode === "cli-default" || (!choice && input.boundValue === ""),
    cliLogin: true,
    nativeCLI: true,
    backendType: input.backendType,
    pending,
    loading: input.loading && !modelId,
  };
}

export function cliModelResolution(
  catalog: CliModelCatalog,
  choice: CliModelChoice,
): { label: string; modelId: string } {
  const value = choice.mode === "fixed" ? choice.value : catalog.effectiveValue;
  const model = catalog.models.find((item) => item.value === value);
  const modelId =
    choice.mode === "fixed"
      ? model?.modelId || choice.value
      : catalog.effectiveModelId || "";
  return { label: model?.label || modelId, modelId };
}
