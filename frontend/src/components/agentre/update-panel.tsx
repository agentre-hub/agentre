import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  ArrowUp,
  CircleCheck,
  Download,
  ExternalLink,
  RefreshCw,
  RotateCw,
  Settings,
  TriangleAlert,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { useUpdateStore } from "@/stores/update-store";

import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";
import { getUpdateChannel, type UpdateChannel } from "./update-api";

const CHANNEL_LABEL: Record<UpdateChannel, string> = {
  stable: "update.channel.stable.label",
  beta: "update.channel.beta.label",
  nightly: "update.channel.nightly.label",
};

function formatVersion(v: string, unknown: string): string {
  if (!v) return unknown;
  return v.startsWith("v") ? v : `v${v}`;
}

/** 只取日期部分：发布时间精确到秒对用户没有意义，且会把副行挤到第二行。 */
function formatPublished(raw: string, unknown: string): string {
  if (!raw) return unknown;
  const day = raw.slice(0, 10);
  return /^\d{4}-\d{2}-\d{2}$/.test(day) ? day : raw;
}

type Tone = "primary" | "success" | "danger";

const TONE_CLASS: Record<Tone, string> = {
  primary: "bg-primary-soft text-primary-text",
  success: "bg-status-running-bg text-status-running",
  danger: "bg-destructive-soft text-destructive",
};

function PanelHeader({
  icon: Icon,
  tone,
  title,
  meta,
}: {
  icon: LucideIcon;
  tone: Tone;
  title: string;
  meta: React.ReactNode;
}) {
  return (
    <div className="flex items-start gap-2.5 border-b border-border px-3 py-3">
      <span
        className={cn(
          "inline-flex size-7 shrink-0 items-center justify-center rounded-lg",
          TONE_CLASS[tone],
        )}
      >
        <Icon className="size-[15px]" aria-hidden="true" />
      </span>
      <div className="min-w-0">
        <div className="text-[13px] font-semibold">{title}</div>
        <div className="mt-0.5 text-2xs leading-relaxed text-muted-foreground">
          {meta}
        </div>
      </div>
    </div>
  );
}

/**
 * UpdatePanel 是状态栏胶囊点开后的就地更新去处：看清楚是什么版本、直接装完。
 *
 * 它**不**承载通道 / 镜像 / 调试日志 —— 那些仍归「设置 → 版本 & 更新」，
 * 面板只提供一个通往那里的入口。
 */
