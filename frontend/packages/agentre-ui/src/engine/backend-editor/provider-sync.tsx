// 供应商同步的两处 UI：编辑器里那条常驻入口（远端执行且草稿真引用了供应商时才出现），
// 与确认弹窗（把凭证复制到目标设备，可选「同步后继续保存」）。
import { AlertCircle, Loader2, Radar } from "lucide-react";

import { useUiTranslation as useTranslation } from "../../i18n";
import { Alert, AlertDescription, AlertTitle } from "../../ui/alert";
import { Button } from "../../ui/button";
import type { Provider } from "../agent-backends-shared";
import { AgentreDialog } from "../app-dialog";

import { providerLabel, type PendingProviderSync } from "./draft";

export function ManualProviderSyncAlert({
  disabled,
  onSync,
}: {
  disabled: boolean;
  onSync: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Alert className="border-border bg-secondary text-xs">
      <Radar className="size-4" aria-hidden="true" />
      <AlertTitle className="text-xs">
        {t("agentBackends.providerSync.inlineTitle")}
      </AlertTitle>
      <AlertDescription className="flex flex-col gap-2 text-2xs">
        <span>{t("agentBackends.providerSync.inlineDescription")}</span>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="self-start"
          disabled={disabled}
          onClick={onSync}
        >
          {t("agentBackends.providerSync.syncRemote")}
        </Button>
      </AlertDescription>
    </Alert>
  );
}

export function ProviderSyncDialog({
  pending,
  providers,
  syncing,
  error,
  onClose,
  onConfirm,
}: {
  pending: PendingProviderSync;
  providers: Provider[];
  syncing: boolean;
  error: string | null;
  onClose: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();
  return (
    <AgentreDialog
      open
      onOpenChange={(o) => (!o && !syncing ? onClose() : undefined)}
      title={t("agentBackends.providerSync.title")}
      description={
        pending.saveAfterSync
          ? t("agentBackends.providerSync.descriptionSave")
          : t("agentBackends.providerSync.descriptionOnly")
      }
      bodyClassName="flex flex-col gap-3"
      footer={
        <div className="flex w-full items-center justify-end gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={syncing}
            onClick={onClose}
          >
            {t("common.cancel")}
          </Button>
          <Button type="button" disabled={syncing} onClick={onConfirm}>
            {syncing ? (
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
            ) : null}
            {syncing
              ? t("agentBackends.providerSync.syncing")
              : pending.saveAfterSync
                ? t("agentBackends.providerSync.syncAndSave")
                : t("agentBackends.providerSync.syncRemote")}
          </Button>
        </div>
      }
    >
      <Alert className="border-status-waiting/40 bg-status-waiting-bg text-xs">
        <AlertCircle className="size-4" aria-hidden="true" />
        <AlertTitle className="text-xs">
          {t("agentBackends.providerSync.requiredTitle")}
        </AlertTitle>
        <AlertDescription className="text-2xs">
          {t("agentBackends.providerSync.requiredDescription")}
        </AlertDescription>
      </Alert>
      {error ? (
        <Alert className="border-status-error/40 bg-status-error-bg text-xs">
          <AlertCircle className="size-4" aria-hidden="true" />
          <AlertTitle className="text-xs">
            {t("agentBackends.providerSync.failedTitle")}
          </AlertTitle>
          <AlertDescription className="whitespace-pre-line text-2xs">
            {error}
          </AlertDescription>
        </Alert>
      ) : null}
      <div className="flex flex-col gap-1.5 text-xs">
        {pending.providerKeys.map((key) => (
          <div
            key={key}
            className="flex items-center justify-between rounded-md border border-border bg-secondary px-2 py-1.5"
          >
            <span className="min-w-0 truncate">
              {providerLabel(key, providers)}
            </span>
            <span className="ml-2 shrink-0 font-mono text-2xs text-muted-foreground">
              {key}
            </span>
          </div>
        ))}
      </div>
    </AgentreDialog>
  );
}
