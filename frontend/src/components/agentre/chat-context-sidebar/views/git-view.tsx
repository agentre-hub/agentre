import * as React from "react";
import { useTranslation } from "react-i18next";

import { FileTypeIcon } from "@/components/agentre/file-type-icon";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import {
  deriveGitRows,
  GIT_STATUS_META,
  gitStatusLabel,
  type GitRow,
} from "../git-rows";

import { PanelNotice, PanelSkeleton } from "./panel-feedback";
import { SidebarList } from "./sidebar-list";
import { SidebarRow } from "./sidebar-row";
import type { GitChangesState, GitScope } from "./use-git-changes";

type Props = {
  /** 会话 id：预览选中按会话存储。 */
  sessionId: number;
  /** 会话工作目录；空串表示这个会话没有工作目录，直接出空态。 */
  cwd: string;
  remote: boolean;
  scope: GitScope;
  baseRef: string;
  state: GitChangesState;
  onRetry: () => void;
};

/**
 * GitView 是「文件」页的「Git」模式内容区：当前档的变动文件扁平列表。
 *
 * 取数、两档与基线都在 useGitChanges 里，这里只负责把状态渲染成行与各种空态。
 * 行的交互与另外两个模式完全一致（SidebarRow）：单击可预览文件 = 开预览标签。
 */
export function GitView({
  sessionId,
  cwd,
  remote,
  scope,
  baseRef,
  state,
  onRetry,
}: Props) {
  const { t } = useTranslation();

  const changes = state.status === "loaded" ? state.view.changes : null;
  const rows = React.useMemo(() => deriveGitRows(changes), [changes]);

  if (cwd === "") return <PanelNotice text={t("chatContext.git.noCwd")} />;

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

  if (state.view.notARepo) {
    return (
      <PanelNotice
        text={t("chatContext.git.notARepo")}
        hint={t("chatContext.git.notARepoHint")}
      />
    );
  }

  // 本分支档拿不到基线：origin/HEAD / main / master 都不可得，引导用户自己选一个。
  if (scope === "branch" && baseRef === "") {
    return (
      <PanelNotice
        text={t("chatContext.git.noBaseline")}
        hint={t("chatContext.git.noBaselineHint")}
      />
    );
  }

  if (rows.length === 0) {
    return (
      <PanelNotice
        text={
          scope === "branch"
            ? t("chatContext.git.cleanBranch", { ref: baseRef })
            : t("chatContext.git.cleanUncommitted")
        }
      />
    );
  }

  return (
    <SidebarList
      variant="list"
      label={t("chatContext.git.listAria")}
      className="flex flex-col gap-0.5 px-2 py-2.5"
    >
      {rows.map((row) => (
        <Row
          key={row.path}
          row={row}
          sessionId={sessionId}
          cwd={cwd}
          remote={remote}
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

function Row({
  row,
  sessionId,
  cwd,
  remote,
}: {
  row: GitRow;
  sessionId: number;
  cwd: string;
  remote: boolean;
}) {
  const { t } = useTranslation();
  const meta = GIT_STATUS_META[row.status] ?? GIT_STATUS_META.modified;
  return (
    <SidebarRow
      sessionId={sessionId}
      cwd={cwd}
      remote={remote}
      sourceMode="git"
      kind="file"
      path={row.path}
      name={row.name}
      depth={0}
      title={row.oldPath ? `${row.oldPath} → ${row.path}` : row.path}
      // Git 页的扁平行沿用既有形态：状态字母占住图标列，没有展开箭头槽位。
      lead={
        <>
          <span
            data-status-letter
            aria-hidden="true"
            className={cn(
              "w-3 shrink-0 text-center font-mono text-3xs font-bold",
              meta.className,
            )}
          >
            {meta.letter}
          </span>
          <span className="sr-only">{gitStatusLabel(t, row.status)}</span>
          <FileTypeIcon path={row.path} />
        </>
      }
      trailing={
        <>
          {/*
            窄栏下先挤掉的是目录后缀：它的 shrink 权重大到会先缩到没有，文件名
            因此实际上永不被截断（决策 11）；只有 basename 自己就超宽时才轮到它
            省略。目录后缀从头截断（rtl 让省略号落在开头），根目录下的文件为空。
          */}
          <span
            dir="rtl"
            className="min-w-0 flex-1 shrink-[9999] truncate text-left font-mono text-3xs opacity-55"
          >
            {row.dir}
          </span>
          <DiffBadge row={row} />
        </>
      }
      testId="git-row"
      rowData={{ "data-path": row.path, "data-status": row.status }}
    />
  );
}

/** 二进制文件没有可信的行数，只列出文件本身；两者皆为 0 时同样不出角标。 */
function DiffBadge({ row }: { row: GitRow }) {
  if (row.binary || (row.added <= 0 && row.deleted <= 0)) return null;
  return (
    <span
      aria-hidden="true"
      className="inline-flex shrink-0 items-center gap-1 font-mono text-3xs font-medium"
    >
      {row.added > 0 ? (
        <span className="text-status-running">+{row.added}</span>
      ) : null}
      {row.deleted > 0 ? (
        <span className="text-destructive">−{row.deleted}</span>
      ) : null}
    </span>
  );
}
