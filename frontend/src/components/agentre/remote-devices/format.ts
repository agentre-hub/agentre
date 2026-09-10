import type { TFunction } from "i18next";

import { formatRelativeTime } from "@agentre-hub/agentre-ui";

// frontend/src/components/agentre/remote-devices/format.ts
/**
 * 档位阶梯与文案都在共享包里（`formatRelativeTime`）。设备列表要的是「刚刚」这一
 * 档：秒级精度对「上次见到这台机器」没有意义。
 */
export function relativeTime(
  thenMs: number,
  nowMs: number,
  t: TFunction,
): string {
  return formatRelativeTime(thenMs, nowMs, t);
}

const IP_RE = /^\d{1,3}(\.\d{1,3}){3}$/;

export function deriveDeviceName(
  url: string,
  existing: Array<{ name: string }>,
): string {
  try {
    const u = new URL(url);
    const host = u.hostname;
    if (!host) return "";
    if (IP_RE.test(host)) {
      let n = 1;
      const used = new Set(
        existing.map((d) => d.name).filter((n) => /^agentred-\d+$/.test(n)),
      );
      while (used.has(`agentred-${n}`)) n++;
      return `agentred-${n}`;
    }
    return host.split(".")[0] || host;
  } catch {
    return "";
  }
}

export function friendlyLastError(le: string, t: TFunction): string {
  if (!le) return "";
  if (le === "tofu_mismatch") return t("remoteDevices.errors.tofuMismatch");
  if (le === "unauthorized") return t("remoteDevices.errors.unauthorized");
  // agentred 是用 `make agentred-deploy` 单独推到远端机器的,版本歪斜是常态。
  // 「不认识这套协议」与「版本对不上」的处置一样(重装远端 agentred),但原因
  // 不同,所以各说各的。
  if (le === "protocol_unsupported")
    return t("remoteDevices.errors.protocolUnsupported");
  if (le === "protocol_mismatch")
    return t("remoteDevices.errors.protocolMismatch");
  if (le.startsWith("dial_failed:"))
    return t("remoteDevices.errors.dialFailed", {
      message: le.slice("dial_failed:".length),
    });
  return le;
}

/** Formats a whole-second countdown as M:SS, clamped at 0:00. */
export function formatCountdown(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const m = Math.floor(s / 60);
  const sec = s % 60;
  return `${m}:${String(sec).padStart(2, "0")}`;
}

/** Extracts host[:port] from a URL for display; falls back to the raw input. */
export function hostOf(url: string): string {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
}

// server_svc's login/poll bindings return raw Go error strings (not
// i18n.NewError-wrapped — see internal/app/server.go), so the frontend maps
// the known server_svc sentinels to translated copy and falls back to the
// raw message for anything else, mirroring friendlyLastError above.
export function friendlyLoginError(e: unknown, t: TFunction): string {
  const msg = e instanceof Error ? e.message : typeof e === "string" ? e : "";
  switch (msg) {
    case "server: unreachable":
      return t("remoteDevices.login.errors.unreachable");
    case "server: access denied":
      return t("remoteDevices.login.errors.accessDenied");
    case "server: device code expired":
      return t("remoteDevices.login.errors.expired");
    case "server: login already in progress":
      return t("remoteDevices.login.errors.inProgress");
    default:
      return msg || t("remoteDevices.login.errors.generic");
  }
}
