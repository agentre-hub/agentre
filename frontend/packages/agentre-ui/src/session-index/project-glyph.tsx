import { MessagesSquare } from "lucide-react";

import { iconNode } from "../org/icon-registry";
import { AgentAvatar } from "../ui/agent-avatar";
import { NeutralGlyph, initialOf } from "./glyph";

/**
 * 「项目」这一维在索引里的**唯一**字形。
 *
 * 项目组头（24px）、「按 Agent」档的行首 14px 槽位（决策 4）、「按时间」档第二行的
 * 项目那半（决策 5）是同一个项目的三种尺寸，必须是同一枚字形。此前行里画的是一枚
 * 通用文件夹 —— 同一个项目在组头上是「橙色方块 · 火箭」、在行里是「橙色文件夹」，
 * 既认不出是同一个项目，也认不出是三个项目里的哪一个。
 *
 * 自由会话（`project == null`）给中性面 + 「随手对话」那枚 MessagesSquare：
 * 槽位保留、字形置灰（决策 4），且与它在项目轴上的组头同源（决策 6/7）。
 *
 * 有身份的那一枚就是 `AgentAvatar`（规格 2026-08-21「身份字形归一」）：项目与 Agent
 * 共用同一枚记号，只是喂进去的身份不同。
 *
 * 项目自己选的图标（`ProjectNode.icon`）就在这里解：词表与「key → 图标节点」都在包里
 * （`org/icon-registry` 的 `iconNode`），宿主只管把 key 带着走。此前这里是一个
 * `glyph` 插槽 —— 词表已经归包、解 key 却留在宿主，于是桌面端填了、agentre-server
 * 没填，同一个项目在控制台里永远只是一个字母。词表不认得的 key 退回项目名首字：
 * 猜一枚图标比一个字母更糟。
 */

/** 画一个项目字形要且只要这两件事（`ProjectNode` 结构上满足它）。 */
export type ProjectGlyphInfo = {
  name: string;
  /** 颜色 token，如 "agent-1"；不是调色板 token 时退回中性面。 */
  color?: string;
  /** 图标词表的 key，如 "rocket"；空 / 表里没有时退回项目名首字。 */
  icon?: string;
};

export type ProjectGlyphProps = {
  /** `null` / `undefined` = 不属于任何项目。 */
  project?: ProjectGlyphInfo | null;
  /** 尺寸与圆角由调用方给（行里 14px、子项目 16px、组头 24px）。 */
  className?: string;
  testId?: string;
};

export function ProjectGlyph({
  project,
  className,
  testId,
}: ProjectGlyphProps) {
  if (!project) {
    return (
      <NeutralGlyph testId={testId} className={className}>
        <MessagesSquare className="size-[60%] text-decorative-foreground" />
      </NeutralGlyph>
    );
  }

  return (
    <AgentAvatar
      testId={testId}
      name={project.name}
      // 项目字形要的是**原样**首字（不大写），所以这一层自己给 initials，
      // 不让 AgentAvatar 走它给 Agent 用的那套大写规则。
      initials={initialOf(project.name)}
      color={project.color}
      icon={iconNode(project.icon)}
      size="xs"
      className={className}
    />
  );
}
