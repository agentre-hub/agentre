// 模型清单（逐层档）：一行一个模型、开关在最右，没有最小宽度的表格。
// 行内的判断（默认模型不可停用 / 不可删除、已通过徽标）与 ModelsTable 一致，
// 次要动作（测试 / 编辑 / 设为默认 / 删除）收进行内「更多」菜单。
import { Loader2, MoreHorizontal } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Button } from "../../ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../../ui/dropdown-menu";
import { Switch } from "../../ui/switch";
import { cn } from "../../lib/utils";

import { LlmModelLogo } from "../ai-brand-logo";
import { type Model, type Provider, modelDeleteability } from "./index";

export function ModelsList({
  provider,
  models,
  passedModelTests,
  testingModelId,
  onSetDefault,
  onToggleModelEnabled,
  onTestModel,
  onEditModel,
  onDeleteModel,
}: {
  provider: Provider;
  models: Model[];
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
    <ul
      aria-label={t("llmProviders.workspace.modelsList")}
      className="flex flex-col"
    >
      {models.map((model) => {
        const isDefault = model.modelKey === provider.defaultModelKey;
        const canSetDefault = model.enabled && !isDefault;
        const deleteBlocked =
          modelDeleteability(model, provider.defaultModelKey).kind ===
          "default";
        const passed = passedModelTests.get(model.id);
        return (
          <li
            key={model.id}
            className={cn(
              "flex min-w-0 items-center gap-2 px-3 py-2.5",
              !model.enabled && "opacity-55",
            )}
          >
            <LlmModelLogo
              providerType={provider.type}
              providerName={provider.name}
              baseUrl={provider.baseUrl}
              model={model.modelId}
              className="size-4 shrink-0"
            />
            <div className="flex min-w-0 flex-1 flex-col gap-0.5">
              <span className="flex min-w-0 items-center gap-1.5">
                <span className="truncate text-xs">
                  {model.name || model.modelId}
                </span>
                {isDefault ? (
                  <span className="shrink-0 rounded-sm bg-primary-soft px-1.5 py-0.5 text-2xs font-medium text-primary-text">
                    {t("llmProviders.modelsTable.defaultBadge")}
                  </span>
                ) : null}
                {passed ? (
                  <span
                    className="shrink-0 rounded-sm bg-status-running/10 px-1.5 py-0.5 text-2xs font-medium text-status-running-text"
                    title={t("llmProviders.modelsTable.testPassedHint", {
                      duration: passed,
                    })}
                  >
                    {t("llmProviders.modelsTable.testPassed")}
                  </span>
                ) : null}
              </span>
              <span className="truncate font-mono text-2xs text-muted-foreground">
                {model.modelId}
              </span>
            </div>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="shrink-0 text-muted-foreground"
                  aria-label={t("llmProviders.modelsTable.moreModelNamed", {
                    model: model.modelId,
                  })}
                >
                  {testingModelId === model.id ? (
                    <Loader2
                      className="animate-spin"
                      data-icon="only"
                      aria-hidden="true"
                    />
                  ) : (
                    <MoreHorizontal data-icon="only" aria-hidden="true" />
                  )}
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem
                  disabled={testingModelId === model.id}
                  onSelect={() => onTestModel(model)}
                >
                  {t("llmProviders.modelsTable.testModelNamed", {
                    model: model.modelId,
                  })}
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => onEditModel(model)}>
                  {t("llmProviders.modelsTable.editModelNamed", {
                    model: model.modelId,
                  })}
                </DropdownMenuItem>
                <DropdownMenuItem
                  disabled={!canSetDefault}
                  onSelect={() => onSetDefault(model)}
                >
                  {t("llmProviders.modelsTable.setDefaultAria", {
                    model: model.modelId,
                  })}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  variant="destructive"
                  disabled={deleteBlocked}
                  title={
                    deleteBlocked
                      ? t("llmProviders.modelsTable.deleteBlockedDefault")
                      : undefined
                  }
                  onSelect={() => onDeleteModel(model)}
                >
                  {t("llmProviders.modelsTable.deleteModelNamed", {
                    model: model.modelId,
                  })}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            <Switch
              checked={model.enabled}
              disabled={isDefault}
              onCheckedChange={() => onToggleModelEnabled(model)}
              size="sm"
              title={
                isDefault
                  ? t("llmProviders.modelsTable.disableBlockedDefault")
                  : undefined
              }
              aria-label={t("llmProviders.modelsTable.enableNamed", {
                model: model.modelId,
              })}
            />
          </li>
        );
      })}
    </ul>
  );
}
