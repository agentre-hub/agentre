import { ArrowDown, ArrowUp, GitBranch, Pencil } from "lucide-react";
import type * as React from "react";
import { useTranslation } from "react-i18next";

import type { GitState } from "./use-git-state";

type Props = {
  /** 当前工作根的分支状态；null = 还没读到，整条收起。 */
  state: GitState | null;
};

/**
 * GitStatusBar 是「目录」页第二行左端的 git 状态条（spec 决策 7：只挂目录页，
 * 变更页那一行已被两档胶囊占满）。分支名常显，有值时依次显示与上游的领先 / 落后
 * 数与未提交文件数；宽度不足时只截断分支名，数字部分自带 `shrink-0` 不会丢。
 *
 * 工作目录不是 git 仓库时整条收起。当前工作根是 worktree 时**不重复标注**
 * 「worktree」——根切换器已经说明了这一点。
 */
export function GitStatusBar({ state }: Props) {
  const { t } = useTranslation();
  if (state === null || state.notARepo) return null;
  const branch = state.branch ?? "";
  const ahead = state.ahead ?? 0;
  const behind = state.behind ?? 0;
  const dirty = state.dirty ?? 0;
  // 一条什么都说不出的状态条不占那一行的宽度（分离头、且工作区干净）。
  if (branch === "" && ahead === 0 && behind === 0 && dirty === 0) return null;

  return (
    <div
      data-testid="git-status-bar"
      className="flex min-w-0 flex-1 items-center gap-1 text-3xs text-muted-foreground"
    >
      <GitBranch className="size-3 shrink-0" aria-hidden="true" />
      {branch !== "" ? (
        <span
          data-testid="git-status-branch"
          title={branch}
          className="min-w-0 truncate"
        >
          {branch}
        </span>
      ) : null}
      {ahead > 0 ? (
        <Metric
          testId="git-status-ahead"
          label={t("chatContext.gitStatus.ahead", { count: ahead })}
          value={ahead}
          icon={<ArrowUp className="size-2.5" aria-hidden="true" />}
        />
      ) : null}
      {behind > 0 ? (
        <Metric
          testId="git-status-behind"
          label={t("chatContext.gitStatus.behind", { count: behind })}
          value={behind}
          icon={<ArrowDown className="size-2.5" aria-hidden="true" />}
        />
      ) : null}
      {dirty > 0 ? (
        <Metric
          testId="git-status-dirty"
          label={t("chatContext.gitStatus.dirty", { count: dirty })}
          value={dirty}
          icon={<Pencil className="size-2.5" aria-hidden="true" />}
        />
      ) : null}
    </div>
  );
}

function Metric({
  testId,
  label,
  value,
  icon,
}: {
  testId: string;
  label: string;
  value: number;
  icon: React.ReactNode;
}) {
  return (
    <span
      data-testid={testId}
      title={label}
      className="inline-flex shrink-0 items-center gap-0.5 font-mono"
    >
      {icon}
      {value}
    </span>
  );
}
