import { cn } from "../lib/utils";

import { ISSUE_TONES, type IssueTone } from "./types";

export { ISSUE_TONES };

/**
 * 8 档色调**全部落在既有 token 上**，本轮不新增任何颜色 token —— `tokens.css:176`
 * 那段注释说得很清楚：只有 `--tone-blue-*` 与 `--tone-violet-*` 是为了把十枚标签
 * 分开才单独存在的，其余各档复用 destructive / primary / status 三个家族。写
 * `--tone-red-*` / `--tone-orange-*` 这类没定义的 token，Tailwind 解不出类，标签
 * 会直接没有底色。
 *
 * 文字尺寸是 text-2xs(11px)，按 WCAG AA 属正文、门槛 4.5 —— 所以文字一律取各家族
 * 的 `-text` 角色，而不是那个当点、当填充的饱和值（见 docs/design.md §3.5）。
 *
 * 中性档是这里唯一**不填充**的一档：暗色下 `--secondary` 与 `--popover` 是同一个
 * 字节（#262931），填充的中性标签落进任何弹层都只剩文字。描边不依赖表面色。
 */
export const toneClassNames: Record<IssueTone, string> = {
  gray: "border border-border-strong text-muted-foreground",
  red: "bg-destructive-soft text-destructive-text",
  // 唯一的实心档（最高优先级要压过其余几档的软底）。暗色下必须把底压到 /60：
  // --destructive 在暗色是**浅**红 #f87171，白字压上去只有 2.65；压暗后 5.30。
  // 与 Button / Badge 的 destructive 变体同一处理。
  red_solid:
    "bg-destructive text-destructive-foreground dark:bg-destructive/60",
  amber: "bg-status-waiting-bg text-status-waiting-text",
  green: "bg-status-running-bg text-status-running-text",
  steel: "bg-primary-soft text-primary-text",
  blue: "bg-tone-blue-bg text-tone-blue-text",
  violet: "bg-tone-violet-bg text-tone-violet-text",
};

/**
 * 线上传来一个不认识的色调时退回**中性档**而不是 `bg-secondary`：后者在暗色的
 * 弹层里等于隐身，一个「兜底」把标签画没了比画错还糟。
 *
 * `className` 走 `cn` 合并，调用点要盖掉某一档的底色时不必自己处理 Tailwind 冲突。
 */
export function toneClass(tone?: string, className?: string): string {
  const known = ISSUE_TONES.includes(tone as IssueTone)
    ? (tone as IssueTone)
    : "gray";

  return cn(toneClassNames[known], className);
}
