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

export const labelToneClassNames: Record<IssueLabelTone, string> = {
  auth: "bg-agent-1/10 text-agent-1",
  bug: "bg-destructive-soft text-destructive",
  critical: "bg-destructive text-destructive-foreground",
  docs: "bg-secondary text-muted-foreground",
  feature: "bg-status-running-bg text-status-running-text",
  hook: "bg-primary-soft text-primary-text",
  ops: "bg-secondary text-muted-foreground",
  perf: "bg-status-waiting-bg text-status-waiting-text",
  refactor: "bg-primary-soft text-primary-text",
  ui: "bg-agent-2/10 text-agent-2",
};

export function toneClass(tone: string): string {
  return (
    labelToneClassNames[tone as IssueLabelTone] ??
    "bg-secondary text-muted-foreground"
  );
}
