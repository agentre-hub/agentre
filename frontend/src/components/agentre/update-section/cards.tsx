// 版本卡片:可用更新 / 已安装 / 出错三种形态。

import { useTranslation } from "react-i18next";
import { AlertCircle, Download, RotateCw } from "lucide-react";

import {
  Alert,
  AlertDescription,
  AlertTitle,
  Button,
} from "@agentre-hub/agentre-ui";

import { type UpdateInfo } from "../update-api";

import { formatVersion, formatProgress } from "./format";

export function AvailableCard({
  info,
  downloading,
  progress,
  onDownload,
}: {
  info: UpdateInfo;
  downloading: boolean;
  progress: number;
  onDownload: () => void;
}) {
  const { t } = useTranslation();
  const unknownTimeLabel = t("update.release.unknownTime");
  const unknownVersionLabel = t("update.version.unknown");

  return (
    <section className="overflow-hidden rounded-lg border border-agent-1/30 bg-agent-1/5">
      <div className="flex flex-wrap items-center gap-3 border-b border-agent-1/20 px-4 py-3">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <h2 className="text-sm font-semibold text-agent-1">
            {t("update.release.available", {
              version: formatVersion(info.latestVersion, unknownVersionLabel),
            })}
          </h2>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t("update.release.publishedAt", {
              time: info.publishedAt || unknownTimeLabel,
            })}
          </p>
        </div>
      </div>

      <div className="flex flex-col gap-4 p-4">
        {info.releaseNotes ? (
          <pre className="max-h-[220px] overflow-auto whitespace-pre-wrap rounded-md border border-border bg-muted/40 p-3 text-xs leading-relaxed">
            {info.releaseNotes}
          </pre>
        ) : (
          <p className="text-xs text-muted-foreground">
            {t("update.release.noNotes")}
          </p>
        )}

        {downloading ? (
          <div className="flex flex-col gap-2">
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-agent-1 transition-[width] duration-200"
                style={{ width: `${progress}%` }}
              />
            </div>
            <div className="flex items-center justify-between text-xs text-muted-foreground">
              <span>{t("update.actions.downloadingShort")}</span>
              <span className="font-mono">{formatProgress(progress)}</span>
            </div>
          </div>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            <Button type="button" onClick={onDownload}>
              <Download aria-hidden="true" className="size-4" />
              {t("update.actions.downloadAndInstall")}
            </Button>
          </div>
        )}
      </div>
    </section>
  );
}

export function InstalledCard({
  info,
  onRestart,
}: {
  info: UpdateInfo;
  onRestart: () => void;
}) {
  const { t } = useTranslation();
  const unknownVersionLabel = t("update.version.unknown");

  return (
    <section className="overflow-hidden rounded-lg border border-status-running/30 bg-status-running-bg">
      <div className="flex flex-wrap items-center gap-3 border-b border-status-running/20 px-4 py-3">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <h2 className="text-sm font-semibold text-status-running">
            {t("update.installed.title", {
              version: formatVersion(info.latestVersion, unknownVersionLabel),
            })}
          </h2>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t("update.installed.description")}
          </p>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2 p-4">
        <Button type="button" onClick={onRestart}>
          <RotateCw aria-hidden="true" className="size-4" />
          {t("update.actions.restartNow")}
        </Button>
      </div>
    </section>
  );
}

export function ErrorCard({ message }: { message: string }) {
  const { t } = useTranslation();

  return (
    <Alert variant="destructive">
      <AlertCircle className="size-4" aria-hidden="true" />
      <AlertTitle className="text-xs font-semibold">
        {t("update.error.title")}
      </AlertTitle>
      <AlertDescription className="text-2xs leading-relaxed">
        {message}
      </AlertDescription>
    </Alert>
  );
}
