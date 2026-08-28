import { CornerDownRight } from "lucide-react";
import { useTranslation } from "react-i18next";

import type { WorkRoot } from "./views/use-work-roots";

type Props = {
  root: WorkRoot;
  onUndo: () => void;
};

/**
 * RootFollowNotice 是自动跟随发生的那一刻交代「侧栏为什么换了内容」的临时提示行
 * （spec「切换与跟随」）：它带一个即时撤销入口，短暂存在后由 useWorkRoots 自行
 * 收起。撤销与手动选根同义——一旦用户表达过意图，本会话内就不再自动跟随。
 */
export function RootFollowNotice({ root, onUndo }: Props) {
  const { t } = useTranslation();
  return (
    <div
      data-testid="root-follow-notice"
      className="flex shrink-0 items-center gap-1.5 border-b border-border bg-muted/40 px-2 py-1 text-3xs text-muted-foreground"
    >
      <CornerDownRight className="size-3 shrink-0" aria-hidden="true" />
      <span className="min-w-0 flex-1 truncate">
        {/*
          跟随是双向的：AI 从 worktree 回到主 checkout 时也会切。那一句还写
          「worktree」就把主仓库叫成了 worktree，所以按这个根自己的身份选文案，
          两句都是静态 key（动态拼 key 会从 i18n 覆盖检查里溜掉）。
        */}
        {root.isWorktree
          ? t("chatContext.workRoots.followed", { name: root.name })
          : t("chatContext.workRoots.followedRepo", { name: root.name })}
      </span>
      <button
        type="button"
        onClick={onUndo}
        className="shrink-0 cursor-pointer rounded px-1 py-0.5 font-medium text-foreground underline-offset-2 hover:underline focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
      >
        {t("chatContext.workRoots.stayInMain")}
      </button>
    </div>
  );
}
