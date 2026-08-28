import * as React from "react";

import { cn } from "../lib/utils";

/**
 * 等转录时的骨架（规格 2026-08-21 决策 2、2026-08-23 决策 9）。
 *
 * 它取代的是一行「正在加载转录…」灰字。那行字与当时头上那条红色的「连接中…」
 * 横幅说的是同一件事，一红一灰摆在一起；而且它不占位置——内容落地时整块版面
 * 往下一跳。骨架两样都解决：既是「在动」的记号，又把行的位置先占住。
 *
 * 对读屏隐藏：连接状态由头部芯片播报（role=status），「下面还会变」由滚动带上的
 * `aria-busy` 说，这里再念一遍几条灰条只是噪音。
 *
 * 实现以 `agentre-server` 的 `components/session/TranscriptSkeleton` 为准迁入。
 * 桌面端此前是四条静止的 `bg-muted`：**没有** `animate-pulse`、**没有**
 * `motion-reduce`，浅色下压在 `--background` 上几乎不显影，静止的灰块读起来像
 * 渲染坏了。
 */
const ROWS = [
  { self: false, w: "w-[66%]", h: "h-11" },
  { self: true, w: "w-[44%]", h: "h-8" },
  { self: false, w: "w-[78%]", h: "h-14" },
  { self: true, w: "w-[38%]", h: "h-8" },
];

export type TranscriptSkeletonProps = React.ComponentProps<"div">;

export function TranscriptSkeleton({
  className,
  ...props
}: TranscriptSkeletonProps) {
  return (
    <div
      data-testid="transcript-skeleton"
      aria-hidden="true"
      className={cn("flex flex-col gap-3", className)}
      {...props}
    >
      {ROWS.map((row, i) => (
        <div
          key={i}
          // 逐条淡下去：最下面那条最浅，读起来是「还在往下长」而不是「就这些」。
          style={{ opacity: 1 - i * 0.22 }}
          className={cn(
            "animate-pulse rounded-lg bg-secondary motion-reduce:animate-none",
            row.w,
            row.h,
            row.self ? "self-end" : "self-start",
          )}
        />
      ))}
    </div>
  );
}
