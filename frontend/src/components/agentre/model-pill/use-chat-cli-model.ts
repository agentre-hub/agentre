import * as React from "react";
import {
  useCLIModels,
  type CliModelCatalog,
  type CliModelChoice,
  type CliModelPickerState,
} from "@agentre-hub/agentre-ui";
import { SetChatSessionCLIModel } from "../../../../wailsjs/go/app/App";

import { listCLIModels } from "../cli-models-port";

const EMPTY_CLI_CATALOG: CliModelCatalog = { models: [] };

export function useChatCLIModel({
  backendId,
  enabled,
  sessionId = 0,
  persistedMode = "",
  persistedValue = "",
  onChosen,
  onSwitched,
}: {
  backendId?: number;
  enabled: boolean;
  sessionId?: number;
  persistedMode?: string;
  persistedValue?: string;
  onChosen: () => void;
  onSwitched?: () => void;
}) {
  const [mode, setMode] = React.useState(persistedMode);
  const [value, setValue] = React.useState(persistedValue);
  const [error, setError] = React.useState<string | null>(null);
  const generation = React.useRef(0);
  const directory = useCLIModels({ backendId, enabled, listCLIModels });
  React.useEffect(() => {
    generation.current++;
    setMode(persistedMode);
    setValue(persistedValue);
    setError(null);
  }, [sessionId, backendId, persistedMode, persistedValue]);
  const choose = React.useCallback(
    async (choice?: CliModelChoice) => {
      const current = ++generation.current;
      const nextMode = choice?.mode ?? "",
        nextValue = choice?.mode === "fixed" ? choice.value : "";
      setError(null);
      try {
        if (sessionId > 0)
          await SetChatSessionCLIModel({
            sessionId,
            mode: nextMode,
            value: nextValue,
          });
        if (current !== generation.current) return false;
        setMode(nextMode);
        setValue(nextValue);
        onChosen();
        if (sessionId > 0) onSwitched?.();
        return true;
      } catch (err) {
        if (current === generation.current)
          setError(err instanceof Error ? err.message : String(err));
        return false;
      }
    },
    [sessionId, onChosen, onSwitched],
  );
  const clear = React.useCallback(() => {
    generation.current++;
    setMode("");
    setValue("");
    setError(null);
  }, []);
  // 下面几样都会经 useProviderPill 递进 memo 的 composer 底栏:输入不变时身份必须
  // 不变,否则宿主每重渲染一次(流式输出期间是每个文字 delta)底栏就跟着重渲染。
  const choice = React.useMemo<CliModelChoice | undefined>(
    () =>
      mode === "fixed"
        ? { mode: "fixed", value }
        : mode === "cli-default"
          ? { mode: "cli-default" }
          : undefined,
    [mode, value],
  );
  // 未启用时共享包每次渲染都现拼一份空目录;这一侧收成同一个常量。
  const catalog = enabled ? directory.catalog : EMPTY_CLI_CATALOG;
  const cliModels = React.useMemo<CliModelPickerState | undefined>(
    () =>
      enabled
        ? {
            ...directory.catalog,
            loading: directory.loading,
            choice,
            switchError: error ?? undefined,
            onChange: choose,
            onRefresh: directory.refresh,
          }
        : undefined,
    [
      enabled,
      directory.catalog,
      directory.loading,
      directory.refresh,
      choice,
      error,
      choose,
    ],
  );
  const inherit = React.useCallback(() => choose(), [choose]);
  return React.useMemo(
    () => ({
      mode,
      value,
      error,
      choice,
      cliModels,
      clear,
      inherit,
      catalog,
      loading: directory.loading,
    }),
    [
      mode,
      value,
      error,
      choice,
      cliModels,
      clear,
      inherit,
      catalog,
      directory.loading,
    ],
  );
}
