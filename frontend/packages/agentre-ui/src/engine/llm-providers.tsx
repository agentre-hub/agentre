import * as React from "react";
import { useUiTranslation as useTranslation } from "../i18n";
import {
  AlertCircle,
  ArrowRight,
  CheckCircle2,
  Loader2,
  Plus,
  Sparkles,
} from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "../ui/alert";
import { Button } from "../ui/button";

import { llm_provider_svc, useEngineSettingsBridge } from "./port-bridge";
import type { EngineSettingsPorts } from "./ports";
import {
  EngineSettingsPortsProvider,
  useEngineSettingsPorts,
} from "./ports-context";
import { cn } from "../lib/utils";
import {
  type DeleteTarget,
  DeleteDialog,
} from "./llm-provider-models/delete-dialog";
import { type BatchDeleteResult } from "./llm-provider-models/batch-delete-dialog";
import {
  type DefaultModelTarget,
  DefaultModelDialog,
} from "./llm-provider-models/default-model-dialog";
import { AddModelDialog } from "./llm-provider-models/add-model-dialog";
import { DiscoverDialog } from "./llm-provider-models/discover-dialog";
import { ModelEditDialog } from "./llm-provider-models/model-edit-dialog";
import {
  type ProviderFormMode,
  type ProviderFormValues,
  ProviderFormDialog,
} from "./llm-provider-models/provider-form-dialog";
import { ProviderNav } from "./llm-provider-models/provider-nav";
import { ProviderWorkspace } from "./llm-provider-models/provider-workspace";
import { useProviderActions } from "./llm-provider-models/use-provider-actions";
import {
  useProviderCatalog,
  type PanelFlash,
} from "./llm-provider-models/use-provider-catalog";
import {
  type Model,
  type Provider,
  type ReferenceCounts,
} from "./llm-provider-models/index";

type LlmProvidersPanelProps = {
  onOpenAgentBackends?: () => void;
  // 页头由宿主渲染，面板把自己的页级操作（新增供应商）交进去：按钮要落在 H1
  // 行，而它开的创建弹窗仍归面板持有。
  renderHeader?: (actions: React.ReactNode) => React.ReactNode;
};

// 宿主传进来的那一份端口只覆盖本面板的子树：两个面板同时挂载时各用各的，
// 与谁最后渲染无关。
export function LlmProvidersPanel({
  ports,
  ...props
}: LlmProvidersPanelProps & { ports: EngineSettingsPorts }) {
  return (
    <EngineSettingsPortsProvider ports={ports}>
      <LlmProvidersPanelBody {...props} />
    </EngineSettingsPortsProvider>
  );
}

