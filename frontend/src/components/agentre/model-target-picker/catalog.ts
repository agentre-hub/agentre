import * as React from "react";

import { ListLLMModels } from "../../../../wailsjs/go/app/App";
import { llm_provider_svc } from "../../../../wailsjs/go/models";
import {
  buildPickerCatalog,
  type PickerProvider,
} from "@agentre-hub/agentre-ui";

export type ModelTargetCatalogState = {
  catalog: PickerProvider[];
  loading: boolean;
  error: boolean;
  refresh: () => void;
};

function invalidateGeneration(
  generationRef: React.MutableRefObject<number>,
  generation: number,
) {
  if (generationRef.current === generation) {
    generationRef.current++;
  }
}

/**
 * Desktop data adapter for the shared picker. The picker owns catalog shaping;
 * only its Wails query remains at the host boundary.
 */
export function useModelTargetCatalog(
  providers: llm_provider_svc.ProviderItem[],
): ModelTargetCatalogState {
  const [modelsByProvider, setModelsByProvider] = React.useState<
    Map<number, llm_provider_svc.ModelItem[]>
  >(new Map());
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState(false);
  const generationRef = React.useRef(0);

  const refresh = React.useCallback(() => {
    const generation = ++generationRef.current;
    setLoading(true);
    setError(false);
    void (async () => {
      const next = new Map<number, llm_provider_svc.ModelItem[]>();
      let anyFailed = false;
      let anyOk = false;
      await Promise.all(
        providers.map(async (provider) => {
          try {
            const response = await ListLLMModels(
              new llm_provider_svc.ListModelsRequest({ id: provider.id }),
            );
            next.set(provider.id, response.items ?? []);
            anyOk = true;
          } catch {
            anyFailed = true;
          }
        }),
      );
      if (generationRef.current !== generation) return;
      setModelsByProvider(next);
      setError(anyFailed && !anyOk);
      setLoading(false);
    })();
  }, [providers]);

  React.useEffect(() => {
    refresh();
    const generation = generationRef.current;
    return () => {
      invalidateGeneration(generationRef, generation);
    };
  }, [refresh]);

  return {
    catalog: buildPickerCatalog(providers, modelsByProvider),
    loading,
    error,
    refresh,
  };
}
