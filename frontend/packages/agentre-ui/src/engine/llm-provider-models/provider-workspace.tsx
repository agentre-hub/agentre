import * as React from "react";
import { useUiTranslation as useTranslation } from "../../i18n";
import {
  Copy,
  Loader2,
  MoreHorizontal,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  SendHorizontal,
  Sparkles,
  Trash2,
} from "lucide-react";

import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../../ui/dropdown-menu";
import { RadioGroup, RadioGroupItem } from "../ui/radio-group";
import { Switch } from "../ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../ui/table";

import { LlmModelLogo, LlmProviderLogo } from "../ai-brand-logo";
import { cn } from "../../lib/utils";
import {
  type BatchDeleteResult,
  BatchDeleteDialog,
} from "./batch-delete-dialog";
import {
  type Model,
  type Provider,
  type ReferenceCounts,
  endpointFor,
  formatTokens,
  modelDeleteability,
  providerTypeMeta,
  totalReferences,
} from "./index";

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

  const meta =
    provider.type in providerTypeMeta
      ? providerTypeMeta[provider.type as keyof typeof providerTypeMeta]
      : undefined;
  const endpoint = endpointFor(provider);

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

  // Provider 启用需要属于它的启用默认模型
  const defaultModel = models.find(
    (m) => m.modelKey === provider.defaultModelKey,
  );
  const hasEnabledDefault =
    provider.enabled ||
    Boolean(provider.defaultModelKey && defaultModel && defaultModel.enabled);
  const enableDisabledReason = hasEnabledDefault
    ? undefined
    : t("llmProviders.workspace.cannotEnableNoDefault");

  // 元信息行：当前默认模型（无则占位）与供应商被引用计数。
  const defaultModelName = defaultModel?.modelId ?? "—";
  const refCounts = providerRefCounts ?? {
    backends: 0,
    sessions: 0,
    routes: 0,
  };
  const refParts: string[] = [];
  if (refCounts.backends > 0) {
    refParts.push(
      t("llmProviders.workspace.refBackends", { count: refCounts.backends }),
    );
  }
  if (refCounts.sessions > 0) {
    refParts.push(
      t("llmProviders.workspace.refSessions", { count: refCounts.sessions }),
    );
  }
  if (refCounts.routes > 0) {
    refParts.push(
      t("llmProviders.workspace.refRoutes", { count: refCounts.routes }),
    );
  }
  const refsText = refParts.join(" · ");

  return (
    <div
      role="region"
      aria-label={t("llmProviders.workspace.ariaLabel", {
        name: provider.name,
      })}
      className="@container flex min-w-0 flex-col overflow-hidden"
    >
      {/* Provider header：身份行 + 元信息行 */}
      <div className="border-b border-border px-3 py-3 sm:px-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2.5">
            <LlmProviderLogo
              providerType={provider.type}
              providerName={provider.name}
              baseUrl={provider.baseUrl}
              className="size-8 shrink-0 rounded-md"
            />
            <div className="flex min-w-0 items-center gap-1.5">
              <span className="truncate text-sm font-semibold">
                {provider.name}
              </span>
              <span className="shrink-0 rounded-sm bg-secondary px-1.5 py-0.5 font-mono text-2xs text-muted-foreground">
                {meta
                  ? t(`llmProviders.providerType.${provider.type}.label`)
                  : provider.type}
              </span>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-1.5">
            <label
              className={cn(
                "flex items-center gap-1.5 text-2xs text-muted-foreground",
                !hasEnabledDefault && "cursor-not-allowed",
              )}
              title={enableDisabledReason}
            >
              <Switch
                checked={provider.enabled}
                disabled={!hasEnabledDefault}
                onCheckedChange={() => onToggleProviderEnabled()}
                size="sm"
                title={enableDisabledReason}
                aria-label={t("llmProviders.workspace.enableNamed", {
                  name: provider.name,
                })}
              />
              {provider.enabled
                ? t("llmProviders.workspace.enabledShort")
                : t("llmProviders.workspace.disabledShort")}
            </label>
            {/* 状态与操作分档：enable 开关是一档，测试/发现/更多是另一档 */}
            <span
              className="h-[18px] w-px shrink-0 bg-border"
              aria-hidden="true"
            />
            {canTestProvider ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-[30px] gap-1.5 px-3 text-xs"
                onClick={onTestProvider}
                disabled={testingDefault}
                aria-label={t("llmProviders.workspace.testNamed", {
                  name: provider.name,
                })}
                title={t("llmProviders.workspace.testTitle")}
              >
                {testingDefault ? (
                  <Loader2
                    className="size-3.5 animate-spin"
                    data-icon="inline-start"
                    aria-hidden="true"
                  />
                ) : (
                  <SendHorizontal
                    className="size-3.5"
                    data-icon="inline-start"
                    aria-hidden="true"
                  />
                )}
                {t("llmProviders.workspace.testConnection")}
              </Button>
            ) : null}
            {canDiscover ? (
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-[30px] gap-1.5 px-3 text-xs"
                onClick={onDiscover}
                aria-label={t("llmProviders.workspace.discoverNamed", {
                  name: provider.name,
                })}
              >
                <RefreshCw
                  className="size-3.5"
                  data-icon="inline-start"
                  aria-hidden="true"
                />
                {t("llmProviders.workspace.discover")}
              </Button>
            ) : null}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  type="button"
                  variant="outline"
                  size="icon-sm"
                  aria-label={t("llmProviders.workspace.more")}
                  title={t("llmProviders.workspace.more")}
                >
                  <MoreHorizontal data-icon="only" aria-hidden="true" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onSelect={onEditConnection}>
                  <Pencil className="size-3.5" aria-hidden="true" />
                  {t("llmProviders.workspace.editConnectionNamed", {
                    name: provider.name,
                  })}
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={onCopyProviderKey}>
                  <Copy className="size-3.5" aria-hidden="true" />
                  {t("llmProviders.fields.copyProviderKey")}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  variant="destructive"
                  onSelect={onDeleteProvider}
                >
                  <Trash2 className="size-3.5" aria-hidden="true" />
                  {t("llmProviders.workspace.deleteNamed", {
                    name: provider.name,
                  })}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>

        {/* 元信息行：endpoint / 掩码 key / 默认模型 / 被引用 */}
        <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono text-2xs text-muted-foreground">
          <span className="truncate">{endpoint}</span>
          <span className="truncate">
            {provider.hasApiKey
              ? provider.maskedApiKey
              : t("llmProviders.row.noApiKey")}
          </span>
          <span className="flex items-center gap-1">
            <span className="text-muted-foreground">
              {t("llmProviders.workspace.metaDefaultModel")}
            </span>
            <span>{defaultModelName}</span>
          </span>
          <span className="flex items-center gap-1">
            <span className="text-muted-foreground">
              {t("llmProviders.workspace.metaReferenced")}
            </span>
            <span>{refsText || "—"}</span>
          </span>
        </div>
      </div>

      {/* Model tools：选择态原地替换为操作条 */}
      {selected.size > 0 ? (
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2 sm:px-4">
          <span className="shrink-0 font-mono text-2xs text-muted-foreground">
            {t("llmProviders.modelsTable.batch.selectedCount", {
              selected: selected.size,
              total: visible.length,
            })}
          </span>
          <div className="flex flex-wrap items-center gap-1.5">
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-[30px] gap-1.5 px-3 text-xs"
              onClick={() => setSelected(new Set(visible.map((m) => m.id)))}
            >
              {t("llmProviders.modelsTable.batch.selectAll")}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-[30px] gap-1.5 px-3 text-xs"
              onClick={() => setSelected(new Set())}
            >
              {t("llmProviders.modelsTable.batch.clearSelection")}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-[30px] gap-1.5 px-3 text-xs"
              onClick={() => void handleBatchToggle(true)}
            >
              {t("llmProviders.modelsTable.batch.enable")}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-[30px] gap-1.5 px-3 text-xs"
              onClick={() => void handleBatchToggle(false)}
            >
              {t("llmProviders.modelsTable.batch.disable")}
            </Button>
            <Button
              type="button"
              variant="destructive"
              size="sm"
              className="h-[30px] gap-1.5 px-3 text-xs"
              onClick={handleBatchDelete}
            >
              {t("llmProviders.modelsTable.batch.delete")}
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2 sm:px-4">
          <div className="relative min-w-0 flex-1">
            <Search
              className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <input
              type="search"
              value={search}
              onChange={(e) => setSearch(e.currentTarget.value)}
              placeholder={t("llmProviders.workspace.searchPlaceholder")}
              aria-label={t("llmProviders.workspace.searchAria")}
              className="h-8 w-full rounded-md border border-input bg-transparent pl-8 pr-3 text-xs outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50"
            />
          </div>
          {/* 手动添加常驻工具栏：有模型之后也必须够得着 */}
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-8 shrink-0 gap-1.5 px-3 text-xs"
            onClick={onAddModel}
          >
            <Plus
              className="size-3.5"
              data-icon="inline-start"
              aria-hidden="true"
            />
            {t("llmProviders.workspace.addModelManual")}
          </Button>
          <span className="shrink-0 font-mono text-2xs text-muted-foreground">
            {t("llmProviders.workspace.modelCount", {
              shown: visible.length,
              total: models.length,
            })}
          </span>
        </div>
      )}

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
          <Table
            aria-label={t("llmProviders.modelsTable.ariaLabel")}
            className="min-w-[560px] @min-[560px]:min-w-0"
          >
            <TableHeader>
              <TableRow className="bg-secondary hover:bg-secondary">
                <TableHead className="w-[40px] px-4">
                  <Checkbox
                    aria-label={t("llmProviders.modelsTable.selectAll")}
                    checked={allVisibleSelected}
                    onCheckedChange={(checked) => {
                      if (checked === true) {
                        setSelected(new Set(visible.map((m) => m.id)));
                      } else {
                        setSelected(new Set());
                      }
                    }}
                  />
                </TableHead>
                <TableHead className="px-4 font-mono text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                  {t("llmProviders.modelsTable.model")}
                </TableHead>
                <TableHead className="w-[88px] @max-[640px]:hidden font-mono text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                  {t("llmProviders.modelsTable.context")}
                </TableHead>
                <TableHead className="w-[88px] @max-[640px]:hidden font-mono text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                  {t("llmProviders.modelsTable.maxOutput")}
                </TableHead>
                <TableHead className="w-[64px] font-mono text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                  {t("llmProviders.modelsTable.references")}
                </TableHead>
                <TableHead className="w-[64px] font-mono text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                  {t("llmProviders.modelsTable.default")}
                </TableHead>
                <TableHead className="w-[56px] font-mono text-2xs font-semibold uppercase tracking-[0.08em] text-muted-foreground">
                  {t("llmProviders.modelsTable.enableColumn")}
                </TableHead>
                <TableHead className="w-[88px]" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {visible.map((model) => {
                const isDefault = model.modelKey === provider.defaultModelKey;
                const canDisable = !isDefault;
                const canSetDefault = model.enabled && !isDefault;
                const refCount = totalReferences(
                  modelRefCounts.get(model.modelKey),
                );
                const passedDuration = passedModelTests.get(model.id);
                const del = modelDeleteability(
                  model,
                  provider.defaultModelKey,
                  modelRefCounts,
                );
                const deleteBlocked = del.kind === "default";
                const deleteBlockedReason = deleteBlocked
                  ? t("llmProviders.modelsTable.deleteBlockedDefault")
                  : undefined;
                return (
                  <TableRow
                    key={model.id}
                    className={cn(
                      "align-top hover:bg-accent/45",
                      // 停用的模型整行压暗，与行内「已停用」徽标一起给出状态
                      !model.enabled && "opacity-55",
                    )}
                  >
                    <TableCell className="px-4 py-2.5">
                      <Checkbox
                        aria-label={t("llmProviders.modelsTable.selectModel", {
                          model: model.modelId,
                        })}
                        checked={selected.has(model.id)}
                        onCheckedChange={(checked) => {
                          setSelected((prev) => {
                            const next = new Set(prev);
                            if (checked === true) next.add(model.id);
                            else next.delete(model.id);
                            return next;
                          });
                        }}
                      />
                    </TableCell>
                    <TableCell className="px-4 py-2.5">
                      <div className="flex min-w-0 items-start gap-2">
                        <LlmModelLogo
                          providerType={provider.type}
                          providerName={provider.name}
                          baseUrl={provider.baseUrl}
                          model={model.modelId}
                          className="mt-0.5 size-4"
                        />
                        <div className="flex min-w-0 flex-col gap-0.5">
                          <span className="flex min-w-0 items-center gap-1.5">
                            <span className="truncate text-xs">
                              {model.name || model.modelId}
                            </span>
                            {isDefault ? (
                              <span className="shrink-0 rounded-sm bg-primary-soft px-1.5 py-0.5 text-2xs font-medium text-primary-text">
                                {t("llmProviders.modelsTable.defaultBadge")}
                              </span>
                            ) : null}
                            {model.enabled ? null : (
                              <span className="shrink-0 rounded-sm bg-status-waiting/10 px-1.5 py-0.5 text-2xs font-medium text-status-waiting">
                                {t("llmProviders.modelsTable.disabled")}
                              </span>
                            )}
                          </span>
                          <span className="flex min-w-0 items-center gap-1.5">
                            <span className="truncate font-mono text-2xs text-muted-foreground">
                              {model.modelId}
                            </span>
                            {passedDuration ? (
                              <span
                                className="shrink-0 rounded-sm bg-status-running/10 px-1.5 py-0.5 text-2xs font-medium text-status-running"
                                title={t(
                                  "llmProviders.modelsTable.testPassedHint",
                                  { duration: passedDuration },
                                )}
                              >
                                {t("llmProviders.modelsTable.testPassed")}
                              </span>
                            ) : null}
                          </span>
                          {selected.has(model.id) ? (
                            <span
                              className={cn(
                                "text-2xs",
                                del.kind === "default"
                                  ? "text-status-error"
                                  : refCount > 0
                                    ? "text-status-waiting"
                                    : "text-status-running",
                              )}
                            >
                              {del.kind === "default"
                                ? t(
                                    "llmProviders.modelsTable.batch.rowDefaultBlocked",
                                  )
                                : refCount > 0
                                  ? t(
                                      "llmProviders.modelsTable.batch.rowReferenced",
                                      { count: refCount },
                                    )
                                  : t(
                                      "llmProviders.modelsTable.batch.rowCanDelete",
                                    )}
                            </span>
                          ) : null}
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="py-2.5 @max-[640px]:hidden font-mono text-2xs text-muted-foreground">
                      {formatTokens(model.contextWindow)}
                    </TableCell>
                    <TableCell className="py-2.5 @max-[640px]:hidden font-mono text-2xs text-muted-foreground">
                      {formatTokens(model.maxOutput)}
                    </TableCell>
                    <TableCell className="py-2.5 font-mono text-2xs text-muted-foreground">
                      {refCount > 0 ? refCount : "—"}
                    </TableCell>
                    <TableCell className="py-2.5">
                      <RadioGroup
                        value={isDefault ? model.modelKey : ""}
                        onValueChange={() => onSetDefault(model)}
                        className="gap-0"
                      >
                        <RadioGroupItem
                          value={model.modelKey}
                          checked={isDefault}
                          disabled={!canSetDefault}
                          aria-label={
                            isDefault
                              ? t(
                                  "llmProviders.modelsTable.currentDefaultAria",
                                  {
                                    model: model.modelId,
                                  },
                                )
                              : t("llmProviders.modelsTable.setDefaultAria", {
                                  model: model.modelId,
                                })
                          }
                        />
                      </RadioGroup>
                    </TableCell>
                    <TableCell className="py-2.5">
                      <Switch
                        checked={model.enabled}
                        disabled={!canDisable}
                        onCheckedChange={() => onToggleModelEnabled(model)}
                        size="sm"
                        title={
                          canDisable
                            ? undefined
                            : t(
                                "llmProviders.modelsTable.disableBlockedDefault",
                              )
                        }
                        aria-label={t("llmProviders.modelsTable.enableNamed", {
                          model: model.modelId,
                        })}
                      />
                    </TableCell>
                    <TableCell className="px-4 py-2.5">
                      <div className="flex justify-end gap-1">
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-xs"
                          className="size-[26px] text-muted-foreground"
                          aria-label={t(
                            "llmProviders.modelsTable.testModelNamed",
                            {
                              model: model.modelId,
                            },
                          )}
                          title={t("llmProviders.modelsTable.testTitle")}
                          onClick={() => onTestModel(model)}
                          disabled={testingModelId === model.id}
                        >
                          {testingModelId === model.id ? (
                            <Loader2
                              className="size-3.5 animate-spin"
                              data-icon="only"
                              aria-hidden="true"
                            />
                          ) : (
                            <SendHorizontal
                              data-icon="only"
                              aria-hidden="true"
                            />
                          )}
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-xs"
                          className="size-[26px] text-muted-foreground"
                          aria-label={t(
                            "llmProviders.modelsTable.editModelNamed",
                            {
                              model: model.modelId,
                            },
                          )}
                          title={t("common.edit")}
                          onClick={() => onEditModel(model)}
                        >
                          <Pencil data-icon="only" aria-hidden="true" />
                        </Button>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon-xs"
                              className="size-[26px] text-muted-foreground"
                              aria-label={t(
                                "llmProviders.modelsTable.moreModelNamed",
                                {
                                  model: model.modelId,
                                },
                              )}
                              title={t(
                                "llmProviders.modelsTable.moreModelNamed",
                                {
                                  model: model.modelId,
                                },
                              )}
                            >
                              <MoreHorizontal
                                data-icon="only"
                                aria-hidden="true"
                              />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem
                              variant="destructive"
                              disabled={deleteBlocked}
                              onSelect={() => onDeleteModel(model)}
                              title={deleteBlockedReason}
                            >
                              <Trash2 className="size-3.5" aria-hidden="true" />
                              {t("llmProviders.modelsTable.deleteModelNamed", {
                                model: model.modelId,
                              })}
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
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
