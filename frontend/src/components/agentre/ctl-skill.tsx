import * as React from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Badge, Button } from "@agentre-hub/agentre-ui";

import {
  GetCtlSkillStatus,
  InstallCtlSkill,
  UninstallCtlSkill,
} from "../../../wailsjs/go/app/App";
import type { ctlskill } from "../../../wailsjs/go/models";

// 通用目录 ~/.agents/skills/agrctl 的读取方一览，来自 spec 的既有事实核实
// （Problem 5）。产品名是专有名词，不进 i18n。
const UNIVERSAL_SKILL_HOSTS = [
  "Pi",
  "Codex",
  "OpenCode",
  "Cursor",
  "Copilot",
  "Windsurf",
  "Gemini CLI",
  "Cline",
  "Warp",
  "Rovo Dev",
  "Amp",
  "DeepSeek Harness",
] as const;

type PendingAction = "install" | "uninstall" | null;

/**
 * CtlSkillPanel 呈现 ctl 控制通道技能包的两种落地形态（Claude Code 插件 / 通用
 * Agent Skill 目录）各自的安装态与路径，以及通用目录会让哪些宿主受益的只读名单。
 * 安装与卸载都是幂等的单次绑定调用，成功后直接用响应里的最新状态刷新自己——不
 * 二次拉取，`Install`/`Uninstall` 返回的就是刷新后的 DTO。
 */
export function CtlSkillPanel() {
  const { t } = useTranslation();
  const [info, setInfo] = React.useState<ctlskill.Info | null>(null);
  const [pending, setPending] = React.useState<PendingAction>(null);

  React.useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const status = await GetCtlSkillStatus();
        if (!cancelled) setInfo(status);
      } catch (err) {
        console.warn("fetch ctl skill status failed", err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // 卸载会把两种形态一起摘掉、安装会把两者一起铺回去（都走同一次绑定调用），
  // 半装只会在失败降级路径上短暂出现；按钮的开关以「两者都装了」为准，否则
  // 半装状态下按钮会显示「卸载」却什么都点不掉。
  const installed = Boolean(info?.pluginInstalled && info?.universalInstalled);

  const handleInstall = React.useCallback(async () => {
    setPending("install");
    try {
      const next = await InstallCtlSkill();
      setInfo(next);
    } catch (err) {
      toast.error(
        err instanceof Error
          ? err.message
          : t("settings.ctlSkill.toast.installFailed"),
      );
    } finally {
      setPending(null);
    }
  }, [t]);

  const handleUninstall = React.useCallback(async () => {
    setPending("uninstall");
    try {
      const next = await UninstallCtlSkill();
      setInfo(next);
    } catch (err) {
      toast.error(
        err instanceof Error
          ? err.message
          : t("settings.ctlSkill.toast.uninstallFailed"),
      );
    } finally {
      setPending(null);
    }
  }, [t]);

  return (
    <section className="overflow-hidden rounded-lg border border-border bg-card">
      <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <h2 className="text-sm font-semibold">
            {t("settings.ctlSkill.sectionTitle")}
          </h2>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t("settings.ctlSkill.sectionDescription")}
          </p>
        </div>
        <Badge
          variant={installed ? "default" : "secondary"}
          className="rounded-sm px-1.5 py-0 font-mono text-2xs font-medium"
        >
          {installed
            ? t("settings.ctlSkill.status.installed")
            : t("settings.ctlSkill.status.notInstalled")}
        </Badge>
      </div>

      <TargetRow
        label={t("settings.ctlSkill.plugin.label")}
        installed={info?.pluginInstalled ?? false}
        path={info?.pluginPath ?? ""}
      />
      <TargetRow
        label={t("settings.ctlSkill.universal.label")}
        installed={info?.universalInstalled ?? false}
        path={info?.universalPath ?? ""}
      />

      <div className="flex flex-col gap-2 border-t border-border p-4">
        <span className="text-xs font-medium">
          {t("settings.ctlSkill.hosts.title")}
        </span>
        <p className="text-2xs leading-relaxed text-muted-foreground">
          {t("settings.ctlSkill.hosts.description")}
        </p>
        <ul
          className="flex flex-wrap gap-1.5"
          aria-label={t("settings.ctlSkill.hosts.title")}
        >
          {UNIVERSAL_SKILL_HOSTS.map((host) => (
            <li key={host}>
              <Badge
                variant="outline"
                className="rounded-sm px-1.5 py-0 text-2xs font-normal"
              >
                {host}
              </Badge>
            </li>
          ))}
        </ul>
      </div>

      <div className="flex items-center gap-2 border-t border-border p-4">
        {installed ? (
          <Button
            type="button"
            variant="outline"
            onClick={handleUninstall}
            disabled={pending !== null}
          >
            {pending === "uninstall" ? (
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
            ) : null}
            {pending === "uninstall"
              ? t("settings.ctlSkill.actions.uninstalling")
              : t("settings.ctlSkill.actions.uninstall")}
          </Button>
        ) : (
          <Button type="button" onClick={handleInstall} disabled={pending !== null}>
            {pending === "install" ? (
              <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
            ) : null}
            {pending === "install"
              ? t("settings.ctlSkill.actions.installing")
              : t("settings.ctlSkill.actions.install")}
          </Button>
        )}
      </div>
    </section>
  );
}

function TargetRow({
  label,
  installed,
  path,
}: {
  label: string;
  installed: boolean;
  path: string;
}) {
  const { t } = useTranslation();

  return (
    <div className="flex flex-col gap-1 border-t border-border px-4 py-3 first:border-t-0 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="text-xs font-medium">{label}</span>
        <span className="truncate font-mono text-2xs text-muted-foreground">
          {path || "—"}
        </span>
      </div>
      <Badge
        variant={installed ? "default" : "secondary"}
        className="w-fit rounded-sm px-1.5 py-0 text-2xs font-medium"
      >
        {installed
          ? t("settings.ctlSkill.status.installed")
          : t("settings.ctlSkill.status.notInstalled")}
      </Badge>
    </div>
  );
}
