/**
 * 「发现模型」对话框外壳：头部 + 搜索/计数条 + 正文三态 + 底部导入。
 *
 * 只做装配：拉取与勾选在 `use-discover-models.ts`，失败那一块在
 * `discover-error-panel.tsx`，清单在 `discover-model-list.tsx`，错误归类与
 * 模型族名在 `discover-failure.ts`。
 */
import { CheckCircle2, Download, Inbox, Loader2, Search } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Button } from "../../ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../ui/dialog";
import { Input } from "../../ui/input";

import { useEngineSettingsBridge } from "../port-bridge";
import { llm_provider_svc } from "../port-bridge";
import { discoverFailure } from "./discover-failure";
import { DiscoverErrorPanel } from "./discover-error-panel";
import { DiscoverModelList } from "./discover-model-list";
import { useDiscoverModels } from "./use-discover-models";
import { type Model, type Provider } from "./index";

export function DiscoverDialog({
  provider,
  existingModels,
  onClose,
  onEditConnection,
  onImported,
}: {
  provider: Provider | null;
  existingModels: Model[];
  onClose: () => void;
  onEditConnection: () => void;
  onImported: (resp: llm_provider_svc.ImportModelsResponse) => void;
}) {
  const { ImportLLMModels, PreviewLLMModels } = useEngineSettingsBridge();
  const { t } = useTranslation();
  const state = useDiscoverModels({
    provider,
    existingModels,
    onClose,
    onImported,
    PreviewLLMModels,
    ImportLLMModels,
    t,
  });
  const {
    fetchError,
    importError,
    importing,
    items,
    loading,
    selectedCount,
    existingCount,
    newCount,
  } = state;

  const providerName = provider ? provider.name : "";
  const failure = fetchError ? discoverFailure(fetchError) : null;

  return (
    <Dialog
      open={state.open}
      onOpenChange={(next) => {
        if (!next && !importing) onClose();
      }}
    >
      <DialogContent className="max-w-[640px]">
        <DialogHeader>
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0 flex-1">
              <DialogTitle>
                {t("llmProviders.discover.title", { name: providerName })}
              </DialogTitle>
              <DialogDescription>
                {t("llmProviders.discover.description")}
              </DialogDescription>
            </div>
            {/* 读取成功的状态 chip：读到了什么在头部一眼可见 */}
            {!loading && !fetchError && items.length > 0 ? (
              <span className="shrink-0 rounded-sm bg-status-running/10 px-1.5 py-0.5 text-2xs font-medium text-status-running">
                {t("llmProviders.discover.readOk")}
              </span>
            ) : null}
          </div>
        </DialogHeader>

        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-5 py-3">
          <div className="relative min-w-0 flex-1">
            <Search
              className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              value={state.search}
              onChange={(e) => state.setSearch(e.currentTarget.value)}
              placeholder={t("llmProviders.discover.searchPlaceholder")}
              className="h-8 pl-8 text-xs"
              aria-label={t("llmProviders.discover.searchAria")}
            />
          </div>
          <span className="shrink-0 font-mono text-2xs text-muted-foreground">
            {t("llmProviders.discover.count", {
              remote: items.length,
              new: newCount,
              existing: existingCount,
            })}
          </span>
        </div>

        <DialogBody className="space-y-2">
          {fetchError && failure ? (
            <DiscoverErrorPanel
              rawError={fetchError}
              failure={failure}
              loading={loading}
              showRaw={state.showRaw}
              onToggleRaw={() => state.setShowRaw((v) => !v)}
              onEditConnection={onEditConnection}
              onRetry={() => void state.fetchPreview()}
            />
          ) : loading ? (
            <div className="flex items-center gap-2 py-6 text-2xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              {t("llmProviders.discover.loading")}
            </div>
          ) : items.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-6 text-center">
              <Inbox
                className="size-5 text-decorative-foreground"
                aria-hidden="true"
              />
              <p className="text-xs font-semibold text-foreground">
                {t("llmProviders.discover.empty")}
              </p>
            </div>
          ) : (
            <DiscoverModelList
              providerType={provider?.type ?? ""}
              groups={state.groups}
              existingIds={state.existingIds}
              selected={state.selected}
              newCount={newCount}
              allNewChecked={state.allNewChecked}
              onToggle={state.toggle}
              onToggleAllNew={state.toggleAllNew}
            />
          )}
        </DialogBody>

        {importError ? (
          <p role="alert" className="px-5 pb-1 text-2xs text-status-error">
            {importError}
          </p>
        ) : null}

        <DialogFooter className="justify-between gap-2">
          <span className="text-2xs leading-relaxed text-muted-foreground">
            {t("llmProviders.discover.importSummary", { count: selectedCount })}
            {existingCount > 0
              ? ` · ${t("llmProviders.discover.importSummaryExisting")}`
              : ""}
          </span>
          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-8 text-xs"
              onClick={onClose}
              disabled={importing}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="button"
              size="sm"
              className="h-8 gap-1.5 text-xs"
              onClick={() => void state.importModels()}
              disabled={importing || selectedCount === 0}
            >
              {importing ? (
                <Loader2
                  className="size-3.5 animate-spin"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
              ) : (
                <Download
                  className="size-3.5"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
              )}
              {t("llmProviders.discover.importButton", {
                count: selectedCount,
              })}
            </Button>
          </div>
        </DialogFooter>

        {importing ? (
          <p
            role="status"
            className="flex items-center gap-1.5 px-5 pb-3 text-2xs text-muted-foreground"
          >
            <CheckCircle2
              className="size-3 text-status-running"
              aria-hidden="true"
            />
            {t("llmProviders.discover.importing")}
          </p>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
