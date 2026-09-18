import * as React from "react";

import { tokenToCssColor } from "../lib/agent-color";
import { cn } from "../lib/utils";

/**
 * 身份方块：Agent、项目、任何「一个有名字有颜色的东西」在界面上的**唯一**那一枚记号
 * （规格 2026-08-21「身份字形归一」）。
 *
 * 实现由桌面端的 `src/components/agentre/primitives.tsx` 迁入，**以它为准**：
 * 三档身份（上传头像 ▸ 图标 ▸ 首字母）、四档尺寸、首字母算法一律照搬。此前这一枚
 * 记号有三份实现（桌面端的 `AgentAvatar`、包内私有的 `Glyph`、agentre-server 的
 * `AgentGlyph`），三份的兜底规则各不相同 —— 同一个 Agent 在三处长成三个样子。
 *
 * **图标不在这里解**：把 icon key 换成图标的那张注册表是宿主的产品决定
 * （桌面端 383 行的 icon-registry，server 根本没有），所以这里收的是已经画好的
 * `icon` 节点，不是 key。
 *
 * 上色走 `tokenToCssColor` 的内联 css 变量而不是 `bg-agent-*` 类名：类名要靠宿主的
 * Tailwind 扫到包源码才生成得出来，消费方少配一条 content 路径字形就静默变透明；
 * css 变量随 tokens.css 一起进包，两端一定拿得到。值与桌面端的类名同源
 * （`--agent-foreground: #ffffff` = `text-white`）。
 */

export type AgentAvatarSize = "xs" | "sm" | "md" | "lg";

/**
 * 四档尺寸。`xs` 是 20px 以下那一档（会话行 14px、组织行 18px、归属选择 20px，
 * 后两处靠 `className` 放大）：这一档只画一个字，见 `glyphText`。
 * 圆角取边长的 1/4 左右，小方块不被四个角吃掉。
 */
const sizeClassNames: Record<AgentAvatarSize, string> = {
  xs: "size-3.5 rounded-[3.5px] text-[9px]",
  sm: "size-6 rounded-md text-2xs",
  md: "size-8 rounded-lg text-sm",
  lg: "size-10 rounded-lg text-sm",
};

/**
 * 名字 → 首字母。拉丁多词名取前两词首字母并大写（`Code Reviewer` → `CR`），
 * 其余取首字；名字为空时给 `?`，**不编一个身份出来**（可及名同时为空）。
 *
 * 「拼两个」的条件是**两个词都**拉丁/数字开头，不是只看第一个词：`CEO 助手`
 * 拼出来的 `C助` 是一个半角加一个全角，在最小的方块里放不下会折行。
 */
export function getAgentInitials(name: string): string {
  const trimmed = name.trim();

  if (!trimmed) {
    return "?";
  }

  const pair = trimmed.split(/\s+/).slice(0, 2);
  if (pair.length > 1 && pair.every((part) => /^[a-z0-9]/i.test(part))) {
    return pair
      .map((part) => part[0])
      .join("")
      .toUpperCase();
  }

  return trimmed.slice(0, 1).toUpperCase();
}

/**
 * 方块里画的字。`xs` 只放一个字：两个字母在 14–20px 里量出来占宽 78%–111%，
 * `MW` 直接撑出方块。调用方显式给的 `initials` 原样画，不替它截断。
 */
function glyphText(
  name: string,
  initials: string | undefined,
  size: AgentAvatarSize,
): string {
  if (initials !== undefined) return initials;
  const auto = getAgentInitials(name);
  return size === "xs" ? auto.slice(0, 1) : auto;
}

export type AgentAvatarProps = Omit<React.ComponentProps<"span">, "color"> & {
  /** 无障碍名。空串 = 没有身份可言，可及名跟着为空。 */
  name: string;
  /** 覆盖首字母。项目字形要的是原样首字（不大写），所以这一层不替调用方大写。 */
  initials?: string;
  /** 颜色 token（`agent-1`…`agent-16` / `neutral`）。 */
  color?: string;
  size?: AgentAvatarSize;
  /** 上传的头像图片；给了就整块换成它，不再上底色。 */
  avatarDataUrl?: string;
  /** 宿主图标注册表解出来的那枚图标。 */
  icon?: React.ReactNode;
  testId?: string;
};

export function AgentAvatar({
  className,
  color,
  initials,
  name,
  size = "md",
  avatarDataUrl,
  icon,
  testId,
  ...props
}: AgentAvatarProps) {
  if (avatarDataUrl) {
    return (
      <span
        role="img"
        aria-label={name}
        data-testid={testId}
        className={cn(
          "inline-flex shrink-0 items-center justify-center overflow-hidden bg-muted",
          sizeClassNames[size],
          className,
        )}
        {...props}
      >
        {/*
          外层已经带了可及名，内层的 alt 留空当装饰 —— 两处都报同一个名字会让
          读屏把一枚头像念两遍，也会让 getByRole("img", { name }) 撞见两个元素。
        */}
        <img
          src={avatarDataUrl}
          alt=""
          className="size-full object-cover"
          draggable={false}
        />
      </span>
    );
  }

  // 颜色**缺失**（空 / 未给）退回 agent-1；`neutral` 与任何解析不出的 token 走中性面
  // （规格决策 5）。`neutral` 是调色板里用户能选的正当灰，不是「没有颜色」——
  // 把它一并退成 agent-1 会让用户选的灰渲染成蓝。
  const css = tokenToCssColor(color || "agent-1");
  const text = glyphText(name, initials, size);

  return (
    <span
      role="img"
      aria-label={name}
      data-testid={testId}
      className={cn(
        "inline-flex shrink-0 items-center justify-center font-semibold",
        // 两个字母收一点字距，别顶到方块两边。
        !icon && text.length > 1 && "tracking-tight",
        // 中性面要跟着换前景色：白字落在浅色的 secondary 面上读不出来。
        css
          ? "text-agent-foreground"
          : "bg-secondary text-secondary-foreground",
        sizeClassNames[size],
        className,
      )}
      style={css ? { backgroundColor: css } : undefined}
      {...props}
    >
      {icon ?? text}
    </span>
  );
}
