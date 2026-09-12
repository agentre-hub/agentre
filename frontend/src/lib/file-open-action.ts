import type { FileOpenAction } from "@/stores/file-settings-store";

/** 单击一个文件行实际会发生什么。`none` = 这一行没有任何一端能打开它。 */
export type RowOpenAction = "preview" | "external" | "none";

type Input = {
  /** 用户在「设置 → 常规 → 文件」里选的去向。 */
  openAction: FileOpenAction;
  /** 会话级预览路径；null = 扩展名不在 allowlist 内，或落在 cwd 之外。 */
  previewPath: string | null;
  /** 能交给本机系统的绝对路径；null = 远端会话，或会话没有 cwd。 */
  absPath: string | null;
};

/**
 * resolveRowOpenAction 是「单击这一行会去哪」唯一的判定处。它把三件事收在一起：
 *
 * - allowlist 外的文件没有第二个去向可选——内置预览**根本渲染不了**它们，所以
 *   一律交给外部应用，与设置无关（spec 决策 2）。
 * - 选了外部应用但这一行拼不出绝对路径（远端会话 / 无 cwd）时退回预览，而不是
 *   报错或禁用（spec 决策 6）。
 * - 两者都不可用时返回 `none`，调用方据此把行渲染成不可交互——这条退回发生在
 *   渲染时，用户不会点到一个注定失败的目标。
 */
export function resolveRowOpenAction({
  openAction,
  previewPath,
  absPath,
}: Input): RowOpenAction {
  if (previewPath === null) return absPath === null ? "none" : "external";
  if (openAction === "external" && absPath !== null) return "external";
  return "preview";
}
