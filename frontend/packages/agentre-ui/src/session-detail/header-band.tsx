import * as React from "react";

import { cn } from "../lib/utils";

/**
 * 一条对话的顶带 —— **四态同一副外壳**。
 *
 * 「已经开着的会话 / 还没发第一句 / 正在加载 / 加载失败」在两端都是同一条 68px 的
 * 带：一格头像、两行标题的高度、一行 mono meta，右端一组控件。高度写死、内容整块
 * 垂直居中，所以标题长短、身份认不认得出、第一句发没发出去，都不再顶动它下面的
 * 转录（桌面端规格 2026-08-23 决策 2/3）。
 *
 * 抽进包里是因为两端各写了一份而**只有一端**做到了这条：控制台把「还没发第一句」
 * 做成了另一副头（`py-3` + 24px 头像），发出第一句那一瞬整条带换成详情那副 68px
 * 的，头像 24→32、转录整体上跳。
 *
 * 只承载**形状**：meta 各段说什么、右端摆什么，由宿主自己算好递进来 —— 状态点、
 * 停止、更多操作、连接指示各自的来路都在宿主那边，包里不认得它们。
 */

/** 各段之间的分隔符。是排版符号不是文案，因此不进 i18n。 */
const META_SEPARATOR = "·";

export type SessionHeaderMetaPart = {
  /** 这一段是哪一维。也是推 `data-testid` 用的后缀。 */
  key: string;
  node: React.ReactNode;
  /**
   * 窄档先收哪一段：一个容器查询类名（如 `@max-[560px]/header:hidden`）。
   * 收起的那一段必须在别处还说得出 —— 这一行不是它唯一的出处。
   */
  hideAt?: string;
  /** 显式的 testId。不给就按 `${metaTestId}-${key}` 推。 */
  testId?: string;
};

export type SessionHeaderBandProps = {
  /**
   * 身份那一格（32px）。四态都要占住它 —— 加载中 / 加载失败也给一枚占位方块，
   * 否则标题会横向跳一格。
   */
  avatar: React.ReactNode;
  /** 头像之前的那一格（窄屏的返回）。不给就不占位。 */
  leading?: React.ReactNode;
  /**
   * 标题。宿主自己把「未命名 / 还没发第一句 / 加载中 / 打不开」折成一句话。
   * 不给就整行不画：控制台的路由页形态里，标题由壳的顶栏呈现。
   */
  title?: string;
  /** mono meta 行的各段。一段都没有时这一行不画。 */
  meta?: SessionHeaderMetaPart[];
  /** 右端那一组控件（宿主自带语义，例如桌面端的 `role="toolbar"`）。 */
  actions?: React.ReactNode;
  className?: string;
  testId?: string;
  metaTestId?: string;
};

export function SessionHeaderBand({
  avatar,
  leading,
  title,
  meta,
  actions,
  className,
  testId,
  metaTestId,
}: SessionHeaderBandProps) {
  const parts = meta ?? [];

  return (
    <div
      data-testid={testId}
      // @container/header：窄档按**这一带的实际宽度**逐段收起，而不是靠
      // flex-wrap 折行把头部撑高。
      className={cn(
        "@container/header flex h-[68px] shrink-0 items-center gap-3",
        className,
      )}
    >
      {leading}
      {avatar}
      <div className="min-w-0 flex-1">
        {title ? (
          <h2
            className="line-clamp-2 break-words text-sm font-semibold leading-snug"
            title={title}
          >
            {title}
          </h2>
        ) : null}
        {parts.length > 0 ? (
          <div
            data-testid={metaTestId}
            className="mt-0.5 flex min-w-0 items-center gap-x-1.5 overflow-hidden font-mono text-2xs whitespace-nowrap text-muted-foreground"
          >
            {/* 分隔符夹在**真的存在的**相邻两段之间：还没跑过第一轮的会话没有状态、
                也没有活动时间，逐段各自带一个前置「·」会在行首留下一个孤零零的
                分隔符。 */}
            {parts.map((part, index) => (
              <span
                key={part.key}
                data-part={part.key}
                data-testid={
                  part.testId ??
                  (metaTestId ? `${metaTestId}-${part.key}` : undefined)
                }
                className={cn(
                  "inline-flex min-w-0 items-center gap-1.5",
                  part.hideAt,
                )}
              >
                {index > 0 ? (
                  <span aria-hidden="true" className="text-border-strong">
                    {META_SEPARATOR}
                  </span>
                ) : null}
                {part.node}
              </span>
            ))}
          </div>
        ) : null}
      </div>
      {actions}
    </div>
  );
}