function LlmProvidersPanelBody({
  onOpenAgentBackends,
  renderHeader,
}: LlmProvidersPanelProps) {
  const ports = useEngineSettingsPorts();
  const bridge = useEngineSettingsBridge();
  const { t } = useTranslation();
  const [flash, setFlash] = React.useState<PanelFlash>(null);
  // 读路径（清单 / 模型表 / 引用计数）与写路径（测试、启停、播报）各自成钩子，
  // 中间只靠 flash 槽位与两个 refresh 相连。
  const catalog = useProviderCatalog({ bridge, setFlash });
  const {
    providers,
    providersLoading,
    selectedId,
    setSelectedId,
    selectedProvider,
    models,
    modelsLoading,
    modelsError,
    providerRefCounts,
    modelRefCounts,
    refreshProviders,
    refreshModels,
  } = catalog;
  const [formMode, setFormMode] = React.useState<ProviderFormMode | null>(null);
  const [discoverProvider, setDiscoverProvider] =
    React.useState<Provider | null>(null);
  const [addModelProvider, setAddModelProvider] =
    React.useState<Provider | null>(null);
  const [editModel, setEditModel] = React.useState<Model | null>(null);
  const [deleteTarget, setDeleteTarget] = React.useState<DeleteTarget | null>(
    null,
  );
  const [setDefaultTarget, setSetDefaultTarget] =
    React.useState<DefaultModelTarget | null>(null);
  const actions = useProviderActions({
    bridge,
    selectedProvider,
    setFlash,
    refreshProviders,
    refreshModels,
  });

  // 修改 Provider 默认模型前先展示动态影响（Backend / Session / Route 数量）并二次确认
  // （spec 2026-08-11「Provider management」：保存不改写引用、不批量插入会话 notice，
  // 默认变化自下一轮被 provider-default 动态跟随）。
  const handleSetDefault = React.useCallback(
    (model: Model) => {
      const provider = providers.find((p) => p.id === model.providerId) ?? null;
      if (!provider) return;
      setSetDefaultTarget({ provider, model });
    },
    [providers],
  );

  const handleDefaultSaved = React.useCallback(async () => {
    setFlash({
      kind: "ok",
      text: t("llmProviders.flash.defaultSet", {
        model: setDefaultTarget?.model.modelId ?? "",
      }),
    });
    await refreshProviders();
    await refreshModels();
  }, [refreshModels, refreshProviders, setDefaultTarget?.model.modelId, t]);

  const handleFormSubmit = React.useCallback(
    async (mode: ProviderFormMode, values: ProviderFormValues) => {
      if (mode.kind === "create") {
        await bridge.CreateLLMProvider(
          new llm_provider_svc.CreateProviderRequest({
            type: values.type,
            name: values.name.trim(),
            apiKey: values.apiKey.trim(),
            baseUrl: values.baseUrl.trim(),
            models: values.models.map(
              (m) =>
                new llm_provider_svc.ModelInput({
                  modelId: m.modelId,
                  name: m.name,
                  contextWindow: m.contextWindow,
                  maxOutput: m.maxOutput,
                }),
            ),
            defaultModelId: values.defaultModelId,
          }),
        );
        setFlash({
          kind: "ok",
          text: t("llmProviders.flash.created", {
            name: values.name.trim(),
          }),
        });
        setFormMode(null);
        await refreshProviders();
      } else {
        await bridge.UpdateLLMProvider(
          new llm_provider_svc.UpdateProviderRequest({
            id: mode.provider.id,
            name: values.name.trim(),
            apiKey: values.apiKey,
            baseUrl: values.baseUrl.trim(),
          }),
        );
        setFlash({
          kind: "ok",
          text: t("llmProviders.flash.updated", {
            name: values.name.trim(),
          }),
        });
        setFormMode(null);
        await refreshProviders();
      }
    },
    [bridge, refreshProviders, t],
  );

  // 空态自带「添加第一个供应商」CTA，此时页头不再出第二个——全页始终只有一个入口。
  const headerActions =
    providersLoading || providers.length === 0 ? null : (
      <Button
        type="button"
        size="sm"
        className="h-[30px] gap-1.5 px-3 text-xs"
        onClick={() => setFormMode({ kind: "create" })}
      >
        <Plus data-icon="inline-start" aria-hidden="true" />
        {t("llmProviders.page.add")}
      </Button>
    );

  return (
    <>
      {renderHeader?.(headerActions)}
      <div className="flex min-w-0 flex-col gap-3">
        {flash ? (
          <Alert
            className={cn(
              "py-2",
              flash.kind === "ok"
                ? "border-status-running/40 bg-status-running/10 text-status-running"
                : "border-status-error/40 bg-status-error/10 text-status-error",
            )}
          >
            {flash.kind === "ok" ? (
              <CheckCircle2 className="size-4" aria-hidden="true" />
            ) : (
              <AlertCircle className="size-4" aria-hidden="true" />
            )}
            <AlertTitle className="text-xs font-semibold">
              {flash.kind === "ok"
                ? t("common.operationSucceeded")
                : t("common.errorOccurred")}
            </AlertTitle>
            <AlertDescription className="text-2xs">
              {flash.text}
            </AlertDescription>
          </Alert>
        ) : null}

        {providersLoading ? (
          <div className="flex items-center justify-center gap-2 py-10 text-2xs text-muted-foreground">
            <Loader2 className="size-4 animate-spin" aria-hidden="true" />
            {t("llmProviders.page.loadingProviders")}
          </div>
        ) : providers.length === 0 ? (
          <ProvidersEmptyState
            onCreate={() => setFormMode({ kind: "create" })}
            onOpenAgentBackends={onOpenAgentBackends}
          />
        ) : (
          <div className="min-w-0 overflow-hidden rounded-lg border border-border bg-card">
            <ProviderManagement
              providers={providers}
              selectedId={selectedId}
              onSelect={setSelectedId}
              selectedProvider={selectedProvider}
              models={models}
              modelsLoading={modelsLoading}
              modelsError={modelsError}
              onRetryModels={() => void refreshModels()}
              testingDefault={actions.testingDefault}
              testingModelId={actions.testingModelId}
              passedModelTests={actions.passedModelTests}
              providerRefCounts={providerRefCounts}
              modelRefCounts={modelRefCounts}
              onTestProvider={actions.handleTestProvider}
              onTestModel={actions.handleTestModel}
              canTestProvider={Boolean(ports.testProvider)}
              onEditConnection={() => {
                if (selectedProvider)
                  setFormMode({ kind: "edit", provider: selectedProvider });
              }}
              onDiscover={() => setDiscoverProvider(selectedProvider)}
              canDiscover={Boolean(ports.discoverModels)}
              onAddModel={() => setAddModelProvider(selectedProvider)}
              onCopyProviderKey={() => {
                if (selectedProvider)
                  void actions.handleCopyProviderKey(selectedProvider);
              }}
              onDeleteProvider={() =>
                selectedProvider
                  ? setDeleteTarget({
                      kind: "provider",
                      provider: selectedProvider,
                    })
                  : undefined
              }
              onSetDefault={handleSetDefault}
              onToggleModelEnabled={actions.handleToggleModelEnabled}
              onEditModel={setEditModel}
              onDeleteModel={(model) =>
                setDeleteTarget({ kind: "model", model })
              }
              onToggleProviderEnabled={actions.handleToggleProviderEnabled}
              onBatchToggleEnabled={actions.handleBatchToggleEnabled}
              onBatchDeleteCompleted={actions.handleBatchDeleteCompleted}
            />
          </div>
        )}

        <ProviderFormDialog
          mode={formMode}
          onClose={() => setFormMode(null)}
          onSubmit={handleFormSubmit}
        />
        <DiscoverDialog
          provider={discoverProvider}
          existingModels={models}
          onClose={() => setDiscoverProvider(null)}
          onEditConnection={() => {
            if (discoverProvider)
              setFormMode({ kind: "edit", provider: discoverProvider });
            setDiscoverProvider(null);
          }}
          onImported={actions.handleImported}
        />
        <AddModelDialog
          provider={addModelProvider}
          onClose={() => setAddModelProvider(null)}
          onImported={actions.handleImported}
        />
        <ModelEditDialog
          model={editModel}
          onClose={() => setEditModel(null)}
          onSaved={actions.handleModelSaved}
        />
        <DeleteDialog
          target={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          onDeleted={actions.handleDeleted}
          onDisabled={actions.handleDisabled}
        />
        <DefaultModelDialog
          target={setDefaultTarget}
          onClose={() => setSetDefaultTarget(null)}
          onSaved={() => void handleDefaultSaved()}
        />
      </div>
    </>
  );
}

function ProviderManagement({
  providers,
  selectedId,
  onSelect,
  selectedProvider,
  models,
  modelsLoading,
  modelsError,
  onRetryModels,
  testingDefault,
  testingModelId,
  passedModelTests,
  onTestProvider,
  onTestModel,
  canTestProvider,
  canDiscover,
  onEditConnection,
  onDiscover,
  onAddModel,
  onCopyProviderKey,
  onSetDefault,
  onToggleModelEnabled,
  onEditModel,
  onDeleteModel,
  onDeleteProvider,
  onToggleProviderEnabled,
  onBatchToggleEnabled,
  onBatchDeleteCompleted,
  providerRefCounts,
  modelRefCounts,
}: {
  providers: Provider[];
  selectedId: number | null;
  onSelect: (id: number) => void;
  selectedProvider: Provider | null;
  models: Model[];
  modelsLoading: boolean;
  modelsError: string | null;
  onRetryModels: () => void;
  testingDefault: boolean;
  testingModelId: number | null;
  passedModelTests: ReadonlyMap<number, string>;
  onTestProvider: (provider: Provider) => void;
  onTestModel: (model: Model) => void;
  canTestProvider?: boolean;
  canDiscover?: boolean;
  onEditConnection: () => void;
  onDiscover: () => void;
  onAddModel: () => void;
  onCopyProviderKey: () => void;
  onSetDefault: (model: Model) => void;
  onToggleModelEnabled: (model: Model) => void;
  onEditModel: (model: Model) => void;
  onDeleteModel: (model: Model) => void;
  onDeleteProvider: () => void;
  onToggleProviderEnabled: (provider: Provider) => void;
  onBatchToggleEnabled: (models: Model[], enabled: boolean) => Promise<void>;
  onBatchDeleteCompleted: (result: BatchDeleteResult) => void;
  providerRefCounts: ReferenceCounts | null;
  modelRefCounts: Map<string, ReferenceCounts>;
}) {
  return (
    <div className="flex min-w-0">
      <ProviderNav
        providers={providers}
        selectedId={selectedId}
        onSelect={onSelect}
      />

      <div className="min-w-0 flex-1">
        {selectedProvider ? (
          <ProviderWorkspace
            provider={selectedProvider}
            models={models}
            modelsLoading={modelsLoading}
            modelsError={modelsError}
            onRetryModels={onRetryModels}
            testingDefault={testingDefault}
            testingModelId={testingModelId}
            passedModelTests={passedModelTests}
            onTestProvider={() => onTestProvider(selectedProvider)}
            onTestModel={onTestModel}
            canTestProvider={canTestProvider}
            canDiscover={canDiscover}
            onEditConnection={onEditConnection}
            onDiscover={onDiscover}
            onAddModel={onAddModel}
            onCopyProviderKey={onCopyProviderKey}
            onSetDefault={onSetDefault}
            onToggleModelEnabled={onToggleModelEnabled}
            onEditModel={onEditModel}
            onDeleteModel={onDeleteModel}
            onDeleteProvider={onDeleteProvider}
            onToggleProviderEnabled={() =>
              onToggleProviderEnabled(selectedProvider)
            }
            onBatchToggleEnabled={onBatchToggleEnabled}
            onBatchDeleteCompleted={onBatchDeleteCompleted}
            providerRefCounts={providerRefCounts}
            modelRefCounts={modelRefCounts}
          />
        ) : null}
      </div>
    </div>
  );
}

type ProvidersEmptyStateProps = {
  onOpenAgentBackends?: () => void;
  onCreate: () => void;
};

function ProvidersEmptyState({
  onOpenAgentBackends,
  onCreate,
}: ProvidersEmptyStateProps) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center justify-center gap-3 px-6 py-10 text-center">
      <div
        aria-hidden="true"
        className="relative flex size-12 items-center justify-center rounded-full bg-primary-soft text-primary-text"
      >
        <Sparkles className="size-5" />
        <span className="absolute -bottom-0.5 -right-0.5 inline-flex size-5 items-center justify-center rounded-full border-2 border-card bg-card text-primary-text shadow-xs">
          <Plus className="size-3" />
        </span>
      </div>
      <div className="flex max-w-md flex-col gap-1">
        <p className="text-sm font-semibold">{t("llmProviders.empty.title")}</p>
        <p className="text-2xs leading-relaxed text-muted-foreground">
          {t("llmProviders.empty.description")}
        </p>
      </div>
      <Button
        type="button"
        size="sm"
        className="h-[30px] gap-1.5 px-3 text-xs"
        onClick={onCreate}
      >
        <Plus data-icon="inline-start" aria-hidden="true" />
        {t("llmProviders.empty.addFirst")}
      </Button>
      <div className="flex flex-col items-center gap-1 text-2xs text-muted-foreground">
        <span className="font-mono">{t("llmProviders.empty.chain")}</span>
        <Button
          type="button"
          variant="link"
          className="h-auto px-0 text-2xs"
          onClick={() => onOpenAgentBackends?.()}
        >
          {t("llmProviders.empty.goToBackend")}
          <ArrowRight className="size-3" aria-hidden="true" />
        </Button>
      </div>
    </div>
  );
}
