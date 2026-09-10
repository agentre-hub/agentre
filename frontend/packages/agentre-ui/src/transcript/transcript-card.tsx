import * as React from "react";

import { cn } from "../lib/utils";

// 对话流卡片原语。此前 12 个卡片各写各的:3 种圆角、2 套阴影策略、4 种内边距,
// 相邻卡片互相打架。这里是这些样式的唯一定义处 —— 改样式只改这个文件。
export type TranscriptCardTone = "default" | "error" | "pending" | "done";

const cardToneClass: Record<TranscriptCardTone, string> = {
  default: "border-border",
  error: "border-status-error/40",
  pending: "border-primary",
  done: "border-status-running/50",
};

const pillToneClass: Record<TranscriptCardTone, string> = {
  default: "bg-muted text-muted-foreground",
  error: "bg-destructive-soft text-status-error",
  pending: "bg-status-waiting-bg text-status-waiting",
  done: "bg-status-running-bg text-status-running",
};

function TranscriptCard({
  tone = "default",
  className,
  ...props
}: React.ComponentProps<"section"> & { tone?: TranscriptCardTone }) {
  return (
    <section
      {...props}
      className={cn(
        "w-full max-w-measure overflow-hidden rounded-lg border bg-card",
        cardToneClass[tone],
        className,
      )}
    />
  );
}

function TranscriptCardHeader({
  className,
  ...props
}: React.ComponentProps<"button">) {
  return (
    <button
      type="button"
      {...props}
      className={cn(
        "flex w-full min-w-0 cursor-pointer items-center gap-2 px-3.5 py-2.5 text-left transition-colors hover:bg-muted/40",
        className,
      )}
    />
  );
}

function TranscriptCardBody({
  className,
  ...props
}: React.ComponentProps<"div">) {
  return (
    <div
      {...props}
      className={cn("border-t border-border px-3.5 py-3", className)}
    />
  );
}

function TranscriptPill({
  tone = "default",
  className,
  ...props
}: React.ComponentProps<"span"> & { tone?: TranscriptCardTone }) {
  return (
    <span
      {...props}
      className={cn(
        "inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-meta font-medium",
        pillToneClass[tone],
        className,
      )}
    />
  );
}

export {
  TranscriptCard,
  TranscriptCardBody,
  TranscriptCardHeader,
  TranscriptPill,
};
