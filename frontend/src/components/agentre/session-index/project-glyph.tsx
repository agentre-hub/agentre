// project-glyph.tsx —— 项目字形的**宿主那一半**。
//
// 字形本身已经搬进共享包（规格 2026-08-21「呈现件归包」），这里不再画任何东西，
// 只做包留给宿主的那一件事：把项目自己选的 icon key 经 icon-registry 解成一个画好的
// 节点递进 `glyph` 插槽。图标表是宿主的产品决定，不进包（决策 3）。
//
// 与 `primitives.tsx` 的 `AgentAvatar` 是同一种转发（决策 4）：留着它，
// `projectInfoOf` 那套「项目信息带着 icon key 走」的形状与全部调用点都不用改。
import { ProjectGlyph as UiProjectGlyph } from "@agentre-ai/agentre-ui";

import { agentIconNode } from "../primitives";

/** 画一个项目字形要且只要这三件事 —— 比包里多一个 icon key，那是宿主才解得开的。 */
export type ProjectGlyphInfo = {
  name: string;
  /** 颜色 token，如 "agent-1"。 */
  color: string;
  /** icon-registry 的图标 key；空串 / 不在表里时退回项目名首字。 */
  icon: string;
};

export type ProjectGlyphProps = {
  /** `null` = 自由会话，不属于任何项目。 */
  project: ProjectGlyphInfo | null;
  /** 尺寸与圆角由调用方给（行里 14px、子项目 16px、组头 24px）。 */
  className?: string;
  testId?: string;
};

export function ProjectGlyph({
  project,
  className,
  testId,
}: ProjectGlyphProps) {
  return (
    <UiProjectGlyph
      project={project}
      glyph={agentIconNode(project?.icon)}
      className={className}
      testId={testId}
    />
  );
}
