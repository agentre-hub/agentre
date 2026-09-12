// 远程一键升级整簇:只有它要用 Dialog 与 CommandCard,因此独立于 device-row.tsx。
//
// ── 远程一键升级(spec「远程一键升级」+「桌面端呈现」)──────────────────────
// 触发点是动作菜单里的「升级 agentred」;这里只负责把 useDeviceUpgrade 的状态机
// 翻成菜单项的文案/可用性,以及升级中/成功/超时之后行下方的一句话反馈。

import type { TFunction } from "i18next";

import {
  Button,
  CommandCard,
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agentre-hub/agentre-ui";

import type { AgentredVersionState } from "./agentred-version";
import type { UpgradeMenuItem } from "./device-action-menu";
import { useDeviceUpgrade, type UpgradePhase } from "./use-device-upgrade";

/** 菜单项:已是最新与开发构建保留为禁用态并注明版本(决策 5/20,入口不隐藏)。
 * 活跃轮次拒绝之后不禁用、文案改口(决策 21),真正的拦截交给下面的确认对话框。 */
export function upgradeMenuItem(
  versionState: AgentredVersionState,
  phase: UpgradePhase,
  upgrade: ReturnType<typeof useDeviceUpgrade>,
  t: TFunction,
  // onCopyCommand 由调用点补：它对每一态都一样(始终并列 —— 决策 18),不该在下面
  // 五个分支里各写一遍。
): Omit<UpgradeMenuItem, "onCopyCommand"> {
  if (versionState.kind === "current") {
    return {
      label: t("remoteDevices.upgrade.action.upToDate", {
        version: versionState.version,
      }),
      disabled: true,
      onSelect: () => {},
    };
  }
  if (versionState.kind === "dev-build") {
    return {
      label: t("remoteDevices.upgrade.action.default"),
      disabled: true,
      onSelect: () => {},
    };
  }
  // 调用还在飞与升级已受理,菜单项是同一件事:点不动。差别只在它此刻说什么。
  if (phase.kind === "requesting") {
    return {
      label: t("remoteDevices.upgrade.action.requesting"),
      disabled: true,
      onSelect: () => {},
    };
  }
  if (phase.kind === "upgrading") {
    return {
      label: t("remoteDevices.upgrade.action.upgrading"),
      disabled: true,
      onSelect: () => {},
    };
  }
  if (phase.kind === "active-turns") {
    return {
      label: t("remoteDevices.upgrade.action.forceLabel"),
      disabled: false,
      onSelect: upgrade.requestForce,
    };
  }
  return {
    label: t("remoteDevices.upgrade.action.default"),
    disabled: false,
    badgeVersion:
      versionState.kind === "upgradable" ? versionState.latest : undefined,
    onSelect: upgrade.start,
  };
}

/** 准备中 / 升级中 / 成功 / 超时失败各自的一句话反馈,画在行下方(与协议不匹配的强提示
 * 同一处地方,互斥出现)。活跃轮次的拒绝不在这里呈现 —— 它改的是菜单项文案,
 * 拦截交给下面的确认对话框,不占用这块反馈位。 */
/**
 * 这一次升级此刻有没有话要说。
 *
 * 它决定反馈位归谁：daemon 一受理就重启，watcher 下一次拨号必然失败，行上因此**必然**
 * 带着一条 lastError —— 让 lastError 优先,等于恰好在升级期间把反馈拿掉,而超时那一态
 * 的命令卡正是「它没回来」时唯一的出口。分支与 UpgradeStatus 自己那几条一一对应。
 */
export function upgradeOwnsFeedback(phase: UpgradePhase): boolean {
  switch (phase.kind) {
    case "requesting":
    case "upgrading":
    case "success":
    case "timeout":
      return true;
    case "failed":
      // 认得出原因时就有话可说,message 空不空都不影响。
      return phase.reason !== null || phase.message !== "";
    default:
      // idle 与 active-turns 在这块位置上什么都不画（后者改的是菜单文案）。
      return false;
  }
}

export function UpgradeStatus({
  phase,
  deviceId,
  t,
}: {
  phase: UpgradePhase;
  deviceId: number;
  t: TFunction;
}) {
  if (phase.kind === "requesting") {
    // 这一段能长达几分钟:受理判定在那台机器上把下载与校验都做完了才应答。说清楚
    // 它在做什么,比一个转着的图标更能让人不去点第二次。
    return (
      <div className="flex flex-col gap-1 rounded-md border border-primary/30 bg-primary-soft p-2.5">
        <div className="text-xs font-semibold text-primary-text">
          {t("remoteDevices.upgrade.status.requestingTitle")}
        </div>
        <div className="text-xs text-muted-foreground">
          {t("remoteDevices.upgrade.status.requestingBody")}
        </div>
      </div>
    );
  }
  if (phase.kind === "upgrading") {
    return (
      <div className="flex flex-col gap-1 rounded-md border border-primary/30 bg-primary-soft p-2.5">
        <div className="text-xs font-semibold text-primary-text">
          {t("remoteDevices.upgrade.status.upgradingTitle", {
            version: phase.targetVersion,
          })}
        </div>
        <div className="text-xs text-muted-foreground">
          {t("remoteDevices.upgrade.status.upgradingBody")}
        </div>
      </div>
    );
  }
  if (phase.kind === "success") {
    return (
      <div className="flex flex-col gap-1 rounded-md border border-status-running/30 bg-status-running-bg p-2.5">
        <div className="text-xs font-semibold text-status-running-text">
          {t("remoteDevices.upgrade.status.successTitle")}
        </div>
        <div className="text-xs text-muted-foreground">
          {phase.fromVersion} → {phase.toVersion}
        </div>
      </div>
    );
  }
  if (phase.kind === "timeout") {
    return (
      <div className="flex flex-col gap-2 rounded-md border border-destructive/30 bg-destructive/5 p-2.5">
        <div className="text-xs font-semibold text-destructive">
          {t("remoteDevices.upgrade.status.timeoutTitle")}
        </div>
        <div className="text-xs text-muted-foreground">
          {t("remoteDevices.upgrade.status.timeoutBody")}
        </div>
        <CommandCard
          label={t("remoteDevices.upgrade.commandLabel")}
          command={t("remoteDevices.upgrade.command")}
        />
      </div>
    );
  }
  if (phase.kind === "failed") {
    return <UpgradeFailure phase={phase} deviceId={deviceId} t={t} />;
  }
  return null;
}

/**
 * 一次失败该说什么。
 *
 * 原因是 daemon 给的**结构化枚举**,所以这里说的是人话;`phase.message` 是它那句
 * Go 原话(`cannot replace …: permission denied; re-run with …`),降级成等宽小字的
 * 技术细节——认不出原因时它是唯一剩下的线索,认得出时它只是佐证。桌面端自己的
 * 应用更新面板(update-panel.tsx)对错误详情用的是同一种呈现。
 */
const FAILURE_COPY: Record<string, { title: string; body: string }> = {
  in_progress: {
    title: "remoteDevices.upgrade.failed.inProgressTitle",
    body: "remoteDevices.upgrade.failed.inProgressBody",
  },
  not_writable: {
    title: "remoteDevices.upgrade.failed.notWritableTitle",
    body: "remoteDevices.upgrade.failed.notWritableBody",
  },
  already_latest: {
    title: "remoteDevices.upgrade.failed.alreadyLatestTitle",
    body: "remoteDevices.upgrade.failed.alreadyLatestBody",
  },
  download_failed: {
    title: "remoteDevices.upgrade.failed.downloadFailedTitle",
    body: "remoteDevices.upgrade.failed.downloadFailedBody",
  },
};

function UpgradeFailure({
  phase,
  deviceId,
  t,
}: {
  phase: Extract<UpgradePhase, { kind: "failed" }>;
  deviceId: number;
  t: TFunction;
}) {
  const copy = phase.reason ? FAILURE_COPY[phase.reason] : undefined;
  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-destructive/30 bg-destructive/5 p-2.5">
      <div className="text-xs font-semibold text-destructive">
        {t(copy?.title ?? "remoteDevices.upgrade.failed.unknownTitle")}
      </div>
      {copy ? (
        <div className="text-xs text-muted-foreground">{t(copy.body)}</div>
      ) : null}
      {phase.message ? (
        <div data-selectable-text="true">
          <pre
            data-testid={`device-upgrade-detail-${deviceId}`}
            className="overflow-x-auto whitespace-pre-wrap font-mono text-2xs leading-relaxed text-muted-foreground"
          >
            {phase.message}
          </pre>
        </div>
      ) : null}
    </div>
  );
}

