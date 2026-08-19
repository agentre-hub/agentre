import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  AlertCircle,
  Bug,
  CheckCircle2,
  Download,
  ExternalLink,
  FolderOpen,
  Info,
  Loader2,
  RefreshCw,
  RotateCw,
} from "lucide-react";
import { toast } from "sonner";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

import { Info as FetchAppInfo } from "../../../wailsjs/go/app/App";
import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";
import { useUpdateStore } from "@/stores/update-store";

import {
  getAvailableMirrors,
  getBugReportInfo,
  getDebugLogging,
  getDownloadMirror,
  getUpdateChannel,
  openLogsDir,
  setDebugLogging,
  setDownloadMirror,
  setUpdateChannel,
  type MirrorInfo,
  type UpdateChannel,
  type UpdateInfo,
} from "./update-api";

const CHANNEL_LABEL: Record<UpdateChannel, string> = {
  stable: "update.channel.stable.label",
  beta: "update.channel.beta.label",
  nightly: "update.channel.nightly.label",
};

const CHANNEL_DESC: Record<UpdateChannel, string> = {
  stable: "update.channel.stable.description",
  beta: "update.channel.beta.description",
  nightly: "update.channel.nightly.description",
};

const REPOSITORY_URL = "https://github.com/agentre-ai/agentre";

// MIRROR_CUSTOM_ID select 中"自定义"选项的特殊值；选中时显示 input。
const MIRROR_CUSTOM_ID = "__custom__";

function formatVersion(v: string, unknownLabel: string): string {
  if (!v) return unknownLabel;
  return v.startsWith("v") ? v : `v${v}`;
}

function formatProgress(p: number): string {
  if (!Number.isFinite(p) || p <= 0) return "0%";
  if (p >= 100) return "100%";
  return `${p.toFixed(0)}%`;
}

function pickMirrorOption(
  builtins: MirrorInfo[],
  current: string,
): { selectValue: string; customDraft: string } {
  const found = builtins.find((m) => m.url === current);
  if (found) {
    return { selectValue: found.id, customDraft: "" };
  }
  if (current === "") {
    return { selectValue: "github", customDraft: "" };
  }
  return { selectValue: MIRROR_CUSTOM_ID, customDraft: current };
}

