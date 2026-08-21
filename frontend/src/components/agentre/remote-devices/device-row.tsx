// frontend/src/components/agentre/remote-devices/device-row.tsx
import { useState } from "react";
import { Server } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { DeviceActionMenu } from "./device-action-menu";
import { DeviceProvidersSync } from "./device-providers-sync";
import { relativeTime, friendlyLastError } from "./format";
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
  if (device.online) return "bg-status-running";
  return "bg-muted-foreground";
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

// 未认领行的认领动作:认领是 daemon 侧动作(agentred login),桌面端给的是指引
// 与后果说明。认领后,同一账号下的其它设备才能看到这台机器。
function UnclaimedNote({ t }: { t: TFunction }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex flex-col gap-1 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline">{t("remoteDevices.unclaimed.label")}</Badge>
        <span className="text-muted-foreground">
          {t("remoteDevices.unclaimed.note")}
        </span>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          className="h-6 px-1.5 text-xs"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          {t("remoteDevices.unclaimed.claim")}
        </Button>
      </div>
      {open ? (
        <div className="text-muted-foreground">
          {t("remoteDevices.unclaimed.instructions")}
        </div>
      ) : null}
    </div>
  );
}

export function DeviceRow({ device, now, actions }: Props) {
  const { t } = useTranslation();
  // LAN 配对行。undefined = 这一行只来自账号清单,没有本机配对行可依附。
  const lan = device.lan;
  const friendlyErr = friendlyLastError(lan?.lastError ?? "", t);
  const isTofu = lan?.lastError === "tofu_mismatch";
  const [showProviders, setShowProviders] = useState(false);

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
          <div className="text-xs text-muted-foreground truncate">
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
          />
        ) : null}
      </div>
      {device.unclaimed ? <UnclaimedNote t={t} /> : null}
      {friendlyErr ? (
        <div
          className={`text-xs ${isTofu ? "text-destructive" : "text-muted-foreground"}`}
        >
          {friendlyErr}
        </div>
      ) : null}
      {/* R18：daemon 版本过旧是这台设备的固有属性，说明就落在这台设备这一行。 */}
      {lan?.daemonOutdated ? (
        <div className="text-xs text-status-waiting">
          {t("remoteDevices.status.daemonOutdated")}
        </div>
      ) : null}
      {showProviders && lan ? <DeviceProvidersSync deviceId={lan.id} /> : null}
    </div>
  );
}
