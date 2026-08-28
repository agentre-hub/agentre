// 模型表：8 列表头 + 每行一个模型。行内承担的判断全在这里 ——
// 默认模型不可停用也不可删除、被引用数、本次会话内的「已通过」徽标，以及选中态下
// 那条「能不能删」的行内结论。选择集合由工作区持有，这里只上报变化。
import {
  Loader2,
  MoreHorizontal,
  Pencil,
  SendHorizontal,
  Trash2,
} from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Button } from "../../ui/button";
import { Checkbox } from "../../ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../../ui/dropdown-menu";
import { RadioGroup, RadioGroupItem } from "../../ui/radio-group";
import { Switch } from "../../ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../../ui/table";
import { cn } from "../../lib/utils";

import { LlmModelLogo } from "../ai-brand-logo";
import {
  type Model,
  type Provider,
  type ReferenceCounts,
  formatTokens,
  modelDeleteability,
  totalReferences,
} from "./index";

export function ModelsTable({
  provider,
  models,
  selected,
  allVisibleSelected,
  onSelectAll,
  onClearSelection,
  onToggleRow,
  modelRefCounts,
  passedModelTests,
  testingModelId,
  onSetDefault,
  onToggleModelEnabled,
  onTestModel,
  onEditModel,
  onDeleteModel,
}: {
  provider: Provider;
  // models 已按搜索过滤，表格只渲染传进来的这些行。
  models: Model[];
  selected: ReadonlySet<number>;
  allVisibleSelected: boolean;
  onSelectAll: () => void;
  onClearSelection: () => void;
  onToggleRow: (modelId: number, checked: boolean) => void;
  modelRefCounts: Map<string, ReferenceCounts>;
  passedModelTests: ReadonlyMap<number, string>;
  testingModelId: number | null;
  onSetDefault: (model: Model) => void;
  onToggleModelEnabled: (model: Model) => void;
  onTestModel: (model: Model) => void;
  onEditModel: (model: Model) => void;
  onDeleteModel: (model: Model) => void;
}) {
  const { t } = useTranslation();
  return (
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
                  onSelectAll();
                } else {
                  onClearSelection();
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
        {models.map((model) => {
          const isDefault = model.modelKey === provider.defaultModelKey;
          const canDisable = !isDefault;
          const canSetDefault = model.enabled && !isDefault;
          const refCount = totalReferences(modelRefCounts.get(model.modelKey));
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
                  onCheckedChange={(checked) =>
                    onToggleRow(model.id, checked === true)
                  }
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
                          title={t("llmProviders.modelsTable.testPassedHint", {
                            duration: passedDuration,
                          })}
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
                            : t("llmProviders.modelsTable.batch.rowCanDelete")}
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
                        ? t("llmProviders.modelsTable.currentDefaultAria", {
                            model: model.modelId,
                          })
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
                      : t("llmProviders.modelsTable.disableBlockedDefault")
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
                    aria-label={t("llmProviders.modelsTable.testModelNamed", {
                      model: model.modelId,
                    })}
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
                      <SendHorizontal data-icon="only" aria-hidden="true" />
                    )}
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-xs"
                    className="size-[26px] text-muted-foreground"
                    aria-label={t("llmProviders.modelsTable.editModelNamed", {
                      model: model.modelId,
                    })}
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
                        title={t("llmProviders.modelsTable.moreModelNamed", {
                          model: model.modelId,
                        })}
                      >
                        <MoreHorizontal data-icon="only" aria-hidden="true" />
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
  );
}
