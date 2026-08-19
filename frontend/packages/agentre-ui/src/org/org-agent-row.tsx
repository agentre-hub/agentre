import * as React from "react";
import { GripVertical } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Glyph, initialOf } from "../session-index/glyph";
import type { OrgDragHandleBinding, OrgDropState } from "./drag-binding";
import type { OrgIndexRow } from "./org-index-model";
import type { OrgSelection } from "./types";

/**
 * 索引里的一行 Agent。
 *
 * **一律同级**：下属 Agent 与主管平排，从属关系写在行内那句 `↳ 主管`，缩进只由所在
 * 部门决定（见 org-index-model.ts）。行自己不判定任何东西 —— 落点合不合法、现在瞄
 * 的是不是它，都是宿主算好了递进来的。
 */
export type OrgAgentRowProps = {
  row: OrgIndexRow;
  /** 缩进层数（组的 depth）。 */
  indent: number;
  selected: boolean;
  onSelect: (selection: OrgSelection) => void;
  /** 这一行现在是不是一个落点（决定 `data-drop-kind`）。 */
  droppable?: boolean;
  dropState?: OrgDropState;
  /** 整行的落点 ref。宿主不拖拽时不传。 */
  dropRef?: (node: HTMLElement | null) => void;
  /**
   * 拖拽柄。不传就画一个等宽的占位 —— 系统 Agent 不可拖动、以及根本没有拖拽的
   * 宿主，都走这一条：少画一个柄会让这一行的左缘比邻居往左缩，整列参差。
   */
  dragHandle?: OrgDragHandleBinding;
  /** 宿主自带的 Agent 头像。不给就用包内字形（名字首字 + 调色板底色）。 */
  avatar?: React.ReactNode;
};

export function OrgAgentRow({
  row,
  indent,
  selected,
  onSelect,
  droppable,
  dropState,
  dropRef,
  dragHandle,
  avatar,
}: OrgAgentRowProps) {
  const { t } = useUiTranslation();
  // 解构而不是留着 `dragHandle.` 前缀：把它的 ref 挂到 ref= 上会让整个绑定对象被
  // react-hooks/refs 视作 ref，之后连 attributes 都读不得（宿主那侧踩过同一条）。
  const {
    ref: handleRef,
    attributes: handleAttributes,
    listeners: handleListeners,
    onKeyDown: onHandleKeyDown,
  } = dragHandle ?? {};
  const agent = row.agent;

  return (
    <div
      ref={dropRef}
      data-testid={`org-row-${agent.id}`}
      data-slot="org-index-row"
      data-agent-id={agent.id}
      // 下属与主管一律同级：缩进只由所在部门决定，从属关系写在行内。
      data-indent={indent}
      style={{ paddingLeft: 17 + indent * 16 }}
      data-drop-kind={droppable ? "agent" : undefined}
      data-drop-state={dropState}
      aria-current={selected ? "true" : undefined}
      className={cn(
        "flex items-center gap-2 border-b border-l-[3px] border-border/60 py-2 pr-5 transition-colors",
        selected ? "border-l-primary bg-primary-soft" : "border-l-transparent",
        dropState === "valid" && "ring-2 ring-primary/60",
        dropState === "invalid" && "ring-2 ring-destructive",
      )}
    >
      {dragHandle ? (
        <button
          type="button"
          ref={handleRef}
          data-testid={`org-row-handle-${agent.id}`}
          aria-label={t("org.index.drag.agentHandle", { name: agent.name })}
          {...(handleAttributes ?? {})}
          {...(handleListeners ?? {})}
          onKeyDown={onHandleKeyDown}
          className="shrink-0 cursor-grab touch-none select-none rounded-sm text-subtle-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <GripVertical className="size-3.5" aria-hidden="true" />
        </button>
      ) : (
        <span className="size-3.5 shrink-0" aria-hidden="true" />
      )}
      <button
        type="button"
        data-testid={`org-row-select-${agent.id}`}
        onClick={() => onSelect({ kind: "agent", id: agent.id })}
        className="flex min-w-0 flex-1 items-center gap-3 text-left"
      >
        {avatar ?? (
          <Glyph
            color={agent.avatarColor}
            className="size-8 shrink-0 rounded-lg text-xs"
          >
            {initialOf(agent.name, true)}
          </Glyph>
        )}
        <span className="flex min-w-0 flex-col">
          <span className="flex min-w-0 items-center gap-1.5">
            <span
              className={cn(
                "truncate text-sm font-semibold",
                selected && "text-primary-text",
              )}
            >
              {agent.name}
            </span>
            {row.reportsToName && (
              <span
                data-slot="org-row-reports-to"
                className="shrink-0 font-mono text-2xs text-muted-foreground"
              >
                {t("org.index.reportsToInline", { name: row.reportsToName })}
              </span>
            )}
          </span>
          {agent.description && (
            <span className="truncate text-2xs text-muted-foreground">
              {agent.description}
            </span>
          )}
        </span>
        {agent.backend?.name && (
          <span className="shrink-0 rounded border border-transparent bg-secondary px-2 py-0.5 font-mono text-2xs">
            {agent.backend.name}
          </span>
        )}
      </button>
    </div>
  );
}
