// 供应商面板的写路径与结果播报：连通性测试、启停切换、批量启停、复制 providerKey，
// 以及各个弹窗做完事之后回来播报的那几条。
//
// 它们的共同形状是「调后端 → 往 flash 说一句 → 刷新对应的读路径」，所以收在一处；
// 会改弹窗开合状态的几个（表单提交、设为默认）留在面板本体，那是弹窗自己的事。
import * as React from "react";
import { useUiTranslation as useTranslation } from "../../i18n";

import { llm_provider_svc } from "../port-bridge";
import type { EngineSettingsBridge } from "../port-bridge";

import type { BatchDeleteResult } from "./batch-delete-dialog";
import type { DeleteTarget } from "./delete-dialog";
import { type Model, type Provider, errMessage } from ".";
import type { PanelFlash } from "./use-provider-catalog";

export type ProviderActionsBridge = Pick<
  EngineSettingsBridge,
  "SetLLMModelEnabled" | "SetLLMProviderEnabled" | "TestLLMProvider"
>;

export function useProviderActions(args: {
  bridge: ProviderActionsBridge;
  selectedProvider: Provider | null;
  setFlash: (flash: PanelFlash) => void;
  refreshProviders: () => Promise<void>;
  refreshModels: () => Promise<void>;
}) {
  const { selectedProvider, setFlash, refreshProviders, refreshModels } = args;
  const { SetLLMModelEnabled, SetLLMProviderEnabled, TestLLMProvider } =
    args.bridge;
  const { t } = useTranslation();
  const [testingDefault, setTestingDefault] = React.useState(false);
  const [testingModelId, setTestingModelId] = React.useState<number | null>(
    null,
  );
  const [passedModelTests, setPassedModelTests] = React.useState<
    Map<number, string>
  >(new Map());

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
    [TestLLMProvider, setFlash, t],
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
    [TestLLMProvider, setFlash, t],
  );

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
    [SetLLMModelEnabled, refreshModels, setFlash, t],
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
    [SetLLMProviderEnabled, refreshProviders, setFlash, t],
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
    [SetLLMModelEnabled, refreshModels, selectedProvider, setFlash, t],
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
    [refreshModels, setFlash, t],
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
    [setFlash, t],
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
    [refreshModels, setFlash, t],
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
    [refreshModels, setFlash, t],
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
    [refreshModels, refreshProviders, setFlash, t],
  );

  // 弹窗里「改为停用」的落点：停用由弹窗自己完成，这里只负责说出结果并刷新，
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
    [refreshModels, refreshProviders, setFlash, t],
  );

  // 空态自带「添加第一个供应商」CTA，此时页头不再出第二个——全页始终只有一个入口。
  return {
    testingDefault,
    testingModelId,
    passedModelTests,
    handleTestProvider,
    handleTestModel,
    handleToggleModelEnabled,
    handleToggleProviderEnabled,
    handleBatchToggleEnabled,
    handleBatchDeleteCompleted,
    handleCopyProviderKey,
    handleImported,
    handleModelSaved,
    handleDeleted,
    handleDisabled,
  };
}
