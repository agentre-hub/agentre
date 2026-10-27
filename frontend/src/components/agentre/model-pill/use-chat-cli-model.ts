import * as React from "react";
import {
  useCLIModels,
  type CliModelChoice,
  type CliModelPickerState,
} from "@agentre-hub/agentre-ui";
import { SetChatSessionCLIModel } from "../../../../wailsjs/go/app/App";

import { listCLIModels } from "../cli-models-port";

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
  const choice: CliModelChoice | undefined =
    mode === "fixed"
      ? { mode: "fixed", value }
      : mode === "cli-default"
        ? { mode: "cli-default" }
        : undefined;
  const cliModels: CliModelPickerState | undefined = enabled
    ? {
        ...directory.catalog,
        loading: directory.loading,
        choice,
        switchError: error ?? undefined,
        onChange: choose,
        onRefresh: directory.refresh,
      }
    : undefined;
  return {
    mode,
    value,
    error,
    choice,
    cliModels,
    clear,
    inherit: () => choose(),
    catalog: directory.catalog,
    loading: directory.loading,
  };
}
