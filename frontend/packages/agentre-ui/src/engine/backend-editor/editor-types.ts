// BackendEditor 的数据形状：宿主无关的设备 / 远端供应商摘要 DTO，以及编辑器自身
// 的开合状态。放在这里是为了让编辑器的各个 hook 与字段组能共享它们而不必回头
// import 装配根（那会成环）。
import type { Backend } from "../agent-backends-shared";

export type ProviderSummary = {
  key?: string;
  name?: string;
  type?: string;
  defaultModelKey?: string;
  models?: {
    key: string;
    modelId: string;
    name?: string;
    enabled: boolean;
  }[];
};

export type EditorState =
  | { kind: "closed" }
  | { kind: "create" }
  | { kind: "edit"; backend: Backend; cliPath?: string; openBinding?: boolean };