export function UpdateSection() {
  const { t } = useTranslation();
  const [appVersion, setAppVersion] = React.useState<string>("");
  const [appCommit, setAppCommit] = React.useState<string>("");
  const [channel, setChannel] = React.useState<UpdateChannel>("stable");
  const [mirrors, setMirrors] = React.useState<MirrorInfo[]>([]);
  const [mirrorSelectValue, setMirrorSelectValue] =
    React.useState<string>("github");
  const [customMirror, setCustomMirror] = React.useState<string>("");
  const [debugEnabled, setDebugEnabled] = React.useState<boolean>(false);
  // 更新状态是全局唯一一份：状态栏胶囊、更新面板与本页读的是同一个 store。
  const phase = useUpdateStore((s) => s.phase);
  const checksumPrompt = useUpdateStore((s) => s.checksumPrompt);
  const initUpdates = useUpdateStore((s) => s.init);
  const runCheck = useUpdateStore((s) => s.check);
  const runDownload = useUpdateStore((s) => s.download);
  const dismissChecksumPrompt = useUpdateStore((s) => s.dismissChecksumPrompt);
  const runRestart = useUpdateStore((s) => s.restart);

  React.useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const info = await FetchAppInfo();
        if (cancelled) return;
        setAppVersion(info.version ?? "");
        setAppCommit(info.commit ?? "");
      } catch (err) {
        console.warn("fetch app info failed", err);
      }

      try {
        const [ch, mr, ms] = await Promise.all([
          getUpdateChannel(),
          getDownloadMirror(),
          getAvailableMirrors(),
        ]);
        if (cancelled) return;
        setChannel(ch);
        setMirrors(ms);
        const picked = pickMirrorOption(ms, mr);
        setMirrorSelectValue(picked.selectValue);
        setCustomMirror(picked.customDraft);
      } catch (err) {
        console.warn("fetch update settings failed", err);
      }

      try {
        const on = await getDebugLogging();
        if (cancelled) return;
        setDebugEnabled(on);
      } catch (err) {
        console.warn("fetch debug logging failed", err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  React.useEffect(() => {
    let dispose: (() => void) | undefined;
    void initUpdates().then((d) => {
      dispose = d;
    });
    return () => dispose?.();
  }, [initUpdates]);

  const handleChannelChange = React.useCallback(
    async (next: string) => {
      const value = next as UpdateChannel;
      setChannel(value);
      try {
        await setUpdateChannel(value);
      } catch (err) {
        console.warn("save update channel failed", err);
        return;
      }
      // 切通道是用户主动动作,此刻他在等结果:立刻按新通道重查一次(绕过节流),
      // 而不是把先前的结果清成「未知」让他自己再点一次。
      await runCheck("manual");
    },
    [runCheck],
  );

  const persistMirror = React.useCallback(async (url: string) => {
    try {
      await setDownloadMirror(url);
    } catch (err) {
      console.warn("save mirror failed", err);
    }
  }, []);

  const handleMirrorSelectChange = React.useCallback(
    async (next: string) => {
      setMirrorSelectValue(next);
      if (next === MIRROR_CUSTOM_ID) {
        // 切到自定义不立即写库，等用户 onBlur 时再写。
        return;
      }
      const found = mirrors.find((m) => m.id === next);
      const url = found?.url ?? "";
      setCustomMirror("");
      await persistMirror(url);
    },
    [mirrors, persistMirror],
  );

  const handleCustomMirrorBlur = React.useCallback(async () => {
    if (mirrorSelectValue !== MIRROR_CUSTOM_ID) return;
    await persistMirror(customMirror.trim());
  }, [customMirror, mirrorSelectValue, persistMirror]);

  const handleCheck = React.useCallback(() => {
    void runCheck("manual");
  }, [runCheck]);

  const handleDownload = React.useCallback(() => {
    void runDownload(false);
  }, [runDownload]);

  const handleSkipChecksum = React.useCallback(() => {
    dismissChecksumPrompt();
    void runDownload(true);
  }, [dismissChecksumPrompt, runDownload]);

  const handleRestart = React.useCallback(() => {
    void runRestart();
  }, [runRestart]);

  const handleReportBug = React.useCallback(async () => {
    const params = new URLSearchParams({
      template: "bug_report.yml",
      labels: "bug",
    });
    try {
      const info = await getBugReportInfo();
      const version = info.version + (info.commit ? ` (${info.commit})` : "");
      if (version.trim()) params.set("version", version);
      if (info.osLabel) params.set("os", info.osLabel);
    } catch (err) {
      // 取不到诊断信息时仍然打开模板，让用户手动填写。
      console.warn("fetch bug report info failed", err);
    }
    BrowserOpenURL(`${REPOSITORY_URL}/issues/new?${params.toString()}`);
  }, []);

  const handleOpenLogs = React.useCallback(async () => {
    try {
      await openLogsDir();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    }
  }, []);

  const handleDebugToggle = React.useCallback(
    async (next: boolean) => {
      setDebugEnabled(next);
      try {
        await setDebugLogging(next);
        toast.success(
          next
            ? t("update.debug.enabledToast")
            : t("update.debug.disabledToast"),
        );
      } catch (err) {
        setDebugEnabled(!next);
        toast.error(err instanceof Error ? err.message : String(err));
      }
    },
    [t],
  );

  const checkButtonState = (() => {
    if (phase.kind === "checking") {
      return {
        disabled: true,
        label: t("update.actions.checking"),
        icon: Loader2,
        spin: true,
      };
    }
    if (phase.kind === "downloading") {
      return {
        disabled: true,
        label: t("update.actions.downloading"),
        icon: Loader2,
        spin: true,
      };
    }
    return {
      disabled: false,
      label: t("update.actions.check"),
      icon: RefreshCw,
      spin: false,
    };
  })();
  const CheckIcon = checkButtonState.icon;
  const unknownVersionLabel = t("update.version.unknown");

  return (
    <>
      <SectionHeader />

      <section className="overflow-hidden rounded-lg border border-border bg-card">
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <h2 className="text-sm font-semibold">
              {t("update.currentVersion.title")}
            </h2>
            <p className="text-xs leading-relaxed text-muted-foreground">
              {t("update.currentVersion.description")}
            </p>
          </div>
          <Badge
            variant="secondary"
            className="rounded-sm px-1.5 py-0 font-mono text-2xs font-medium"
          >
            {formatVersion(appVersion, unknownVersionLabel)}
            {appCommit ? ` · ${appCommit}` : ""}
          </Badge>
        </div>

        <div className="flex flex-col gap-4 p-4">
          <RepositoryRow />
          <ChannelRow
            channel={channel}
            onChange={handleChannelChange}
            disabled={phase.kind === "downloading"}
          />
          <MirrorRow
            mirrors={mirrors}
            selectValue={mirrorSelectValue}
            customDraft={customMirror}
            onSelectChange={handleMirrorSelectChange}
            onCustomChange={setCustomMirror}
            onCustomBlur={handleCustomMirrorBlur}
            disabled={phase.kind === "downloading"}
          />
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              onClick={handleCheck}
              disabled={checkButtonState.disabled}
              variant="default"
            >
              <CheckIcon
                aria-hidden="true"
                className={cn(
                  "size-4",
                  checkButtonState.spin && "animate-spin",
                )}
              />
              {checkButtonState.label}
            </Button>
            {phase.kind === "uptodate" ? (
              <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                <CheckCircle2 className="size-3.5 text-status-running" />
                {t("update.status.upToDate")}
              </span>
            ) : null}
            <Button type="button" variant="outline" onClick={handleReportBug}>
              <Bug aria-hidden="true" className="size-4" />
              {t("update.actions.reportBug")}
            </Button>
            <Button type="button" variant="outline" onClick={handleOpenLogs}>
              <FolderOpen aria-hidden="true" className="size-4" />
              {t("update.actions.openLogs")}
            </Button>
          </div>

          <DebugRow enabled={debugEnabled} onToggle={handleDebugToggle} />
        </div>
      </section>

      {phase.kind === "available" || phase.kind === "downloading" ? (
        <AvailableCard
          info={phase.info}
          downloading={phase.kind === "downloading"}
          progress={phase.kind === "downloading" ? phase.progress : 0}
          onDownload={handleDownload}
        />
      ) : null}

      {phase.kind === "installed" ? (
        <InstalledCard info={phase.info} onRestart={handleRestart} />
      ) : null}

      {phase.kind === "error" ? <ErrorCard message={phase.message} /> : null}

      <ChecksumDialog
        open={checksumPrompt.open}
        reason={checksumPrompt.reason}
        onCancel={dismissChecksumPrompt}
        onConfirm={handleSkipChecksum}
      />
    </>
  );
}

function SectionHeader() {
  const { t } = useTranslation();

  return (
    <div className="flex max-w-3xl flex-col gap-1.5">
      <h1 className="text-2xl font-semibold tracking-normal">
        {t("update.header.title")}
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        {t("update.header.description")}
      </p>
    </div>
  );
}

function RepositoryRow() {
  const { t } = useTranslation();
  const handleClick = React.useCallback(
    (event: React.MouseEvent<HTMLAnchorElement>) => {
      event.preventDefault();
      BrowserOpenURL(REPOSITORY_URL);
    },
    [],
  );

  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="text-sm font-medium">
          {t("update.repository.title")}
        </span>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t("update.repository.description")}
        </p>
      </div>
      <a
        href={REPOSITORY_URL}
        target="_blank"
        rel="noreferrer"
        onClick={handleClick}
        className="inline-flex min-w-0 items-center gap-1.5 text-xs font-medium text-agent-1 underline-offset-4 hover:underline sm:max-w-[320px]"
      >
        <span className="truncate">{REPOSITORY_URL}</span>
        <ExternalLink className="size-3 shrink-0" aria-hidden="true" />
      </a>
    </div>
  );
}

