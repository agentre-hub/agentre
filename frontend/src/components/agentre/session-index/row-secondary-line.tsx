// row-secondary-line.tsx —— 「按时间」档会话行的第二行（决策 5）。
//
// 这一档没有组头，两维都得落在行里。两维各自带**和其它档同一个字形**：agent 头像
// 就是「按项目」档行首那一枚，项目字形就是「按 Agent」档行首、以及项目组头上那一枚
// —— 否则同一条会话在三个档之间长出三种样子，切档时读者要重新找一遍锚点。
//
// 自由会话如实写「随手对话」并把字形置灰，而不是留半行空白：决策 7 说它是一个正当的
// 去处，空白读起来像「项目丢了」。
import { AgentAvatar } from "../primitives";
import { type AgentColor } from "../types";

import { ProjectGlyph, type ProjectGlyphInfo } from "./project-glyph";

export type RowSecondaryLineProps = {
  agentName: string;
  /** 颜色 token，如 "agent-1"。 */
  agentColor: string;
  /** 会话所属项目；`null` = 自由会话（字形与文案一并退到「随手对话」）。 */
  project: ProjectGlyphInfo | null;
  /** 自由会话那半行的文案。i18n 留在宿主，这里只排版。 */
  freeLabel: string;
};

/** 两维同形同尺寸：都是 14px 的方块，只是里面的身份不同。 */
const GLYPH_SLOT_CLASS_NAME =
  "inline-flex size-3.5 shrink-0 items-center justify-center";
const GLYPH_CLASS_NAME = "size-full rounded-sm text-[8px]";

export function RowSecondaryLine({
  agentName,
  agentColor,
  project,
  freeLabel,
}: RowSecondaryLineProps) {
  const projectName = project?.name || freeLabel;

  return (
    <span
      data-testid="row-secondary-line"
      className="flex min-w-0 items-center gap-1"
    >
      {agentName ? (
        <>
          <span data-kind="agent-avatar" className={GLYPH_SLOT_CLASS_NAME}>
            <AgentAvatar
              name={agentName}
              initials={agentName.trim().slice(0, 1).toUpperCase() || undefined}
              color={(agentColor as AgentColor) || "agent-1"}
              size="sm"
              className={GLYPH_CLASS_NAME}
            />
          </span>
          <span className="truncate">{agentName}</span>
        </>
      ) : null}
      {agentName && projectName ? (
        <span aria-hidden="true" className="text-decorative-foreground">
          ·
        </span>
      ) : null}
      {projectName ? (
        <>
          <span
            data-kind={project ? "project-avatar" : "free-glyph"}
            className={GLYPH_SLOT_CLASS_NAME}
          >
            <ProjectGlyph project={project} className={GLYPH_CLASS_NAME} />
          </span>
          <span className="truncate">{projectName}</span>
        </>
      ) : null}
    </span>
  );
}
