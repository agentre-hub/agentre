import * as React from "react";

import { cn } from "../lib/utils";

/**
 * 索引里那些**没有身份**的方块，以及首字的取法。
 *
 * 有身份的那一枚不在这里 —— 它是 `../ui/agent-avatar` 的 `AgentAvatar`，两端共用
 * 同一枚记号（规格 2026-08-21「身份字形归一」）。这里只剩「这一维根本不存在」时
 * 的占位：槽位还占着（行的左缘要对齐），但不上色、不给名字，读屏不会把它念成
 * 某个 Agent 或某个项目。
 */
const NEUTRAL_GLYPH_CLASS_NAME =
  "inline-flex size-3.5 shrink-0 items-center justify-center rounded-sm bg-secondary text-[8px] font-semibold text-secondary-foreground";

export type NeutralGlyphProps = {
  className?: string;
  testId?: string;
  children?: React.ReactNode;
};

export function NeutralGlyph({
  className,
  testId,
  children,
}: NeutralGlyphProps) {
  return (
    <span
      data-testid={testId}
      aria-hidden="true"
      className={cn(NEUTRAL_GLYPH_CLASS_NAME, className)}
    >
      {children}
    </span>
  );
}

/** 名字的首字（Agent 大写，项目照原样）—— 名字为空时不编一个字母出来。 */
export function initialOf(name: string, uppercase = false): string | undefined {
  const initial = name.trim().charAt(0);
  if (!initial) return undefined;
  return uppercase ? initial.toUpperCase() : initial;
}
