// 工作区头部：身份行（品牌标识 + 名称 + 类型徽标）、右侧动作档位（启停开关 /
// 测试 / 发现 / 更多），以及下面那条元信息行（endpoint / 掩码 key / 默认模型 / 被引用）。
//
// 三条派生也在这里：能不能启用（Provider 启用需要属于它的启用默认模型）、默认模型名、
// 被引用摘要 —— 它们只有头部用得上。
import { useUiTranslation as useTranslation } from "../../i18n";
import {
  Copy,
  Loader2,
  MoreHorizontal,
  Pencil,
  RefreshCw,
  SendHorizontal,
  Trash2,
} from "lucide-react";

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

import { LlmProviderLogo } from "../ai-brand-logo";
import {
  type Model,
  type Provider,
  type ReferenceCounts,
  endpointFor,
  providerTypeMeta,
} from "./index";

export function ProviderWorkspaceHeader({
  provider,
  models,
  providerRefCounts,
  testingDefault,
  canTestProvider,
  canDiscover,
  onToggleProviderEnabled,
  onTestProvider,
  onDiscover,
  onEditConnection,
  onCopyProviderKey,
  onDeleteProvider,
}: {
  provider: Provider;
  models: Model[];
  providerRefCounts: ReferenceCounts | null;
  testingDefault: boolean;
  canTestProvider: boolean;
  canDiscover: boolean;
  onToggleProviderEnabled: () => void;
  onTestProvider: () => void;
  onDiscover: () => void;
  onEditConnection: () => void;
  onCopyProviderKey: () => void;
  onDeleteProvider: () => void;
}) {
  const { t } = useTranslation();
  const meta =
    provider.type in providerTypeMeta
      ? providerTypeMeta[provider.type as keyof typeof providerTypeMeta]
      : undefined;
  const endpoint = endpointFor(provider);

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
  );
}
