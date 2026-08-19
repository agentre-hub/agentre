import * as React from "react";

import { tokenToCssColor } from "../lib/agent-color";
import { cn } from "../lib/utils";

/**
 * 索引里那一枚 14px 的身份方块：Agent 与项目共用（决策 4/8）。**包内私有** ——
 * 对外只出 `ProjectGlyph` 与两个行槽位，因为「这一维怎么画」是它们的实现细节。
 *
 * 上色走 `tokenToCssColor`（token → css 变量）而不是 `bg-agent-7` 这类类名：
 * 类名要靠宿主的 Tailwind 扫到包源码才生成得出来，消费方少配一条 content 路径，
 * 字形就静默变成透明；css 变量随 tokens.css 一起进包，两端一定拿得到。
 *
 * 这一维**解析不出来时不编身份**：不上色、不给名字、只留一个中性方块 —— 槽位还
 * 占着（行的左缘要对齐），但读屏不会把它念成某个 Agent 或某个项目。
 */
const GLYPH_CLASS_NAME =
  "inline-flex size-3.5 shrink-0 items-center justify-center rounded-sm text-[8px] font-semibold text-agent-foreground";

export type GlyphProps = {
  /** 颜色 token（"agent-1"…"agent-16" / "neutral"）；上不了色就给中性面。 */
  color?: string;
  /** 无障碍名。给了才是一个 role="img" 的身份，不给就是纯装饰。 */
  label?: string;
  className?: string;
  testId?: string;
  children?: React.ReactNode;
};

export function Glyph({
  color,
  label,
  className,
  testId,
  children,
}: GlyphProps) {
  const css = tokenToCssColor(color);

  return (
    <span
      data-testid={testId}
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      className={cn(
        GLYPH_CLASS_NAME,
        // 上不了色（这一维缺席 / token 非法）时给中性面。写成 `css || "bg-..."`
        // 会把 "var(--agent-11)" 本身当成一个类名塞进 class 列表。
        !css && "bg-secondary",
        className,
      )}
      style={css ? { backgroundColor: css } : undefined}
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