/** 活跃轮次的二次确认(决策 8/21):只有点了这里的「仍然升级」,force=true
 * 才会真的出现在请求里 —— 点菜单项那一下只打开这个对话框。 */
export function ActiveTurnsConfirm({
  phase,
  upgrade,
  t,
}: {
  phase: Extract<UpgradePhase, { kind: "active-turns" }>;
  upgrade: ReturnType<typeof useDeviceUpgrade>;
  t: TFunction;
}) {
  return (
    <Dialog
      open={phase.confirmOpen}
      onOpenChange={(open) => {
        if (!open) upgrade.cancelForce();
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t("remoteDevices.upgrade.confirm.title", {
              count: phase.activeTurns,
            })}
          </DialogTitle>
        </DialogHeader>
        <DialogBody>
          {/* 标题已经用结构化的 activeTurns 说了「还有 N 条在跑」;正文说后果,
              而不是把 daemon 那句英文原话再贴一遍。 */}
          <p className="text-sm text-muted-foreground">
            {t("remoteDevices.upgrade.confirm.body")}
          </p>
        </DialogBody>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={upgrade.cancelForce}>
            {t("remoteDevices.upgrade.confirm.cancel")}
          </Button>
          <Button
            size="sm"
            variant="destructive"
            onClick={upgrade.confirmForce}
          >
            {t("remoteDevices.upgrade.confirm.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
