export type IssueLabelTone =
  | "auth"
  | "bug"
  | "critical"
  | "docs"
  | "feature"
  | "hook"
  | "ops"
  | "perf"
  | "refactor"
  | "ui";

/**
 * 每一档都是「软底 + 该底上可读的文字色」一对 token。文字尺寸是 text-2xs(11px),
 * 按 WCAG AA 属正文,门槛 4.5——所以文字用的一律是各家族的 `-text` 角色,而不是
 * 那个当点/当填充的饱和值(见 docs/design.md §3.5「每个状态最多四种角色」)。
 * 由 __tests__/issue-tones.test.ts 逐档实测把关。
 */
export const labelToneClassNames: Record<IssueLabelTone, string> = {
  auth: "bg-tone-blue-bg text-tone-blue-text",
  bug: "bg-destructive-soft text-destructive-text",
  critical: "bg-destructive text-destructive-foreground",
  docs: "bg-secondary text-secondary-foreground",
  feature: "bg-status-running-bg text-status-running-text",
  hook: "bg-primary-soft text-primary-text",
  ops: "bg-secondary text-secondary-foreground",
  perf: "bg-status-waiting-bg text-status-waiting-text",
  refactor: "bg-primary-soft text-primary-text",
  ui: "bg-tone-violet-bg text-tone-violet-text",
};

export function toneClass(tone: string): string {
  return (
    labelToneClassNames[tone as IssueLabelTone] ??
    "bg-secondary text-secondary-foreground"
  );
}
