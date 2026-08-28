import * as React from "react";
import { useUiTranslation as useTranslation } from "../../i18n";
import { Loader2, RefreshCw, Sparkles } from "lucide-react";

import { Button } from "../../ui/button";

import {
  type BatchDeleteResult,
  BatchDeleteDialog,
} from "./batch-delete-dialog";
import { ModelsTable } from "./models-table";
import { ProviderWorkspaceHeader } from "./workspace-header";
import { ModelsToolbar } from "./workspace-toolbar";
import { type Model, type Provider, type ReferenceCounts } from "./index";

export type WorkspaceHandlers = {
  onAddModel: () => void;
  onCopyProviderKey: () => void;
  onDeleteModel: (model: Model) => void;
  onDeleteProvider: () => void;
  onDiscover: () => void;
  onEditConnection: () => void;
  onEditModel: (model: Model) => void;
  onRetryModels: () => void;
  onSetDefault: (model: Model) => void;
  onTestModel: (model: Model) => void;
  onTestProvider: () => void;
  onToggleModelEnabled: (model: Model) => void;
  onToggleProviderEnabled: () => void;
  onBatchToggleEnabled: (models: Model[], enabled: boolean) => Promise<void>;
  onBatchDeleteCompleted: (result: BatchDeleteResult) => void;
  canDiscover?: boolean;
  canTestProvider?: boolean;
};

