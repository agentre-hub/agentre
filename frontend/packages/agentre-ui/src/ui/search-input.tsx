import * as React from "react";
import { Search } from "lucide-react";

import { cn } from "../lib/utils";

/**
 * 搜索框，两端共用那一份。
 *
 * 合之前它在六处各手搓了一遍（引擎导航栏、模型表工具条、归属选择器、目录选择器、
 * 会话索引、组织索引、新建会话）。每一处都是「绝对定位一枚放大镜 + 给输入框留左
 * 内边距 + 自己写一遍 focus 环」，而六份的 class 各差一点点：有的图标 `size-3.5`
 * 有的 `size-4`，有的 focus 环在输入框上、有的在外层 label 上，深色下的占位符颜色
 * 有两个出处。
 *
 * 差异不是设计意图，只是复制粘贴的漂移 —— 真正有意图的差异只有两条，都收成了 prop：
 *
 * - `variant`：坐在什么表面上。`outline` 是独立字段（带边框），`muted` 是工具条里
 *   的填充块（无边框、`bg-muted`），`bare` 是弹层顶部那一行（连背景都没有，边界由
 *   外面那条 `border-b` 给）。
 * - `size`：`xs` / `sm` / `md` = 28 / 32 / 36px。合之前六处落在四个高度上
 *   （28、30、32、36），30 那两个没有理由，收进这条阶梯。
 *
 * focus 环一律画在**外框**上而不是输入框上：这个控件在视觉上是「图标 + 输入」一整块，
 * 环只圈住输入那半边会在图标旁边裂开一道缝。
 */
export type SearchInputVariant = "outline" | "muted" | "bare";

export interface SearchInputProps extends Omit<
  React.ComponentProps<"input">,
  "type" | "value" | "onChange" | "size"
> {
  value: string;
  /**
   * 直接给值，而不是给事件。
   *
   * 六处调用点里没有一处用得上 event 本身，全都是 `e.target.value` —— 那一层拆包
   * 每处都要写一遍，还留着写成 `e.currentTarget` 还是 `e.target` 的分叉余地。
   */
  onChange: (value: string) => void;
  variant?: SearchInputVariant;
  size?: "xs" | "sm" | "md";
  /** 落在外框上：调用方要控制的通常是宽度与外边距，不是输入框内部。 */
  className?: string;
  inputClassName?: string;
}

const FRAME: Record<SearchInputVariant, string> = {
  outline:
    "rounded-md border border-input bg-transparent focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/50",
  muted:
    "rounded-md bg-muted focus-within:ring-[3px] focus-within:ring-ring/50",
  bare: "bg-transparent",
};

const FRAME_SIZE = {
  xs: "h-7 gap-1.5 px-2",
  sm: "h-8 gap-1.5 px-2.5",
  md: "h-9 gap-2 px-3",
} as const;

const TEXT_SIZE = { xs: "text-2xs", sm: "text-xs", md: "text-sm" } as const;

const ICON_SIZE = { xs: "size-3", sm: "size-3.5", md: "size-4" } as const;

export function SearchInput({
  value,
  onChange,
  variant = "outline",
  size = "sm",
  className,
  inputClassName,
  ...props
}: SearchInputProps) {
  return (
    <div
      data-slot="search-input"
      data-variant={variant}
      className={cn(
        "flex min-w-0 items-center",
        FRAME[variant],
        variant === "bare" ? "gap-1.5" : FRAME_SIZE[size],
        className,
      )}
    >
      <Search
        className={cn(
          "pointer-events-none shrink-0 text-decorative-foreground",
          variant === "bare" ? "size-3.5" : ICON_SIZE[size],
        )}
        aria-hidden="true"
      />
      <input
        type="search"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={cn(
          // 输入框自己不画边框也不画环：那两样都归外框（见文件头）。
          "h-full w-full min-w-0 bg-transparent text-foreground outline-none placeholder:text-muted-foreground",
          // 系统自带的清除按钮在两个宿主上长得不一样，且与右侧的工具按钮撞在一起。
          "[&::-webkit-search-cancel-button]:appearance-none",
          TEXT_SIZE[size],
          inputClassName,
        )}
        {...props}
      />
    </div>
  );
}
