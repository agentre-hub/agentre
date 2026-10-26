import * as React from "react";

import { useUiTranslation } from "../../i18n";
import type { EngineID } from "../ports";
import { messageFromError } from "../agent-backends-utils";
import type {
  CliModelCatalog,
  ListCliModels,
} from "../model-target-picker/cli-models";

// 目录只能代表已保存的设备/后端配置；草稿改路径或环境后宁可提示保存，也不展示旧机器的结果。
export function useCLIModels({
  backendId,
  enabled,
  configurationChanged = false,
  listCLIModels,
}: {
  backendId?: EngineID;
  enabled: boolean;
  configurationChanged?: boolean;
  listCLIModels?: ListCliModels;
}) {
  const { t } = useUiTranslation();
  const generation = React.useRef(0);
  const [state, setState] = React.useState<{
    backendId?: EngineID;
    loading: boolean;
    catalog: CliModelCatalog;
  }>({ loading: false, catalog: { models: [] } });
  const load = React.useCallback(
    async (refresh: boolean) => {
      const current = ++generation.current;
      if (!enabled || !backendId || configurationChanged || !listCLIModels)
        return;
      setState({ backendId, loading: true, catalog: { models: [] } });
      try {
        const catalog = await listCLIModels({ backendId, refresh });
        if (generation.current === current)
          setState({ backendId, loading: false, catalog });
      } catch (error) {
        if (generation.current === current)
          setState({
            backendId,
            loading: false,
            catalog: { models: [], error: messageFromError(error, t) },
          });
      }
    },
    [backendId, enabled, configurationChanged, listCLIModels, t],
  );
  const invalidate = React.useCallback(() => {
    generation.current++;
  }, []);
  React.useEffect(() => {
    void load(false);
    return invalidate;
  }, [load, invalidate]);
  const refresh = React.useCallback(() => {
    void load(true);
  }, [load]);
  const blocked = !backendId
    ? t("modelTargetPicker.cli.saveFirst")
    : configurationChanged
      ? t("modelTargetPicker.cli.saveChanges")
      : !listCLIModels
        ? t("modelTargetPicker.cli.unavailable")
        : "";
  if (!enabled || blocked)
    return {
      loading: false,
      catalog: {
        models: [],
        ...(enabled ? { error: blocked } : {}),
      } as CliModelCatalog,
      refresh,
    };
  return {
    ...(state.backendId === backendId
      ? state
      : { loading: true, catalog: { models: [] } }),
    refresh,
  };
}
