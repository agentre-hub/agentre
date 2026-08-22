import type * as React from "react";

import type { AgentStatus } from "../transcript/agent-status";

import { IndexGroupHeader } from "./group-header";
import { ProjectGlyph } from "./project-glyph";
import type { ProjectGlyphInfo } from "./project-glyph";

/**
 * 「按项目」轴的组头（规格 2026-08-22「组头归一」）。
 *
 * 组头与行里那一维是**同一枚字形**（`ProjectGlyph`），只是尺寸档不同 —— 两处各画
 * 各的，正是「行里是文件夹、组头是项目图标」以及「桌面端 24px、控制台 16px」的由来。
 *
 * 树的层级靠尺码与字号说（`depth`），不靠缩进堆：深一层就降一档字形、把名字退成
 * mono 小标题。
 *
 * 六个动作（设置 / 子项目 / 新会话 / 终端 / 合并 / 删除）**不在这里** —— 那是宿主的
 * 产品决定，桌面端与 agentre-server 各有各的一份，从 `actions` 递进来。
 */
export type ProjectGroupHeaderProps = {
  project: ProjectGlyphInfo;
  /** 宿主图标注册表解出来的那枚图标；不给就退回项目名首字。 */
  glyph?: React.ReactNode;
  /** 树深度（0 = 根项目）。 */
  depth?: number;
  expanded: boolean;
  onToggle: () => void;
  attentionCount: number;
  attentionTone: AgentStatus | null;
  /** 记号的 title（「N 条在跑」之类）。要 i18n，所以由宿主给。 */
  attentionTitle?: string;
  /** 名字退一档颜色（例如整棵树都没配本地路径）。 */
  labelMuted?: boolean;
  badges?: React.ReactNode;
  actions?: React.ReactNode;
  testId?: string;
} & Omit<React.ComponentProps<"div">, "onToggle" | "color">;

export function ProjectGroupHeader({
  project,
  glyph,
  depth = 0,
  expanded,
  onToggle,
  attentionCount,
  attentionTone,
  attentionTitle,
  labelMuted,
  badges,
  actions,
  testId = "project-group-header",
  ...props
}: ProjectGroupHeaderProps) {
  return (
    <IndexGroupHeader
      testId={testId}
      depth={depth}
      expanded={expanded}
      onToggle={onToggle}
      glyph={(className) => (
        <ProjectGlyph
          testId="project-group-glyph"
          project={project}
          glyph={glyph}
          className={className}
        />
      )}
      label={project.name}
      labelMuted={labelMuted}
      attentionCount={attentionCount}
      attentionTone={attentionTone}
      attentionTestId="project-attention-mark"
      attentionTitle={attentionTitle}
      badges={badges}
      actions={actions}
      {...props}
    />
  );
}
