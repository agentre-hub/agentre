import * as React from "react";

import { useUiTranslation } from "../i18n";

import type { IndexAxis } from "./axis-groups";
import { AgentAvatar } from "../ui/agent-avatar";
import { ProjectGlyph, type ProjectGlyphInfo } from "./project-glyph";

/**
 * 会话行的第二行：**当前轴与行首都没说的那些维**（决策 5/8）。
 *
 *   - 时间轴没有组头也没有行首，三维全落这里；
 *   - 机器轴的组头说了机器、行首说了 Agent，这里只剩项目；
 *   - 项目 / Agent 轴各自说了一维、行首补了另一维，这里只剩机器。
 *
 * 每一维带着**和其它轴同一个字形**：Agent 方块就是「按项目」档行首那一枚，项目
 * 字形就是「按 Agent」档行首、以及项目组头上那一枚 —— 否则同一条会话在几个轴之间
 * 长出几种样子，切轴时读者要重新找一遍锚点。
 *
 * 机器离线时在机器那一维后面跟一段「离线」：本体在 server 上，机器离线只影响能不能
 * 发新消息、不影响读，所以它是行上的一个状态，而不是让整条行变灰、不可点。
 */

/** 各维之间的分隔符。是排版符号不是文案，因此不进 i18n。 */
const DIMENSION_SEPARATOR = "·";

/** 两维同形同尺寸：都是 14px 的方块，只是里面的身份不同。 */
const GLYPH_SLOT_CLASS_NAME =
  "inline-flex size-3.5 shrink-0 items-center justify-center";
const GLYPH_CLASS_NAME = "size-full rounded-sm text-[8px]";

export type RowSecondaryLineProps = {
  axis: IndexAxis;
  agent?: { name: string; color?: string } | null;
  project?: ProjectGlyphInfo | null;
  machine?: { name: string; online: boolean } | null;
  /**
   * 项目那一维缺席时的文案（桌面端的「随手对话」）。给了就如实写出来并把字形置灰
   * ——决策 7 说它是一个正当的去处，空白读起来像「项目丢了」。不给就整段省略。
   */
  freeLabel?: string;
  /** 宿主自带的字形（桌面端的 AgentAvatar / 项目图标注册表）。 */
  agentGlyph?: React.ReactNode;
  projectGlyph?: React.ReactNode;
  testId?: string;
};

export function RowSecondaryLine({
  axis,
  agent,
  project,
  machine,
  freeLabel,
  agentGlyph,
  projectGlyph,
  testId = "row-secondary-line",
}: RowSecondaryLineProps) {
  const { t } = useUiTranslation();

  const agentPart =
    agent && agent.name ? (
      <>
        <span className={GLYPH_SLOT_CLASS_NAME} data-kind="agent-avatar">
          <AgentAvatar
            name={agent.name}
            color={agent.color}
            icon={agentGlyph}
            size="xs"
            className={GLYPH_CLASS_NAME}
          />
        </span>
        <span className="truncate">{agent.name}</span>
      </>
    ) : null;

  const projectName = project?.name || freeLabel;
  const projectPart = projectName ? (
    <>
      <span
        className={GLYPH_SLOT_CLASS_NAME}
        data-kind={project ? "project-avatar" : "free-glyph"}
      >
        <ProjectGlyph
          project={project}
          glyph={projectGlyph}
          className={GLYPH_CLASS_NAME}
        />
      </span>
      <span className="truncate">{projectName}</span>
    </>
  ) : null;

  const machinePart = machine ? (
    <>
      <span className="truncate">{machine.name}</span>
      {!machine.online ? (
        <span data-kind="machine-offline">
          {DIMENSION_SEPARATOR} {t("sessionIndex.machine.offline")}
        </span>
      ) : null}
    </>
  ) : null;

  // 决策 8：说哪几维由轴决定，说的顺序在四个轴上是同一套（Agent → 项目 → 机器）。
  const dimensions: [string, React.ReactNode][] =
    axis === "time"
      ? [
          ["agent", agentPart],
          ["project", projectPart],
          ["machine", machinePart],
        ]
      : axis === "machine"
        ? [["project", projectPart]]
        : [["machine", machinePart]];
  const parts = dimensions.filter(([, node]) => node !== null);

  // 一维都不剩时不画一条空行（组头 + 行首已经把话说完了）。
  if (parts.length === 0) return null;

  return (
    <span
      data-testid={testId}
      className="flex min-w-0 items-center gap-1.5 truncate"
    >
      {parts.map(([dimension, node], index) => (
        <span
          key={dimension}
          data-dimension={dimension}
          className="flex min-w-0 items-center gap-1"
        >
          {index > 0 ? (
            <span aria-hidden="true" className="text-decorative-foreground">
              {DIMENSION_SEPARATOR}
            </span>
          ) : null}
          {node}
        </span>
      ))}
    </span>
  );
}
