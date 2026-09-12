import * as React from "react";

import { cn } from "../lib/utils";

/**
 * 骨架里的一条占位。
 *
 * 它是两端**唯一**的一份：`TranscriptSkeleton`、`BoardSkeleton`、桌面端的面板反馈
 * 与执行目标列表、以及 agentre-server 的总览 / 隐私 / 账户 / 设备 / 组织，全部长在
 * 这上面。此前这九处各内联一遍同样的类，其中桌面端那两处已经分叉成 `bg-muted`。
 *
 * 三件事焊在这里，不留给调用方每次重记：
 *
 *  1. **取色是 `bg-secondary`**。`bg-muted` 在浅色下压在 `--background` 上几乎不
 *     显影，静止的灰块读起来像渲染坏了而不像占位。
 *  2. **它在动，且尊重 `prefers-reduced-motion`**。「在动」是这块区域还活着的记号；
 *     前庭敏感的人那里它停成一条静止的浅色线——记号还在，动效没了。
 *  3. **默认对读屏隐藏**。「正在取」该由容器上的 `aria-busy` 说一次，几条灰条再
 *     念一遍只是噪音。
 *
 * 尺寸、圆角、面都由调用方给：`cn` 走 tailwind-merge，后来者赢。看板那张卡片占位
 * 要的是卡面加描边，照样长在这上面——盖不掉默认层的话，调用方只能绕开它自己拼一遍，
 * 副本正是这么长回来的。
 */
export type SkeletonProps = React.ComponentProps<"span">;

export function Skeleton({ className, ...props }: SkeletonProps) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "block animate-pulse rounded-sm bg-secondary motion-reduce:animate-none",
        className,
      )}
      {...props}
    />
  );
}
