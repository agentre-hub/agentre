import * as React from "react";
import { GripVertical } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { AgentAvatar } from "../ui/agent-avatar";
import type { OrgDragHandleBinding, OrgDropState } from "./drag-binding";
import type { OrgIndexRow } from "./org-index-model";
import { isOrgSystemAgent, type OrgSelection } from "./types";

/**
 * 索引里的一行 Agent。
 *
 * **一律同级**：下属 Agent 与主管平排，从属关系写在行内那句 `↳ 主管`，缩进只由所在
 * 部门决定（见 org-index-model.ts）。行自己不判定任何东西 —— 落点合不合法、现在瞄
 * 的是不是它，都是宿主算好了递进来的。
 *
 * 形态是**内缩的圆角块**（列表左右各留一段内边距，见宿主容器），不是通栏条：没有
 * 下边框、没有左竖条，选中靠底色 + `aria-current`（与会话索引同一套，规格
 * 2026-08-19「删掉两根左竖条」）。行高压到 ≈28px —— 18px 头像、单行、12px 名字；
 * 描述不进索引，它在详情里。
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
  // 行尾只画两种事实之一：宿主明说「一档执行目标都没有」，或者宿主喂了后端名。
  // 两者都没有就什么都不画 —— 「没喂」不是「没有」（见 types.ts 的 noExecTarget）。
  const noTarget = agent.noExecTarget === true;
  const tail = noTarget ? t("org.index.noExecTarget") : agent.backend?.name;

  return (
    <div
      ref={dropRef}
      data-testid={`org-row-${agent.id}`}
      data-slot="org-index-row"
      data-agent-id={agent.id}
      // 下属与主管一律同级：缩进只由所在部门决定，从属关系写在行内。
      data-indent={indent}
      style={{ paddingLeft: 8 + indent * 15 }}
      data-drop-kind={droppable ? "agent" : undefined}
      data-drop-state={dropState}
      aria-current={selected ? "true" : undefined}
      className={cn(
        "flex items-center gap-[7px] rounded-md py-[5px] pr-2 text-xs transition-colors",
        // hover 只发给未选中的行：同时挂两个底色时 `hover:` 那个级联上必胜，鼠标
        // 一停选中面就整块被顶掉（与 session-row 同一条）。
        !selected && "hover:bg-sidebar-active-bg",
        // 选中面与 hover **反向**偏离静止面（hover 提亮、选中压深），外加一道极浅
        // 的浮起阴影 —— 内缩白卡浮在侧栏面上，这是竖条撤掉后的非色相线索。
        selected && "bg-sidebar-selected-bg shadow-xs",
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
          className="shrink-0 cursor-grab touch-none select-none rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
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
        className="flex min-w-0 flex-1 items-center gap-[7px] text-left"
      >
        {avatar ?? (
          <AgentAvatar
            name={agent.name}
            color={agent.avatarColor}
            size="xs"
            className="size-4.5 shrink-0 rounded-[5px] text-[10px]"
          />
        )}
        <span
          className={cn(
            "min-w-0 flex-1 truncate",
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
        {isOrgSystemAgent(agent) && (
          <span
            data-slot="org-row-system-badge"
            className="shrink-0 rounded-sm bg-primary-soft px-1.5 text-2xs font-semibold text-primary-text"
          >
            {t("org.index.systemBadge")}
          </span>
        )}
        {tail && (
          <span
            data-testid={`org-row-tail-${agent.id}`}
            className={cn(
              "shrink-0 text-2xs",
              noTarget ? "text-destructive" : "text-muted-foreground",
            )}
          >
            {tail}
          </span>
        )}
      </button>
    </div>
  );
}
