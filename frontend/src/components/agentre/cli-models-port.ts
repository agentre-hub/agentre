/**
 * 桌面端这一侧的 `ListCliModels` 端口：把 Wails 的 `ListCLIModels` 绑定裹成共享 Picker
 * 认的目录契约。后端设置与会话 pill 都从这里取，字段映射只有这一份。
 *
 * 查询进程不带会话模型；它的 effectiveModelId 只描述 CLI 默认，绝不作为会话的实际报告。
 */
import type { ListCliModels } from "@agentre-hub/agentre-ui";

import { ListCLIModels } from "../../../wailsjs/go/app/App";

export const listCLIModels: ListCliModels = async (request) => {
  const result = await ListCLIModels({
    backendId: Number(request.backendId),
    refresh: request.refresh,
  });
  return {
    models: result.models.map((model) => ({
      value: model.value,
      label: model.displayName,
      modelId: model.modelId,
      group: model.group,
      legacy: model.legacy,
    })),
    effectiveValue: result.effectiveValue,
    effectiveModelId: result.effectiveModelId,
    error: result.error,
  };
};
