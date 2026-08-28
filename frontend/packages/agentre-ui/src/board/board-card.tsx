import * as React from "react";
import { AlignLeft, CornerDownRight } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { formatRelativeTime } from "../lib/relative-time";
import { cn } from "../lib/utils";
import { ProjectGlyph } from "../session-index/project-glyph";

import { BoardCardLabels } from "./card-labels";
import { BoardCardMenu } from "./card-menu";
import { matchExcerpt } from "./card-excerpt";
import { splitMatch, type MatchSegment } from "./scope-tree";
import type { BoardCardDragBinding, BoardCardView, BoardPorts } from "./types";

/** 命中片段高亮：颜色沿用范围选择器里那一处，不为它新造一档。 */
function Highlighted({ segments }: { segments: MatchSegment[] }) {
  return (
    <>
      {segments.map((segment, index) =>
        segment.match ? (
          <mark
            key={index}
            data-slot="board-card-match"
            className="bg-transparent font-semibold text-primary-text"
          >
            {segment.text}
          </mark>
        ) : (
          <React.Fragment key={index}>{segment.text}</React.Fragment>
        ),
      )}
    </>
  );
}

export interface BoardCardProps {
  card: BoardCardView;
  ports: BoardPorts;
  /** 此刻生效的关键词；命中片段高亮，命中落在描述里时多摘一行。 */
  keyword?: string;
  drag?: BoardCardDragBinding;
  /** 相对时间的「现在」；不给就取 `Date.now()`。 */
  nowMs?: number;
}

/**
 * 看板上的一张卡。
 *
 * 卡体是按钮（点开编辑），菜单触发器是它的**兄弟**而不是子节点 —— 按钮套按钮
 * 既不合法，读屏也会把菜单念成卡片名字的一部分。
 */
export function BoardCard({
  card,
  ports,
  keyword,
  drag,
  nowMs,
}: BoardCardProps) {
  const { t } = useUiTranslation();
  const dragState = drag?.state;
  // 与 IssueBoard 同一条：宿主的 nowMs 优先，不给才退回挂载那一刻（渲染期调
  // Date.now() 是不纯的）。看板里这个值本来就由 IssueBoard 统一递下来。
  const [mountedNow] = React.useState(Date.now);
  const time = card.updatedAt
    ? formatRelativeTime(card.updatedAt, nowMs ?? mountedNow, t)
    : "";
  const excerpt = matchExcerpt(card.description, keyword);

  return (
    <article
      ref={drag?.setNodeRef}
      style={drag?.style}
      data-testid={`board-card-${card.id}`}
      data-drag-state={dragState}
      className="group/card relative"
    >
      <button
        {...drag?.attributes}
        {...drag?.listeners}
        type="button"
        data-testid={`board-card-body-${card.id}`}
        onClick={() => ports.onEdit(card.id)}
        className={cn(
          "flex w-full cursor-pointer flex-col gap-2 rounded-md border bg-card px-3 py-2.5 text-left transition-colors",
          "focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
          dragState === "ghost"
            ? // 原位残影：卡还在这儿占着位置，但内容已经被拿走了。
              "border-dashed border-border-strong bg-transparent opacity-60 shadow-none"
            : "border-border shadow-xs hover:border-border-strong",
          dragState === "lifted" && "rotate-2 border-primary shadow-overlay",
        )}
      >
        <span className="flex items-center gap-1.5 pr-6">
          <span className="font-mono text-2xs font-semibold text-primary-text">
            {`#${card.id}`}
          </span>
          {card.project ? (
            <span className="flex min-w-0 items-center gap-1">
              {card.project.nested ? (
                <CornerDownRight
                  className="size-3 shrink-0 text-muted-foreground"
                  aria-hidden="true"
                />
              ) : null}
              <ProjectGlyph
                project={card.project}
                glyph={card.project.glyph}
                testId={`board-card-project-${card.id}`}
              />
              <span className="truncate text-2xs text-muted-foreground">
                {card.project.name}
              </span>
            </span>
          ) : null}
        </span>
        <h3 className="line-clamp-2 text-xs leading-normal font-semibold">
          <Highlighted segments={splitMatch(card.title, keyword ?? "")} />
        </h3>
        {excerpt ? (
          // 命中落在描述里时才有这一行，且**只有一行**：卡片不是正文的容器。
          <p
            data-testid={`board-card-excerpt-${card.id}`}
            className="truncate text-2xs text-muted-foreground"
          >
            <Highlighted segments={excerpt} />
          </p>
        ) : null}
        <BoardCardLabels labels={card.labels} />
        {card.hasDescription || time ? (
          <span className="flex items-center gap-1.5 text-2xs text-muted-foreground">
            {card.hasDescription ? (
              <AlignLeft
                data-testid="board-card-has-description"
                className="size-3 shrink-0"
                aria-label={t("board.hasDescription")}
                role="img"
              />
            ) : null}
            {time ? <span data-testid="board-card-time">{time}</span> : null}
          </span>
        ) : null}
      </button>
      <BoardCardMenu
        cardId={card.id}
        stage={card.stage}
        ports={ports}
        className="absolute top-1.5 right-1.5"
      />
    </article>
  );
}
