import type { EngineID } from "../ports";

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

export type CliModelPickerState = CliModelCatalog & {
  loading?: boolean;
  // 未选中 CLI 登录态时为空，不能把供应商选中态误画成 CLI 默认。
  choice?: CliModelChoice;
  onChange: (choice: CliModelChoice) => void;
  onRefresh: () => void;
};

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
