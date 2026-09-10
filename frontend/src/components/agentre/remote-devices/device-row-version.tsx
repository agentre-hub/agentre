// 远端 agentred 的版本簇:版本文字、副行徽标、协议不匹配的强提示。
//
// 原先与整行一起住在 device-row.tsx(615 行),这一簇 60 行是其中「版本 → 呈现」的全部
// 判据 —— 决策 5(开发构建的版本号不可比)/ 17(版本与徽标挂副行)/ 18(协议不匹配时
// 命令卡是唯一出口)的原话都在这三段注释里。
import type { TFunction } from "i18next";

import { Badge, CommandCard } from "@agentre-hub/agentre-ui";

import type { AgentredVersionState } from "./agentred-version";

// ── 远端 agentred 的版本 ────────────────────────────────────────────────────
// 版本与徽标都挂在副行(决策 17):标题行已经有状态点、TLS 徽章与路径 chip,再加一枚
// 会把设备名挤没。

/** 副行上的版本文字;开发构建如实说是开发构建(它自称的版本号不可比,见决策 5)。 */
export function versionLabel(
  state: AgentredVersionState,
  t: TFunction,
): string | null {
  if (state.kind === "dev-build") return t("remoteDevices.upgrade.devBuild");
  if (state.kind === "unknown") return null;
  return state.version || null;
}

/** 副行上的版本徽标:可升级是弱提示,协议不匹配是强提示,其余什么都不出。 */
export function VersionBadge({
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
export function ProtocolTooOld({ t }: { t: TFunction }) {
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
