// project-glyph.tsx —— 「项目」这一维在索引里的**唯一**字形。
//
// 项目组头（24px）、「按 Agent」档的行首 14px 槽位（决策 4）、「按时间」档第二行的
// 项目那半（决策 5）是同一个项目的三种尺寸，必须是同一枚字形：项目自己选的图标 +
// 项目色。此前行里画的是一枚通用文件夹 —— 同一个项目在组头上是「橙色方块 · 火箭」、
// 在行里是「橙色文件夹」，既认不出是同一个项目，也认不出是三个项目里的哪一个。
//
// 自由会话（`project === null`）给中性面 + 「随手对话」组头那枚 MessagesSquare：
// 槽位保留、字形置灰（决策 4），且与它在项目轴上的组头同源（决策 6/7）。
import { MessagesSquare } from "lucide-react";

import { cn } from "@/lib/utils";

import { AgentAvatar } from "../primitives";
import type { AgentColor } from "../types";

/** 画一个项目字形要且只要这三件事。 */
export type ProjectGlyphInfo = {
  name: string;
  /** 颜色 token，如 "agent-1"；空串按 agent-1 走。 */
  color: string;
  /** icon-registry 的图标 key；空串 / 不在表里时退回项目名首字母。 */
  icon: string;
};

export type ProjectGlyphProps = {
  /** `null` = 自由会话，不属于任何项目。 */
  project: ProjectGlyphInfo | null;
  /** 尺寸与圆角由调用方给（行里 14px、子项目 16px、组头 24px）。 */
  className?: string;
};

export function ProjectGlyph({ project, className }: ProjectGlyphProps) {
  if (!project) {
    return (
      <span
        aria-hidden="true"
        className={cn(
          "inline-flex items-center justify-center rounded-sm bg-secondary",
          className,
        )}
      >
        <MessagesSquare className="size-[60%] text-decorative-foreground" />
      </span>
    );
  }

  return (
    <AgentAvatar
      name={project.name}
      initials={project.name.trim().charAt(0)}
      color={(project.color as AgentColor) || "agent-1"}
      avatarIcon={project.icon}
      size="sm"
      className={className}
    />
  );
}