function DebugRow({
  enabled,
  onToggle,
}: {
  enabled: boolean;
  onToggle: (next: boolean) => void;
}) {
  const { t } = useTranslation();
  const labelId = React.useId();
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span id={labelId} className="text-sm font-medium">
          {t("update.debug.title")}
        </span>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t("update.debug.description")}
        </p>
      </div>
      <Switch
        checked={enabled}
        onCheckedChange={onToggle}
        aria-labelledby={labelId}
      />
    </div>
  );
}

function ChannelRow({
  channel,
  onChange,
  disabled,
}: {
  channel: UpdateChannel;
  onChange: (next: string) => void;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const labelId = React.useId();
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span id={labelId} className="text-sm font-medium">
          {t("update.channel.title")}
        </span>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t(CHANNEL_DESC[channel])}
        </p>
      </div>
      <div className="w-full sm:w-[220px]">
        <Select value={channel} onValueChange={onChange} disabled={disabled}>
          <SelectTrigger aria-labelledby={labelId}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(["stable", "beta", "nightly"] as const).map((c) => (
              <SelectItem key={c} value={c}>
                {t(CHANNEL_LABEL[c])}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </div>
  );
}

function MirrorRow({
  mirrors,
  selectValue,
  customDraft,
  onSelectChange,
  onCustomChange,
  onCustomBlur,
  disabled,
}: {
  mirrors: MirrorInfo[];
  selectValue: string;
  customDraft: string;
  onSelectChange: (v: string) => void;
  onCustomChange: (v: string) => void;
  onCustomBlur: () => void;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const labelId = React.useId();
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div className="flex min-w-0 flex-col gap-0.5 sm:max-w-[300px]">
        <span id={labelId} className="text-sm font-medium">
          {t("update.mirror.title")}
        </span>
        <p className="text-xs leading-relaxed text-muted-foreground">
          {t("update.mirror.description")}
        </p>
      </div>
      <div className="flex w-full flex-col gap-2 sm:w-[260px]">
        <Select
          value={selectValue}
          onValueChange={onSelectChange}
          disabled={disabled}
        >
          <SelectTrigger aria-labelledby={labelId}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {mirrors.map((m) => (
              <SelectItem key={m.id} value={m.id}>
                {m.name}
              </SelectItem>
            ))}
            <SelectItem value={MIRROR_CUSTOM_ID}>
              {t("update.mirror.custom")}
            </SelectItem>
          </SelectContent>
        </Select>
        {selectValue === MIRROR_CUSTOM_ID ? (
          <Input
            type="url"
            value={customDraft}
            onChange={(e: React.ChangeEvent<HTMLInputElement>) =>
              onCustomChange(e.target.value)
            }
            onBlur={onCustomBlur}
            placeholder="https://your.mirror/"
            disabled={disabled}
          />
        ) : null}
      </div>
    </div>
  );
}

function AvailableCard({
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

function InstalledCard({
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

function ErrorCard({ message }: { message: string }) {
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

function ChecksumDialog({
  open,
  reason,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  reason: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useTranslation();

  return (
    <Dialog
      open={open}
      onOpenChange={(o: boolean) => (!o ? onCancel() : undefined)}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Info className="size-4 text-status-waiting" aria-hidden="true" />
            {t("update.checksum.title")}
          </DialogTitle>
          <DialogDescription>
            {t("update.checksum.description")}
          </DialogDescription>
        </DialogHeader>
        <DialogBody className="text-xs leading-relaxed">
          <div className="rounded-md border border-border bg-muted/40 p-2 font-mono text-2xs">
            {reason}
          </div>
          <p className="mt-3 text-muted-foreground">
            {t("update.checksum.warning")}
          </p>
        </DialogBody>
        <DialogFooter>
          <Button type="button" variant="ghost" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button type="button" variant="destructive" onClick={onConfirm}>
            {t("update.checksum.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
