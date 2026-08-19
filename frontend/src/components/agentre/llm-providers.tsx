import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  AlertCircle,
  ArrowRight,
  CheckCircle2,
  Loader2,
  Plus,
  Search,
  Sparkles,
} from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

import {
  CreateLLMProvider,
  ListLLMModels,
  ListLLMProviders,
  LLMModelRefCounts,
  LLMProviderRefCounts,
  SetLLMModelEnabled,
  SetLLMProviderEnabled,
  TestLLMProvider,
  UpdateLLMProvider,
} from "../../../wailsjs/go/app/App";
import { llm_provider_svc } from "../../../wailsjs/go/models";
import { LlmProviderLogo } from "./ai-brand-logo";
import { cn } from "@/lib/utils";
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
import { ProviderWorkspace } from "./llm-provider-models/provider-workspace";
import {
  type Model,
  type Provider,
  type ReferenceCounts,
  endpointFor,
  errMessage,
  isProviderType,
  providerTypeOrder,
} from "./llm-provider-models/index";

type FlashState =
  | { kind: "ok"; text: string }
  | { kind: "err"; text: string }
  | null;

export function LlmProvidersPanel({
  onOpenAgentBackends,
  renderHeader,
}: {
  onOpenAgentBackends?: () => void;
  // 页头由宿主渲染，面板把自己的页级操作（新增供应商）交进去：按钮要落在 H1
  // 行，而它开的创建弹窗仍归面板持有。
  renderHeader?: (actions: React.ReactNode) => React.ReactNode;
} = {}) {
  const { t } = useTranslation();
  const [providers, setProviders] = React.useState<Provider[]>([]);
  const [providersLoading, setProvidersLoading] = React.useState(true);
  const [selectedId, setSelectedId] = React.useState<number | null>(null);
  const [models, setModels] = React.useState<Model[]>([]);
  const [modelsLoading, setModelsLoading] = React.useState(false);
  const [modelsError, setModelsError] = React.useState<string | null>(null);
  const [flash, setFlash] = React.useState<FlashState>(null);
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
  const [testingDefault, setTestingDefault] = React.useState(false);
  const [testingModelId, setTestingModelId] = React.useState<number | null>(
    null,
  );
  const [providerRefCounts, setProviderRefCounts] =
    React.useState<ReferenceCounts | null>(null);
  const [modelRefCounts, setModelRefCounts] = React.useState<
    Map<string, ReferenceCounts>
  >(new Map());
  const [modelRefsLoading, setModelRefsLoading] = React.useState(false);
  // 行内「已通过」是会话内瞬时态：后端不持久化测试结果，刷新即消失。
  const [passedModelTests, setPassedModelTests] = React.useState<
    Map<number, string>
  >(new Map());

  const selectedProvider = providers.find((p) => p.id === selectedId) ?? null;

  const refreshProviders = React.useCallback(async () => {
    try {
      const items = (await ListLLMProviders()).items ?? [];
      setProviders(items);
      setSelectedId((prev) =>
        prev !== null && items.some((p) => p.id === prev)
          ? prev
          : items.length > 0
            ? items[0].id
            : null,
      );
    } catch (err) {
      setFlash({
        kind: "err",
        text: t("llmProviders.flash.loadFailed", {
          message: errMessage(err),
        }),
      });
    }
  }, [t]);

  const refreshModels = React.useCallback(async () => {
    if (selectedId === null) return;
    try {
      const resp = await ListLLMModels(
        new llm_provider_svc.ListModelsRequest({ id: selectedId }),
      );
      setModels(resp.items ?? []);
      setModelsError(null);
    } catch (err) {
      setModelsError(errMessage(err));
    }
  }, [selectedId]);

  React.useEffect(() => {
    let mounted = true;
    void (async () => {
      try {
        const items = (await ListLLMProviders()).items ?? [];
        if (!mounted) return;
        setProviders(items);
        setSelectedId(items.length > 0 ? items[0].id : null);
      } catch (err) {
        if (mounted) {
          setFlash({
            kind: "err",
            text: t("llmProviders.flash.loadFailed", {
              message: errMessage(err),
            }),
          });
        }
      } finally {
        if (mounted) setProvidersLoading(false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, [t]);

  React.useEffect(() => {
    if (selectedId === null) {
      setModels([]);
      setModelsLoading(false);
      setModelsError(null);
      return;
    }
    let cancelled = false;
    setModelsLoading(true);
    setModelsError(null);
    void (async () => {
      try {
        const resp = await ListLLMModels(
          new llm_provider_svc.ListModelsRequest({ id: selectedId }),
        );
        if (!cancelled) setModels(resp.items ?? []);
      } catch (err) {
        if (!cancelled) setModelsError(errMessage(err));
      } finally {
        if (!cancelled) setModelsLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [selectedId]);

  // 元信息行的「被引用」计数：切换供应商时按 providerKey 拉取，失败降级为 0。
  React.useEffect(() => {
    if (!selectedProvider) {
      setProviderRefCounts(null);
      return;
    }
    const providerKey = selectedProvider.providerKey;
    let cancelled = false;
    setProviderRefCounts(null);
    void (async () => {
      try {
        const resp = await LLMProviderRefCounts(
          new llm_provider_svc.ProviderRefCountsRequest({ providerKey }),
        );
        if (!cancelled) {
          setProviderRefCounts(
            resp.counts ?? { backends: 0, sessions: 0, routes: 0 },
          );
        }
      } catch {
        if (!cancelled) {
          setProviderRefCounts({ backends: 0, sessions: 0, routes: 0 });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [selectedProvider]);

  // 模型表「引用」列：按 modelKey 逐条拉取引用计数。新目录先清掉旧计数；单条
  // 失败保持 unknown，绝不能把「未确认」降级成 0 后开放批量删除。
  React.useEffect(() => {
    const keys = models.map((m) => m.modelKey);
    setModelRefCounts(new Map());
    if (keys.length === 0) {
      setModelRefsLoading(false);
      return;
    }
    let cancelled = false;
    setModelRefsLoading(true);
    void (async () => {
      const entries = await Promise.all(
        keys.map(async (key): Promise<[string, ReferenceCounts] | null> => {
          try {
            const resp = await LLMModelRefCounts(
              new llm_provider_svc.ModelRefCountsRequest({ modelKey: key }),
            );
            return [
              key,
              resp.counts ?? { backends: 0, sessions: 0, routes: 0 },
            ];
          } catch {
            return null;
          }
        }),
      );
      if (!cancelled) {
        setModelRefCounts(
          new Map(
            entries.filter(
              (entry): entry is [string, ReferenceCounts] => entry !== null,
            ),
          ),
        );
        setModelRefsLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [models]);

  const handleTestProvider = React.useCallback(
    async (provider: Provider) => {
      setTestingDefault(true);
      const startedAt = Date.now();
      try {
        const resp = await TestLLMProvider(
          new llm_provider_svc.TestConnectionRequest({
            id: provider.id,
            useDraft: false,
            type: provider.type,
            apiKey: "",
            baseUrl: "",
            // 空 modelKey → 测试 Provider 当前默认模型
            modelKey: "",
            modelId: "",
          }),
        );
        const duration = `${Date.now() - startedAt}ms`;
        setFlash(
          resp.ok
            ? {
                kind: "ok",
                text: t("llmProviders.test.providerSuccess", {
                  name: provider.name,
                  duration,
                }),
              }
            : {
                kind: "err",
                text: t("llmProviders.test.providerFailed", {
                  name: provider.name,
                  message: resp.message,
                  duration,
                }),
              },
        );
      } catch (err) {
        setFlash({
          kind: "err",
          text: t("llmProviders.flash.testFailed", {
            message: errMessage(err),
          }),
        });
      } finally {
        setTestingDefault(false);
      }
    },
    [t],
  );

  const handleTestModel = React.useCallback(
    async (model: Model) => {
      setTestingModelId(model.id);
      const startedAt = Date.now();
      try {
        const resp = await TestLLMProvider(
          new llm_provider_svc.TestConnectionRequest({
            id: model.providerId,
            useDraft: false,
            type: "",
            apiKey: "",
            baseUrl: "",
            // 具体 modelKey → 测试该子模型
            modelKey: model.modelKey,
            modelId: "",
          }),
        );
        const duration = `${Date.now() - startedAt}ms`;
        setPassedModelTests((prev) => {
          const next = new Map(prev);
          if (resp.ok) next.set(model.id, duration);
          else next.delete(model.id);
          return next;
        });
        setFlash(
          resp.ok
            ? {
                kind: "ok",
                text: t("llmProviders.test.rowSuccess", {
                  model: model.modelId,
                  duration,
                }),
              }
            : {
                kind: "err",
                text: t("llmProviders.test.rowFailed", {
                  model: model.modelId,
                  message: resp.message,
                  duration,
                }),
              },
        );
      } catch (err) {
        setPassedModelTests((prev) => {
          if (!prev.has(model.id)) return prev;
          const next = new Map(prev);
          next.delete(model.id);
          return next;
        });
        setFlash({
          kind: "err",
          text: t("llmProviders.flash.testFailed", {
            message: errMessage(err),
          }),
        });
      } finally {
        setTestingModelId(null);
      }
    },
    [t],
  );

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

  const handleToggleModelEnabled = React.useCallback(
    async (model: Model) => {
      try {
        await SetLLMModelEnabled(
          new llm_provider_svc.SetModelEnabledRequest({
            id: model.id,
            enabled: !model.enabled,
          }),
        );
        setFlash({
          kind: "ok",
          text: model.enabled
            ? t("llmProviders.flash.modelDisabled", { model: model.modelId })
            : t("llmProviders.flash.modelEnabled", { model: model.modelId }),
        });
        await refreshModels();
      } catch (err) {
        setFlash({ kind: "err", text: errMessage(err) });
      }
    },
    [refreshModels, t],
  );

  const handleToggleProviderEnabled = React.useCallback(
    async (provider: Provider) => {
      try {
        await SetLLMProviderEnabled(
          new llm_provider_svc.SetProviderEnabledRequest({
            id: provider.id,
            enabled: !provider.enabled,
          }),
        );
        setFlash({
          kind: "ok",
          text: provider.enabled
            ? t("llmProviders.flash.providerDisabled", { name: provider.name })
            : t("llmProviders.flash.providerEnabled", { name: provider.name }),
        });
        await refreshProviders();
      } catch (err) {
        setFlash({ kind: "err", text: errMessage(err) });
      }
    },
    [refreshProviders, t],
  );

  const handleBatchToggleEnabled = React.useCallback(
    async (models: Model[], enabled: boolean) => {
      if (!selectedProvider) return;
      const defaultKey = selectedProvider.defaultModelKey;
      // 停用时跳过默认模型（默认模型不可停用）；启用只作用于已停用的模型。
      const targets = enabled
        ? models.filter((m) => !m.enabled)
        : models.filter((m) => m.enabled && m.modelKey !== defaultKey);
      const skippedDefault =
        !enabled && models.some((m) => m.enabled && m.modelKey === defaultKey);

      let done = 0;
      let error: string | null = null;
      for (const model of targets) {
        try {
          await SetLLMModelEnabled(
            new llm_provider_svc.SetModelEnabledRequest({
              id: model.id,
              enabled,
            }),
          );
          done += 1;
        } catch (err) {
          error = errMessage(err);
          break;
        }
      }
      const unprocessed = targets.length - done;

      if (error) {
        setFlash({
          kind: "err",
          text: enabled
            ? t("llmProviders.flash.batchEnabledPartial", {
                done,
                unprocessed,
                message: error,
              })
            : t("llmProviders.flash.batchDisabledPartial", {
                done,
                unprocessed,
                message: error,
              }),
        });
      } else if (enabled) {
        setFlash({
          kind: "ok",
          text: t("llmProviders.flash.batchEnabled", { count: done }),
        });
      } else if (skippedDefault) {
        setFlash({
          kind: "ok",
          text: t("llmProviders.flash.batchDisabledSkippedDefault", {
            count: done,
          }),
        });
      } else {
        setFlash({
          kind: "ok",
          text: t("llmProviders.flash.batchDisabled", { count: done }),
        });
      }
      await refreshModels();
    },
    [refreshModels, selectedProvider, t],
  );

  const handleBatchDeleteCompleted = React.useCallback(
    (result: BatchDeleteResult) => {
      if (result.error) {
        setFlash({
          kind: "err",
          text: t("llmProviders.flash.batchDeletePartial", {
            deleted: result.deleted,
            unprocessed: result.unprocessed,
            message: result.error,
          }),
        });
      } else {
        setFlash({
          kind: "ok",
          text: t("llmProviders.flash.batchDeleted", {
            count: result.deleted,
          }),
        });
      }
      void refreshModels();
    },
    [refreshModels, t],
  );

  const handleCopyProviderKey = React.useCallback(
    async (provider: Provider) => {
      try {
        await navigator.clipboard.writeText(provider.providerKey);
        setFlash({
          kind: "ok",
          text: t("llmProviders.fields.copyProviderKeyDone"),
        });
      } catch {
        setFlash({
          kind: "err",
          text: t("llmProviders.fields.copyProviderKeyFailed"),
        });
      }
    },
    [t],
  );

  const handleFormSubmit = React.useCallback(
    async (mode: ProviderFormMode, values: ProviderFormValues) => {
      if (mode.kind === "create") {
        await CreateLLMProvider(
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
        await UpdateLLMProvider(
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
    [refreshProviders, t],
  );

  const handleImported = React.useCallback(
    (resp: llm_provider_svc.ImportModelsResponse) => {
      setFlash({
        kind: "ok",
        text: t("llmProviders.flash.imported", {
          imported: resp.imported,
          updated: resp.updated,
        }),
      });
      void refreshModels();
    },
    [refreshModels, t],
  );

  const handleModelSaved = React.useCallback(
    (updated: llm_provider_svc.ModelItem) => {
      setFlash({
        kind: "ok",
        text: t("llmProviders.flash.modelUpdated", {
          model: updated.modelId,
        }),
      });
      void refreshModels();
    },
    [refreshModels, t],
  );

  const handleDeleted = React.useCallback(
    (target: DeleteTarget) => {
      if (target.kind === "provider") {
        setFlash({
          kind: "ok",
          text: t("llmProviders.flash.deleted", {
            name: target.provider.name,
          }),
        });
        void refreshProviders();
      } else {
        setFlash({
          kind: "ok",
          text: t("llmProviders.flash.modelDeleted", {
            model: target.model.modelId,
          }),
        });
        void refreshModels();
      }
    },
    [refreshModels, refreshProviders, t],
  );

  // 被引用挡住删除时的第三条出路：弹窗自己完成停用，这里只负责说出结果并刷新，
  // 否则后端已经停用而工作区还停在旧的启用态。
  const handleDisabled = React.useCallback(
    (target: DeleteTarget) => {
      if (target.kind === "provider") {
        setFlash({
          kind: "ok",
          text: t("llmProviders.flash.providerDisabled", {
            name: target.provider.name,
          }),
        });
        void refreshProviders();
      } else {
        setFlash({
          kind: "ok",
          text: t("llmProviders.flash.modelDisabled", {
            model: target.model.modelId,
          }),
        });
        void refreshModels();
      }
    },
    [refreshModels, refreshProviders, t],
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
              testingDefault={testingDefault}
              testingModelId={testingModelId}
              passedModelTests={passedModelTests}
              providerRefCounts={providerRefCounts}
              modelRefCounts={modelRefCounts}
              modelRefsLoading={modelRefsLoading}
              onTestProvider={handleTestProvider}
              onTestModel={handleTestModel}
              onEditConnection={() => {
                if (selectedProvider)
                  setFormMode({ kind: "edit", provider: selectedProvider });
              }}
              onDiscover={() => setDiscoverProvider(selectedProvider)}
              onAddModel={() => setAddModelProvider(selectedProvider)}
              onCopyProviderKey={() => {
                if (selectedProvider)
                  void handleCopyProviderKey(selectedProvider);
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
              onToggleModelEnabled={handleToggleModelEnabled}
              onEditModel={setEditModel}
              onDeleteModel={(model) =>
                setDeleteTarget({ kind: "model", model })
              }
              onToggleProviderEnabled={handleToggleProviderEnabled}
              onBatchToggleEnabled={handleBatchToggleEnabled}
              onBatchDeleteCompleted={handleBatchDeleteCompleted}
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
          onImported={handleImported}
        />
        <AddModelDialog
          provider={addModelProvider}
          onClose={() => setAddModelProvider(null)}
          onImported={handleImported}
        />
        <ModelEditDialog
          model={editModel}
          onClose={() => setEditModel(null)}
          onSaved={handleModelSaved}
        />
        <DeleteDialog
          target={deleteTarget}
          onClose={() => setDeleteTarget(null)}
          onDeleted={handleDeleted}
          onDisabled={handleDisabled}
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
  modelRefsLoading,
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
  modelRefsLoading: boolean;
}) {
  const { t } = useTranslation();
  const [navSearch, setNavSearch] = React.useState("");

  const trimmed = navSearch.trim().toLowerCase();
  const filtered = trimmed
    ? providers.filter(
        (p) =>
          p.name.toLowerCase().includes(trimmed) ||
          endpointFor(p).toLowerCase().includes(trimmed) ||
          p.providerKey.toLowerCase().includes(trimmed),
      )
    : providers;

  const groups = React.useMemo(() => {
    const map = new Map<string, Provider[]>();
    for (const p of filtered) {
      const list = map.get(p.type) ?? [];
      list.push(p);
      map.set(p.type, list);
    }
    const extra = Array.from(map.keys()).filter(
      (k) =>
        !providerTypeOrder.includes(k as (typeof providerTypeOrder)[number]),
    );
    const order = [...providerTypeOrder, ...extra];
    return order
      .map((type) => ({ type, items: map.get(type) ?? [] }))
      .filter((g) => g.items.length > 0);
  }, [filtered]);

  return (
    <div className="flex min-w-0">
      <aside
        role="complementary"
        aria-label={t("llmProviders.nav.ariaLabel")}
        className="flex w-60 shrink-0 flex-col border-r border-border"
      >
        <div className="border-b border-border p-2">
          <div className="relative">
            <Search
              className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <input
              type="search"
              value={navSearch}
              onChange={(e) => setNavSearch(e.currentTarget.value)}
              placeholder={t("llmProviders.nav.searchPlaceholder")}
              aria-label={t("llmProviders.nav.searchAria")}
              className="h-8 w-full rounded-md border border-input bg-transparent pl-8 pr-3 text-xs outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
            />
          </div>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-2">
          {groups.map((group) => (
            <div key={group.type} className="mb-2">
              <div className="px-1.5 pb-1 font-mono text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                {isProviderType(group.type)
                  ? t(`llmProviders.providerType.${group.type}.label`)
                  : group.type}
                <span className="ml-1 text-muted-foreground">
                  {group.items.length}
                </span>
              </div>
              <div className="flex flex-col gap-0.5">
                {group.items.map((p) => {
                  const active = p.id === selectedId;
                  return (
                    <button
                      key={p.id}
                      type="button"
                      aria-current={active}
                      onClick={() => onSelect(p.id)}
                      className={cn(
                        "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                        active
                          ? "bg-primary-soft text-primary-text"
                          : "hover:bg-accent",
                      )}
                    >
                      <LlmProviderLogo
                        providerType={p.type}
                        providerName={p.name}
                        baseUrl={p.baseUrl}
                        className="size-5 shrink-0 rounded-sm"
                      />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-xs font-medium">
                          {p.name}
                        </span>
                        {/* endpoint 独占一行：截断只吃 endpoint 自己 */}
                        <span className="block truncate font-mono text-2xs text-muted-foreground">
                          {endpointFor(p)}
                        </span>
                      </span>
                      {/* 模型数是独立元素，不参与 endpoint 的截断；停用时让位给下面的徽标 */}
                      {p.enabled ? (
                        <span className="shrink-0 font-mono text-2xs text-muted-foreground">
                          {t("llmProviders.nav.modelCount", {
                            count: p.modelCount,
                          })}
                        </span>
                      ) : (
                        <span className="shrink-0 font-mono text-2xs text-status-waiting">
                          {t("llmProviders.nav.disabled")}
                        </span>
                      )}
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
          {filtered.length === 0 ? (
            <p className="px-1.5 py-2 text-2xs text-muted-foreground">
              {t("llmProviders.nav.noMatch")}
            </p>
          ) : null}
        </div>
      </aside>

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
            modelRefsLoading={modelRefsLoading}
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
