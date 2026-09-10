// frontend/src/components/agentre/remote-devices/device-row.tsx
import { useState } from "react";
import { Server } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import {
  Badge,
  Button,
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  copyTextWithToast,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import {
  agentredVersionState,
  isProtocolRefusal,
  type AgentredVersionState,
} from "./agentred-version";
import { CommandCard } from "./command-card";
import { DeviceActionMenu, type UpgradeMenuItem } from "./device-action-menu";
import { DeviceProvidersSync } from "./device-providers-sync";
import { relativeTime, friendlyLastError } from "./format";
import { useDeviceUpgrade, type UpgradePhase } from "./use-device-upgrade";
import type { DevicePath, DeviceRowModel } from "./use-remote-devices";

/** 作用在 LAN 配对行(paired_agentreds)上的那组动作。 */
export type DeviceRowActions = {
  /**
   * 「刷新直连」与「TLS 信任」两项只在这一行真的有 LAN 地址时才给得出来:
   * 账号收编来的行(IsRelayOnly)也有配对行,但没有可拨的地址、也没有可信任的直连
   * 端点 —— 给了只会点出一个无意义的失败。不传就不画。
   */
  onRefresh?: () => void;
  onRename: () => void;
  onEditTLS?: () => void;
  onRemove: () => void;
};

type Props = {
  device: DeviceRowModel;
  now: number;
  /** 账号独有的行没有配对行,这组动作无处落脚 —— 不传就不画菜单。 */
  actions?: DeviceRowActions;
  /**
   * 桌面端已知的最新 agentred 发布版本(见 useLatestAgentredVersion)。
   * 不传 / 空串 = 不知道:显示版本但不下判断,绝不借「没有徽标」冒充已是最新(决策 19)。
   */
  latestVersion?: string;
};

function tlsBadgeVariant(
  mode: string,
): "secondary" | "outline" | "destructive" {
  if (mode === "skip-verify") return "destructive";
  if (mode === "pin-cert" || mode === "ca-bundle") return "outline";
  return "secondary";
}

function tlsBadgeLabel(mode: string, t: TFunction): string {
  switch (mode) {
    case "default":
      return t("remoteDevices.tls.modes.default.label");
    case "pin-cert":
      return t("remoteDevices.tls.modes.pinCert.label");
    case "ca-bundle":
      return t("remoteDevices.tls.modes.caBundle.label");
    case "skip-verify":
      return t("remoteDevices.tls.modes.skipVerify.label");
    default:
      return mode;
  }
}

function dotColor(device: DeviceRowModel): string {
  if (device.lan?.lastError === "tofu_mismatch") return "bg-destructive";
  // 协议不匹配不是「暂时不在线」:这台机器可能好好地在跑,只是桌面端已经够不着它,
  // 灰点会把它说成一次普通的掉线(决策 13 把强提示只留给「已经用不了」)。
  if (isProtocolRefusal(device.lan?.lastError ?? "")) return "bg-destructive";
  if (device.online) return "bg-status-running";
  return "bg-muted-foreground";
}

// ── 远端 agentred 的版本 ────────────────────────────────────────────────────
// 版本与徽标都挂在副行(决策 17):标题行已经有状态点、TLS 徽章与路径 chip,再加一枚
// 会把设备名挤没。

/** 副行上的版本文字;开发构建如实说是开发构建(它自称的版本号不可比,见决策 5)。 */
function versionLabel(
  state: AgentredVersionState,
  t: TFunction,
): string | null {
  if (state.kind === "dev-build") return t("remoteDevices.upgrade.devBuild");
  if (state.kind === "unknown") return null;
  return state.version || null;
}

/** 副行上的版本徽标:可升级是弱提示,协议不匹配是强提示,其余什么都不出。 */
function VersionBadge({
  state,
  t,
}: {
  state: AgentredVersionState;
  t: TFunction;
}) {
  if (state.kind === "upgradable") {
    return (
      <Badge
        variant="outline"
        className="border-status-waiting/40 text-status-waiting"
      >
        {t("remoteDevices.upgrade.badge", { version: state.latest })}
      </Badge>
    );
  }
  if (state.kind === "protocol-mismatch") {
    return (
      <Badge variant="destructive">
        {t("remoteDevices.upgrade.blocked.badge")}
      </Badge>
    );
  }
  return null;
}

// 协议不匹配的强提示:一句标题 + 一句事实 + 出口。一键升级在这一态必然够不着
// (握手都没过),命令卡因此是唯一的出口 —— 这也是它必须存在的理由(决策 18)。
function ProtocolTooOld({ t }: { t: TFunction }) {
  return (
    <div className="flex flex-col gap-2 rounded-md border border-destructive/30 bg-destructive/5 p-2.5">
      <div className="text-xs font-semibold text-destructive">
        {t("remoteDevices.upgrade.blocked.title")}
      </div>
      <div className="text-xs text-muted-foreground">
        {t("remoteDevices.upgrade.blocked.body")}
      </div>
      <CommandCard
        label={t("remoteDevices.upgrade.commandLabel")}
        command={t("remoteDevices.upgrade.command")}
      />
    </div>
  );
}

// ── 远程一键升级(spec「远程一键升级」+「桌面端呈现」)──────────────────────
// 触发点是动作菜单里的「升级 agentred」;这里只负责把 useDeviceUpgrade 的状态机
// 翻成菜单项的文案/可用性,以及升级中/成功/超时之后行下方的一句话反馈。

/** 菜单项:已是最新与开发构建保留为禁用态并注明版本(决策 5/20,入口不隐藏)。
 * 活跃轮次拒绝之后不禁用、文案改口(决策 21),真正的拦截交给下面的确认对话框。 */
function upgradeMenuItem(
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
function upgradeOwnsFeedback(phase: UpgradePhase): boolean {
  switch (phase.kind) {
    case "requesting":
    case "upgrading":
    case "success":
    case "timeout":
      return true;
    case "failed":
      return phase.message !== "";
    default:
      // idle 与 active-turns 在这块位置上什么都不画（后者改的是菜单文案）。
      return false;
  }
}

function UpgradeStatus({ phase, t }: { phase: UpgradePhase; t: TFunction }) {
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
  if (phase.kind === "failed" && phase.message) {
    return <div className="text-xs text-muted-foreground">{phase.message}</div>;
  }
  return null;
}

/** 活跃轮次的二次确认(决策 8/21):只有点了这里的「仍然升级」,force=true
 * 才会真的出现在请求里 —— 点菜单项那一下只打开这个对话框。 */
function ActiveTurnsConfirm({
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
          <p className="text-sm text-muted-foreground">{phase.message}</p>
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

// ── R15 可达路径 chip ───────────────────────────────────────────────────────
// 路径信息只在设备面板呈现(R16),不进入聊天界面。失效态除样式(划线/淡出)外
// 另有文字表达;在用态高亮并以 aria-label 说明 —— 均不靠颜色单独传达。
function PathChip({ path, t }: { path: DevicePath; t: TFunction }) {
  const label = t(
    path.kind === "lan" ? "remoteDevices.path.lan" : "remoteDevices.path.relay",
  );
  const inUse = path.state === "in-use";
  const dead = path.state === "dead";
  const aria = inUse
    ? t("remoteDevices.path.inUseAria", { path: label })
    : dead
      ? t("remoteDevices.path.unreachableAria", { path: label })
      : label;
  return (
    <span
      aria-label={aria}
      title={aria}
      className={cn(
        "inline-flex shrink-0 items-center rounded-full border px-2 text-2xs leading-5",
        inUse
          ? "border-primary bg-primary-soft font-semibold text-primary-text"
          : "border-border-strong text-muted-foreground",
        dead && "line-through opacity-60",
      )}
    >
      {dead ? t("remoteDevices.path.unreachableLabel", { path: label }) : label}
    </span>
  );
}

// 未登录行的指引动作:登录是 daemon 侧动作(agentred login),桌面端给的是指引
// 与后果说明。登录后,同一账号下的其它设备才能看到这台机器。
function SignedOutNote({ t }: { t: TFunction }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex flex-col gap-1 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline">{t("remoteDevices.signedOut.label")}</Badge>
        <span className="text-muted-foreground">
          {t("remoteDevices.signedOut.note")}
        </span>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="h-6 px-1.5 text-xs"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          {t("remoteDevices.signedOut.howTo")}
        </Button>
      </div>
      {open ? (
        <div className="text-muted-foreground">
          {t("remoteDevices.signedOut.instructions")}
        </div>
      ) : null}
    </div>
  );
}

export function DeviceRow({ device, now, actions, latestVersion }: Props) {
  const { t } = useTranslation();
  // LAN 配对行。undefined = 这一行只来自账号清单,没有本机配对行可依附。
  const lan = device.lan;
  const friendlyErr = friendlyLastError(lan?.lastError ?? "", t);
  const isTofu = lan?.lastError === "tofu_mismatch";
  const [showProviders, setShowProviders] = useState(false);
  // 版本与短 commit 来自 watcher 最近一次 health.ping(进程内缓存,不落库)。
  const versionState = agentredVersionState({
    version: lan?.daemonVersion ?? "",
    commit: lan?.daemonCommit ?? "",
    lastError: lan?.lastError ?? "",
    latest: latestVersion ?? "",
  });
  const version = versionLabel(versionState, t);
  // deviceId=0 是账号独有行(没有 lan)的占位:菜单里不会画出升级项,状态机因此
  // 永远不会被触发,给一个稳定的非法 id 只是为了满足 Hooks 的固定调用顺序。
  const upgrade = useDeviceUpgrade(lan?.id ?? 0, lan?.daemonVersion ?? "");
  // 可复制的命令跟着升级项一起进菜单,不按状态二选一(决策 18):一键升级够不着的
  // 那些时候它是唯一的出口,而入口时有时无会让人怀疑自己记错了位置。
  const upgradeItem = lan
    ? {
        ...upgradeMenuItem(versionState, upgrade.phase, upgrade, t),
        onCopyCommand: () => {
          void copyTextWithToast(t("remoteDevices.upgrade.command"), {
            successTitle: t("remoteDevices.onboarding.copySuccess"),
          });
        },
      }
    : undefined;

  return (
    <div
      data-testid="device-row"
      className={`flex flex-col gap-1 rounded-md border p-3 ${
        isTofu ? "border-destructive bg-destructive/5" : "border-border bg-card"
      }`}
    >
      <div className="flex items-center gap-3">
        <span
          aria-label={
            device.online
              ? t("remoteDevices.status.online")
              : t("remoteDevices.status.offline")
          }
          className={`h-2 w-2 rounded-full ${dotColor(device)}`}
        />
        <div className="flex h-7 w-7 items-center justify-center rounded-md bg-secondary">
          <Server className="h-4 w-4" />
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="font-medium truncate">{device.name}</span>
            {/* TLS 信任模式是配对行上的字段。 */}
            {lan ? (
              <Badge variant={tlsBadgeVariant(lan.tlsMode)}>
                {tlsBadgeLabel(lan.tlsMode, t)}
              </Badge>
            ) : null}
          </div>
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            {version ? (
              <>
                <span className="font-mono">{version}</span>
                <span>·</span>
              </>
            ) : null}
            <span className="truncate">
              {/* R15:地址位显示 LAN url,或中转路径在用时的「经中转」。账号独有的
                  行没有 LAN 地址,中转就是它唯一的地址形态。 */}
              {lan && !device.viaRelay
                ? lan.url
                : t("remoteDevices.status.viaRelay")}
              <span className="mx-2">·</span>
              {device.lastSeenAt > 0
                ? t("remoteDevices.status.lastConnected", {
                    time: relativeTime(device.lastSeenAt, now, t),
                  })
                : t("remoteDevices.status.neverConnected")}
            </span>
            <VersionBadge state={versionState} t={t} />
          </div>
        </div>
        {device.paths.length > 0 ? (
          <div className="flex shrink-0 items-center gap-1">
            {device.paths.map((p) => (
              <PathChip key={p.kind} path={p} t={t} />
            ))}
          </div>
        ) : null}
        {actions ? (
          <DeviceActionMenu
            onRefresh={actions.onRefresh}
            onRename={actions.onRename}
            onEditTLS={actions.onEditTLS}
            onRemove={actions.onRemove}
            onToggleProviders={() => setShowProviders((s) => !s)}
            upgrade={upgradeItem}
          />
        ) : null}
      </div>
      {device.signedOut ? <SignedOutNote t={t} /> : null}
      {versionState.kind === "protocol-mismatch" ? (
        <ProtocolTooOld t={t} />
      ) : upgradeOwnsFeedback(upgrade.phase) ? (
        <UpgradeStatus phase={upgrade.phase} t={t} />
      ) : friendlyErr ? (
        <div
          className={`text-xs ${isTofu ? "text-destructive" : "text-muted-foreground"}`}
        >
          {friendlyErr}
        </div>
      ) : null}
      {upgrade.phase.kind === "active-turns" ? (
        <ActiveTurnsConfirm phase={upgrade.phase} upgrade={upgrade} t={t} />
      ) : null}
      {showProviders && lan ? <DeviceProvidersSync deviceId={lan.id} /> : null}
    </div>
  );
}