export function ProviderWorkspace({
  provider,
  models,
  modelsError,
  modelsLoading,
  onTestModel,
  onTestProvider,
  onEditConnection,
  onDeleteProvider,
  onDiscover,
  onAddModel,
  onCopyProviderKey,
  onSetDefault,
  onToggleModelEnabled,
  onEditModel,
  onDeleteModel,
  onToggleProviderEnabled,
  onBatchToggleEnabled,
  onBatchDeleteCompleted,
  onRetryModels,
  canDiscover = true,
  canTestProvider = true,
  testingDefault,
  testingModelId,
  passedModelTests,
  providerRefCounts,
  modelRefCounts,
}: {
  provider: Provider;
  models: Model[];
  modelsError: string | null;
  modelsLoading: boolean;
  testingDefault: boolean;
  testingModelId: number | null;
  // 会话内瞬时的「已通过」：model.id → 本次测试耗时；不持久化，刷新即消失。
  passedModelTests: ReadonlyMap<number, string>;
  providerRefCounts: ReferenceCounts | null;
  modelRefCounts: Map<string, ReferenceCounts>;
} & WorkspaceHandlers) {
  const { t } = useTranslation();
  const [search, setSearch] = React.useState("");
  const [selected, setSelected] = React.useState<Set<number>>(new Set());
  const [batchDelete, setBatchDelete] = React.useState<Model[] | null>(null);

  React.useEffect(() => {
    setSearch("");
    setSelected(new Set());
    setBatchDelete(null);
  }, [provider.id]);

  const trimmed = search.trim().toLowerCase();
  const visible = trimmed
    ? models.filter(
        (m) =>
          m.modelId.toLowerCase().includes(trimmed) ||
          m.modelKey.toLowerCase().includes(trimmed) ||
          m.name.toLowerCase().includes(trimmed),
      )
    : models;

  const selectedModels = visible.filter((m) => selected.has(m.id));
  const allVisibleSelected =
    visible.length > 0 && visible.every((m) => selected.has(m.id));

  const handleBatchToggle = async (enabled: boolean) => {
    if (selectedModels.length === 0) return;
    await onBatchToggleEnabled(selectedModels, enabled);
    setSelected(new Set());
  };

  const handleBatchDelete = () => {
    if (selectedModels.length === 0) return;
    setBatchDelete(selectedModels);
  };

  return (
    <div
      role="region"
      aria-label={t("llmProviders.workspace.ariaLabel", {
        name: provider.name,
      })}
      className="@container flex min-w-0 flex-col overflow-hidden"
    >
      <ProviderWorkspaceHeader
        provider={provider}
        models={models}
        providerRefCounts={providerRefCounts}
        testingDefault={testingDefault}
        canTestProvider={canTestProvider}
        canDiscover={canDiscover}
        onToggleProviderEnabled={onToggleProviderEnabled}
        onTestProvider={onTestProvider}
        onDiscover={onDiscover}
        onEditConnection={onEditConnection}
        onCopyProviderKey={onCopyProviderKey}
        onDeleteProvider={onDeleteProvider}
      />

      <ModelsToolbar
        search={search}
        onSearchChange={setSearch}
        selectedCount={selected.size}
        visibleCount={visible.length}
        totalCount={models.length}
        onSelectAll={() => setSelected(new Set(visible.map((m) => m.id)))}
        onClearSelection={() => setSelected(new Set())}
        onBatchEnable={() => void handleBatchToggle(true)}
        onBatchDisable={() => void handleBatchToggle(false)}
        onBatchDelete={handleBatchDelete}
        onAddModel={onAddModel}
      />

      {/* Model table */}
      {modelsError ? (
        <div
          role="alert"
          className="flex flex-col items-start gap-2 p-4 text-status-error"
        >
          <span className="text-xs">{modelsError}</span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-7 gap-1.5 text-2xs"
            onClick={onRetryModels}
          >
            <RefreshCw
              className="size-3"
              data-icon="inline-start"
              aria-hidden="true"
            />
            {t("llmProviders.workspace.retry")}
          </Button>
        </div>
      ) : modelsLoading && models.length === 0 ? (
        <div className="flex items-center justify-center gap-2 py-8 text-2xs text-muted-foreground">
          <Loader2 className="size-4 animate-spin" aria-hidden="true" />
          {t("llmProviders.workspace.loadingModels")}
        </div>
      ) : visible.length === 0 ? (
        <div className="flex flex-col items-center gap-2 px-6 py-8 text-center">
          <div
            aria-hidden="true"
            className="flex size-10 items-center justify-center rounded-full bg-primary-soft text-primary-text"
          >
            <Sparkles className="size-4" />
          </div>
          <p className="text-sm font-semibold">
            {t("llmProviders.workspace.noModelsTitle")}
          </p>
          <p className="max-w-xs text-2xs leading-relaxed text-muted-foreground">
            {t("llmProviders.workspace.noModelsDescription")}
          </p>
          {/* 手动添加已常驻工具栏，空态只留「发现模型」这一条主路径 */}
          <div className="flex flex-wrap items-center justify-center gap-2 pt-1">
            {canDiscover ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-[30px] gap-1.5 px-3 text-xs"
                onClick={onDiscover}
              >
                <RefreshCw
                  className="size-3.5"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
                {t("llmProviders.workspace.discoverModels")}
              </Button>
            ) : null}
          </div>
        </div>
      ) : (
        <div className="min-w-0 flex-1 overflow-auto">
          <ModelsTable
            provider={provider}
            models={visible}
            selected={selected}
            allVisibleSelected={allVisibleSelected}
            onSelectAll={() => setSelected(new Set(visible.map((m) => m.id)))}
            onClearSelection={() => setSelected(new Set())}
            onToggleRow={(modelId, checked) =>
              setSelected((prev) => {
                const next = new Set(prev);
                if (checked) next.add(modelId);
                else next.delete(modelId);
                return next;
              })
            }
            modelRefCounts={modelRefCounts}
            passedModelTests={passedModelTests}
            testingModelId={testingModelId}
            onSetDefault={onSetDefault}
            onToggleModelEnabled={onToggleModelEnabled}
            onTestModel={onTestModel}
            onEditModel={onEditModel}
            onDeleteModel={onDeleteModel}
          />
        </div>
      )}

      <BatchDeleteDialog
        models={batchDelete}
        defaultModelKey={provider.defaultModelKey}
        modelRefCounts={modelRefCounts}
        onClose={() => setBatchDelete(null)}
        onDone={(result) => {
          setBatchDelete(null);
          setSelected(new Set());
          onBatchDeleteCompleted(result);
        }}
      />
    </div>
  );
}
