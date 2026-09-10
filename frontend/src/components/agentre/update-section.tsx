// 设置页的「版本与更新」区块:当前版本、渠道、镜像源,以及检查更新与下载安装。
//
// 它的零件在 update-section/ 下:format(常量与格式化)、rows(设置行)、
// cards(版本卡片)、checksum-dialog(校验和弹窗)。原先它们与本体挤在同一个
// 754 行的文件里。

import * as React from "react";
import { useTranslation } from "react-i18next";
import {
  Bug,
  CheckCircle2,
  FolderOpen,
  Loader2,
  RefreshCw,
} from "lucide-react";
import { toast } from "sonner";

import { Badge, Button } from "@agentre-hub/agentre-ui";
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
} from "./update-api";

import {
  AvailableCard,
  InstalledCard,
  ErrorCard,
} from "./update-section/cards";
import { ChecksumDialog } from "./update-section/checksum-dialog";
import {
  REPOSITORY_URL,
  MIRROR_CUSTOM_ID,
  formatVersion,
  pickMirrorOption,
} from "./update-section/format";
import {
  SectionHeader,
  RepositoryRow,
  DebugRow,
  ChannelRow,
  MirrorRow,
} from "./update-section/rows";

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
  const runCheck = useUpdateStore((s) => s.check);
  const runDownload = useUpdateStore((s) => s.download);
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
    </>
  );
}

/**
 * UpdateChecksumDialogHost 把「校验文件拉不到，仍要继续吗」这张确认对话挂在应用根上。
 *
 * 它不能留在本节里：下载也可以从状态栏的更新面板发起，那时设置页根本没被渲染，
 * 对话连同「仍要继续」一起消失，用户只会看到下载莫名其妙地退回去。store 是唯一
 * 真相，这张对话也只该有一处。
 */

export function UpdateChecksumDialogHost() {
  const prompt = useUpdateStore((s) => s.checksumPrompt);
  const dismiss = useUpdateStore((s) => s.dismissChecksumPrompt);
  const download = useUpdateStore((s) => s.download);

  const handleConfirm = React.useCallback(() => {
    dismiss();
    void download(true);
  }, [dismiss, download]);

  return (
    <ChecksumDialog
      open={prompt.open}
      reason={prompt.reason}
      onCancel={dismiss}
      onConfirm={handleConfirm}
    />
  );
}
