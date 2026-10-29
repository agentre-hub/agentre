// agrctl.ts — 「这条 shell 调用是被 agrctl send --wait 升级成的子代理卡」的唯一判据。
//
// 升级发生在服务端(把父轮里开着的那条 shell 调用原地挂上 agrctl 来源的 overlay)，
// 工具名与入参都还是 shell 的，canonical 也可能缺(live)或是命令类(未覆盖)。所以卡片类型
// 不能只看 canonical.kind，要认 overlay 的来源。
import type { TranscriptBlock } from "../dto";

export const AGRCTL_ORIGIN = "agrctl";

export function isAgrctlSpawn(block: {
  subagent?: Pick<NonNullable<TranscriptBlock["subagent"]>, "origin">;
}): boolean {
  return block.subagent?.origin === AGRCTL_ORIGIN;
}
