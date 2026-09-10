// 更新区块用到的常量与格式化:渠道名/说明、仓库地址、版本号与进度的显示形式。
// 纯数据与纯函数,不认识任何组件。

import { type MirrorInfo, type UpdateChannel } from "../update-api";

export const CHANNEL_LABEL: Record<UpdateChannel, string> = {
  stable: "update.channel.stable.label",
  beta: "update.channel.beta.label",
  nightly: "update.channel.nightly.label",
};

export const CHANNEL_DESC: Record<UpdateChannel, string> = {
  stable: "update.channel.stable.description",
  beta: "update.channel.beta.description",
  nightly: "update.channel.nightly.description",
};

export const REPOSITORY_URL = "https://github.com/agentre-hub/agentre";

// MIRROR_CUSTOM_ID select 中"自定义"选项的特殊值；选中时显示 input。

// MIRROR_CUSTOM_ID select 中"自定义"选项的特殊值；选中时显示 input。
export const MIRROR_CUSTOM_ID = "__custom__";

export function formatVersion(v: string, unknownLabel: string): string {
  if (!v) return unknownLabel;
  return v.startsWith("v") ? v : `v${v}`;
}

export function formatProgress(p: number): string {
  if (!Number.isFinite(p) || p <= 0) return "0%";
  if (p >= 100) return "100%";
  return `${p.toFixed(0)}%`;
}

export function pickMirrorOption(
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