export function UpdatePanel({
  onOpenSettings,
}: {
  onOpenSettings: () => void;
}) {
  const { t } = useTranslation();
  const phase = useUpdateStore((s) => s.phase);
  const lastCheckedAt = useUpdateStore((s) => s.lastCheckedAt);
  const check = useUpdateStore((s) => s.check);
  const download = useUpdateStore((s) => s.download);
  const skipCurrentVersion = useUpdateStore((s) => s.skipCurrentVersion);
  const restart = useUpdateStore((s) => s.restart);

  // 通道不进 store：后端才是它的真源，设置页与这里各自读一次，不做第二份状态。
  const [channel, setChannel] = React.useState<UpdateChannel>("stable");
  React.useEffect(() => {
    let cancelled = false;
    void getUpdateChannel()
      .then((c) => {
        if (!cancelled) setChannel(c);
      })
      .catch(() => {
        // 读不到通道不该让整个面板打不开，退回默认展示。
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const unknownVersion = t("update.version.unknown");
  const unknownTime = t("update.release.unknownTime");
  const channelLabel = t(CHANNEL_LABEL[channel]);

  const settingsButton = (
    <Button type="button" variant="ghost" size="sm" onClick={onOpenSettings}>
      <Settings aria-hidden="true" className="size-3.5" />
      {t("update.panel.openSettings")}
    </Button>
  );

  if (phase.kind === "available" || phase.kind === "downloading") {
    const info = phase.info;
    const downloading = phase.kind === "downloading";
    return (
      <div className="flex flex-col">
        <PanelHeader
          icon={downloading ? Download : ArrowUp}
          tone="primary"
          title={
            downloading
              ? t("update.panel.downloadingTitle", {
                  version: formatVersion(info.latestVersion, unknownVersion),
                })
              : t("update.panel.availableTitle", {
                  version: formatVersion(info.latestVersion, unknownVersion),
                })
          }
          meta={
            downloading
              ? t("update.panel.downloadingMeta")
              : t("update.panel.availableMeta", {
                  time: formatPublished(info.publishedAt, unknownTime),
                  channel: channelLabel,
                  current: formatVersion(info.currentVersion, unknownVersion),
                })
          }
        />

        {downloading ? (
          <div className="flex flex-col gap-2 px-3 py-3">
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-primary transition-[width] duration-200"
                style={{ width: `${phase.progress}%` }}
              />
            </div>
            <div className="flex justify-end font-mono text-2xs text-muted-foreground">
              {`${phase.progress}%`}
            </div>
          </div>
        ) : (
          <>
            <div className="px-3 py-3">
              {info.releaseNotes ? (
                <div data-selectable-text="true">
                  <pre className="max-h-[168px] overflow-auto whitespace-pre-wrap rounded-md border border-border bg-muted/40 px-2.5 py-2 text-2xs leading-relaxed">
                    {info.releaseNotes}
                  </pre>
                </div>
              ) : (
                <p className="text-2xs text-muted-foreground">
                  {t("update.release.noNotes")}
                </p>
              )}
            </div>
            <div className="flex items-center gap-2 border-t border-border px-3 py-2.5">
              <Button
                type="button"
                size="sm"
                onClick={() => void download(false)}
              >
                <Download aria-hidden="true" className="size-3.5" />
                {t("update.actions.downloadAndInstall")}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => BrowserOpenURL(info.releaseURL)}
              >
                <ExternalLink aria-hidden="true" className="size-3.5" />
                {t("update.panel.releasePage")}
              </Button>
              <Button
                type="button"
                variant="link"
                size="sm"
                className="ml-auto text-muted-foreground"
                onClick={() => void skipCurrentVersion()}
              >
                {t("update.panel.skipVersion")}
              </Button>
            </div>
          </>
        )}
      </div>
    );
  }

  if (phase.kind === "installed") {
    return (
      <div className="flex flex-col">
        <PanelHeader
          icon={CircleCheck}
          tone="success"
          title={t("update.panel.installedTitle", {
            version: formatVersion(phase.info.latestVersion, unknownVersion),
          })}
          meta={t("update.panel.installedMeta")}
        />
        <div className="flex items-center gap-2 px-3 py-2.5">
          <Button type="button" size="sm" onClick={() => void restart()}>
            <RotateCw aria-hidden="true" className="size-3.5" />
            {t("update.actions.restartNow")}
          </Button>
          <span className="text-2xs text-muted-foreground">
            {t("update.panel.later")}
          </span>
        </div>
      </div>
    );
  }

  if (phase.kind === "error") {
    return (
      <div className="flex flex-col">
        <PanelHeader
          icon={TriangleAlert}
          tone="danger"
          title={t("update.panel.errorTitle")}
          meta={t("update.panel.errorMeta")}
        />
        <div className="px-3 py-3">
          {/* 错误详情选中复制，不加复制按钮。 */}
          <div data-selectable-text="true">
            <pre className="max-h-[140px] overflow-auto whitespace-pre-wrap rounded-md border border-border bg-muted/40 px-2.5 py-2 font-mono text-2xs leading-relaxed">
              {phase.message}
            </pre>
          </div>
        </div>
        <div className="flex items-center gap-2 border-t border-border px-3 py-2.5">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => void check("manual")}
          >
            <RefreshCw aria-hidden="true" className="size-3.5" />
            {t("update.panel.retry")}
          </Button>
          {settingsButton}
        </div>
      </div>
    );
  }

  // idle 与 uptodate：都没有可安装的东西，区别只在「查过没有」。
  const checked = phase.kind === "uptodate";
  return (
    <div className="flex flex-col">
      <PanelHeader
        icon={checked ? CircleCheck : RefreshCw}
        tone={checked ? "success" : "primary"}
        title={
          checked
            ? t("update.panel.upToDateTitle")
            : t("update.panel.idleTitle")
        }
        meta={
          <>
            {checked
              ? t("update.panel.upToDateMeta", {
                  current: t("update.version.unknown"),
                  channel: channelLabel,
                })
              : t("update.panel.idleMeta", {
                  current: t("update.version.unknown"),
                  channel: channelLabel,
                })}
            <br />
            {lastCheckedAt === null
              ? t("update.panel.neverChecked")
              : t("update.panel.checkedAt", {
                  time: new Date(lastCheckedAt).toLocaleTimeString(),
                })}
          </>
        }
      />
      <div className="flex items-center gap-2 px-3 py-2.5">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={phase.kind === "checking"}
          onClick={() => void check("manual")}
        >
          <RefreshCw aria-hidden="true" className="size-3.5" />
          {checked ? t("update.panel.checkAgain") : t("update.actions.check")}
        </Button>
        {settingsButton}
      </div>
    </div>
  );
}
