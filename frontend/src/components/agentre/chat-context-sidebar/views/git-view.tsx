import * as React from "react";
import { useTranslation } from "react-i18next";

import { Button } from "@agentre-hub/agentre-ui";

import { deriveGitRows, GIT_STATUS_META, gitStatusLabel } from "../git-rows";

import { ChangeRowView } from "./change-row";
import { PanelNotice, PanelSkeleton } from "./panel-feedback";
import { SidebarList } from "./sidebar-list";
import type { GitChangesState } from "./use-git-changes";

type Props = {
  /** 会话 id：预览选中按会话存储。 */
  sessionId: number;
  /** 会话工作目录，行的路径解析与本机操作按它算。 */
  cwd: string;
  remote: boolean;
  state: GitChangesState;
  onRetry: () => void;
};

/**
 * GitView 是「变更」页「未提交」档的内容区：工作区相对 HEAD 的变动（含未跟踪
 * 文件）扁平列表，语义与本轮之前的 Git 页未提交档一致（spec 决策 5——「本分支」
 * 与任意基线选择整个去掉）。
 *
 * 取数在 useGitChanges 里，这里只负责把状态渲染成行与各种反馈态。「没有工作
 * 目录」与「不是 git 仓库」两种情形由 ChangesPanel 在更外层收掉（那时这一档
 * 连胶囊都不渲染），所以这里不再有它们的空态。
 */
export function GitView({ sessionId, cwd, remote, state, onRetry }: Props) {
  const { t } = useTranslation();

  const changes = state.status === "loaded" ? state.view.changes : null;
  const rows = React.useMemo(() => deriveGitRows(changes), [changes]);

  if (state.status === "loading") {
    return <PanelSkeleton label={t("chatContext.git.loading")} />;
  }

  if (state.status === "error") {
    return (
      <div className="px-3 py-6 text-center text-xs leading-relaxed text-muted-foreground">
        <p>{state.message || t("chatContext.git.readFailed")}</p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="mt-2.5 h-7 text-2xs"
          onClick={onRetry}
        >
          {t("chatContext.git.retry")}
        </Button>
      </div>
    );
  }

  if (rows.length === 0) {
    return <PanelNotice text={t("chatContext.git.cleanUncommitted")} />;
  }

  return (
    <SidebarList
      variant="list"
      label={t("chatContext.git.listAria")}
      className="flex flex-col gap-0.5 px-2 py-2.5"
    >
      {rows.map((row) => (
        <ChangeRowView
          key={row.path}
          sessionId={sessionId}
          cwd={cwd}
          remote={remote}
          sourceMode="git"
          path={row.path}
          name={row.name}
          dir={row.dir}
          meta={GIT_STATUS_META[row.status] ?? GIT_STATUS_META.modified}
          statusLabel={gitStatusLabel(t, row.status)}
          // 二进制文件没有可信的行数，只列出文件本身。
          plus={row.binary ? 0 : row.added}
          minus={row.binary ? 0 : row.deleted}
          title={row.oldPath ? `${row.oldPath} → ${row.path}` : row.path}
          testId="git-row"
          rowData={{ "data-path": row.path, "data-status": row.status }}
        />
      ))}
      {state.view.truncated ? (
        <div className="py-1.5 pr-2.5 pl-2 text-2xs text-muted-foreground">
          {t("chatContext.git.truncated", { limit: rows.length })}
        </div>
      ) : null}
    </SidebarList>
  );
}
