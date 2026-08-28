import * as React from "react";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  Button,
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  ProviderPillResolution,
  ProviderPillTrigger,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import type { UseProviderPillReturn } from "./use-provider-pill";
import { ModelTargetPicker } from "../model-target-picker";

export type ProviderPillProps = UseProviderPillReturn;

/**
 * ProviderPill: composer 动作行里的 LLM ModelTarget 选择器。
 *
 * 常驻触发按钮直接呈现四态与解析结果：跟随 Agent、跟随供应商默认、固定模型、失效。
 * 共享 ModelTargetPicker 继续承载搜索、最近使用、远端门控与键盘交互。
 */
export function ProviderPill({
  providerKey,
  modelKey,
  setTarget,
  backendType,
  catalog,
  loading,
  catalogLoading,
  catalogError,
  error,
  unbound,
  effectiveKey,
  pillState,
  boundResolutionLabel,
  boundProviderType,
  boundProviderLabel,
  boundModelLabel,
  boundCliLogin,
  invalid,
  disabled,
  disabledReason,
  executionLocation = "",
  remoteCatalog,
  supportsFixedModel = true,
  remoteMissing = false,
  syncProvider,
  canSyncProvider = false,
}: ProviderPillProps) {
  const { t } = useTranslation();
  const [providerToSync, setProviderToSync] = React.useState<{
    providerKey: string;
    name: string;
  } | null>(null);
  const [syncing, setSyncing] = React.useState(false);
  const [syncError, setSyncError] = React.useState<string | null>(null);

  const disabledTitle =
    disabled && disabledReason === "unsupportedBackend"
      ? t("providerPill.disabledUnsupportedBackend")
      : disabled && disabledReason === "noCompatibleProviders"
        ? t("providerPill.disabledNoCompatibleProviders")
        : null;

  const ariaValue =
    pillState.resolutionLabel ||
    effectiveKey ||
    (unbound ? t("providerPill.unselected") : t("providerPill.unselected"));

  // 触发器与「跟随 Agent 绑定」那一项的解析副行都住在共享包里：那两格是用户一眼
  // 看到的东西，两端不同源就等于同一条对话在桌面端与浏览器里说两句话。宿主这边只
  // 负责把视图模型递进去，以及同步弹窗、持久化与重载这些宿主自己的事。
  const specialSublabel = (
    <ProviderPillResolution
      boundProviderType={boundProviderType}
      boundProviderLabel={boundProviderLabel}
      boundModelLabel={boundModelLabel}
      boundCliLogin={boundCliLogin}
      fallbackLabel={boundResolutionLabel}
    />
  );

  return (
    <>
      <ModelTargetPicker
        scenario="chat"
        backendType={backendType}
        executionLocation={executionLocation}
        selected={{ providerKey, modelKey }}
        onChange={setTarget}
        catalog={catalog}
        loading={loading || catalogLoading}
        error={catalogError || error !== null}
        errorText={error ?? undefined}
        disabled={disabled}
        invalid={invalid}
        specialSublabel={specialSublabel}
        onSyncProvider={
          canSyncProvider
            ? (provider) => {
                setSyncError(null);
                setProviderToSync({
                  providerKey: provider.providerKey,
                  name: provider.name,
                });
              }
            : undefined
        }
        remoteCatalog={remoteCatalog}
        supportsFixedModel={supportsFixedModel}
        remoteMissing={remoteMissing}
        triggerLabel={<ProviderPillTrigger state={pillState} />}
        title={disabledTitle ?? undefined}
        footer={t("providerPill.switchNote")}
        aria-label={t("providerPill.aria", { provider: ariaValue })}
        data-testid="provider-pill"
        className={cn(
          // mockup ?view=chat 的 .pill：中性描边 + card 底，全程一个样 —— 选没选、
          // 跟随还是固定，一律不靠常亮边框宣示，那份区分由图标与 ↻ 承担。
          // 悬停只加深背景，不动边框（边框式反馈是明确否掉的）；focus-visible ring
          // 由 Picker 基类提供，键盘定位不受影响。
          "h-[26px] w-auto cursor-pointer gap-1.5 rounded-md px-2.5 text-2xs font-medium",
          invalid
            ? "border-status-waiting bg-status-waiting-bg text-status-waiting hover:bg-status-waiting-bg/70"
            : "border-border bg-card text-foreground hover:bg-accent",
        )}
      />
      <Dialog
        open={providerToSync !== null}
        onOpenChange={(open) => {
          if (!open && !syncing) {
            setProviderToSync(null);
            setSyncError(null);
          }
        }}
      >
        <DialogContent className="max-w-[440px]">
          <DialogHeader>
            <DialogTitle>{t("modelTargetPicker.syncDialog.title")}</DialogTitle>
            <DialogDescription>
              {t("modelTargetPicker.syncDialog.description", {
                provider: providerToSync?.name ?? "",
              })}
            </DialogDescription>
          </DialogHeader>
          <DialogBody>
            {syncError ? (
              <p role="alert" className="text-xs text-status-error">
                {syncError}
              </p>
            ) : (
              <p className="text-xs text-muted-foreground">
                {t("modelTargetPicker.syncDialog.confirmation")}
              </p>
            )}
          </DialogBody>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={syncing}
              onClick={() => setProviderToSync(null)}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="button"
              size="sm"
              disabled={syncing || providerToSync === null}
              onClick={() => {
                if (!providerToSync) return;
                setSyncing(true);
                setSyncError(null);
                void syncProvider(providerToSync.providerKey)
                  .then(() => setProviderToSync(null))
                  .catch((err: unknown) => {
                    setSyncError(
                      err instanceof Error ? err.message : String(err),
                    );
                  })
                  .finally(() => setSyncing(false));
              }}
            >
              {syncing ? (
                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
              ) : null}
              {t("modelTargetPicker.syncDialog.confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
