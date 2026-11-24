import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import type { WorkRoot } from "@/stores/session-root-store";

import type { ChangeRow, ChangeStatus } from "../derive";
import { GIT_STATUS_META, type GitStatusMeta } from "../git-rows";

import { ChangeRowView } from "./change-row";
import { PanelNotice } from "./panel-feedback";
import { SidebarList } from "./sidebar-list";

/**
 * 四种状态符号（spec 决策 14）。前三种直接复用 git 状态字母的那一份映射——同一
 * 个概念在侧栏里只有一套配色语义；「写入」是第四种，独立于 created / modified /
 * deleted：全量写入不携带写入前的内容，冒充其中任何一个都会在覆盖既有文件时说谎。
 */
const STATUS_META: Record<ChangeStatus, GitStatusMeta> = {
  created: GIT_STATUS_META.added,
  modified: GIT_STATUS_META.modified,
  deleted: GIT_STATUS_META.deleted,
  written: { letter: "W", className: "text-primary-text" },
};

/**
 * statusLabel 逐个写死 key 而不是拼 `chatContext.changes.status.${status}`——动态
 * key 会从 i18n 的静态 key 覆盖检查里溜掉，漏翻译不会被测出来。
 */
function statusLabel(t: TFunction, status: ChangeStatus): string {
  switch (status) {
    case "created":
      return t("chatContext.changes.status.created");
    case "deleted":
      return t("chatContext.changes.status.deleted");
    case "written":
      return t("chatContext.changes.status.written");
    default:
      return t("chatContext.changes.status.modified");
  }
}

/** 一个已被移除的工作根，以及本次会话在它里面改过的文件（路径相对该根）。 */
export type RemovedRootGroup = { root: WorkRoot; rows: ChangeRow[] };

type Props = {
  sessionId: number;
  rows: ChangeRow[];
  removedGroups?: RemovedRootGroup[];
  cwd: string;
  remote: boolean;
  onJumpToTurn: (turn: number) => void;
};

/**
 * SessionChangesView 是「变更」页「本次会话」档的内容区：本会话里工具改动过的
 * 文件，一个文件一行（spec「本次会话：工具改了什么」）。
 *
 * 它只读消息里的 canonical 块、零后端调用，因此与「有没有提交」无关——AI 中途
 * 提交、事后 rebase 或 amend 都不影响这一档。
 */
export function SessionChangesView({
  sessionId,
  rows,
  removedGroups = [],
  cwd,
  remote,
  onJumpToTurn,
}: Props) {
  const { t } = useTranslation();

  if (rows.length === 0 && removedGroups.length === 0) {
    return <PanelNotice text={t("chatContext.changes.empty")} />;
  }

  const renderRows = (list: ChangeRow[], removedRoot?: string) =>
    list.map((row) => (
      <ChangeRowView
        key={row.path}
        sessionId={sessionId}
        cwd={cwd}
        remote={remote}
        removedRoot={removedRoot}
        sourceMode="session"
        path={row.path}
        name={row.name}
        dir={row.dir}
        meta={STATUS_META[row.status]}
        statusLabel={statusLabel(t, row.status)}
        plus={row.plus}
        minus={row.minus}
        onJumpToTurn={() => onJumpToTurn(row.lastTurn)}
        testId="change-row"
        rowData={{ "data-path": row.path, "data-status": row.status }}
      />
    ));

  return (
    <>
      {rows.length > 0 ? (
        <SidebarList
          variant="list"
          label={t("chatContext.changes.listAria")}
          className="flex flex-col gap-0.5 px-2 py-2.5"
        >
          {renderRows(rows)}
        </SidebarList>
      ) : null}
      {/*
        已被移除的工作根各成一组（spec「worktree 被移除」）：组名标「（已移除）」，
        行划掉但仍可点开——磁盘上没了，Diff 来自对话记录。每组一份独立的列表：
        同一相对路径可能也在当前根里改过，行标识不能混进同一个列表。
      */}
      {removedGroups.map((group) => {
        const label = t("chatContext.workRoots.removedGroup", {
          name: group.root.name,
        });
        return (
          <section
            key={group.root.path}
            data-testid="removed-root-group"
            data-root={group.root.path}
            title={group.root.path}
            className="px-2 pb-2.5 first:pt-2.5"
          >
            <h4 className="flex items-center gap-1.5 px-1.5 pb-1 text-3xs text-muted-foreground">
              <span className="min-w-0 flex-1 truncate">{label}</span>
              <span className="shrink-0 font-mono">{group.rows.length}</span>
            </h4>
            <SidebarList
              variant="list"
              label={label}
              className="flex flex-col gap-0.5"
            >
              {renderRows(group.rows, group.root.path)}
            </SidebarList>
          </section>
        );
      })}
    </>
  );
}
