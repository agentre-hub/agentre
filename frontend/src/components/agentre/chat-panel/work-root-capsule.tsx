import * as React from "react";
import { Check, ChevronDown, Folder, GitBranch } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Switch,
  cn,
} from "@agentre-hub/agentre-ui";

import {
  SESSION_ROOT_NOTICE_MS,
  useSessionRoot,
  useSessionRootStore,
  type WorkRoot,
} from "@/stores/session-root-store";

import { WorkRootChangeCountsContext } from "../chat-context-sidebar/context-windows";
import { useGitState } from "../chat-context-sidebar/views/use-git-state";

type CapsuleProps = {
  sessionId: number;
  /** 会话工作目录；调用方只在它非空时渲染胶囊。 */
  cwd: string;
  /** 胶囊外层，供下方的提示对齐到它。 */
  ref?: React.Ref<HTMLSpanElement>;
};

/** 路径末段：集合还没取回、只有 cwd 时的显示名。 */
function basename(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, "");
  const i = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  return i < 0 ? trimmed : trimmed.slice(i + 1);
}

/**
 * WorkRootCapsule 是对话标题栏副标题里的工作根胶囊（spec「标题栏工作根胶囊」），
 * 也是会话当前工作根唯一的切换点（决策 12）。
 *
 * - 主仓库：中性色，根名 + 分支；不是 git 仓库时只有目录名；
 * - worktree：醒目色（品牌色的柔和底），分支名 + 「worktree」；
 * - 只有一个根时不可点；两个及以上时点开下拉：每个根的名称、主仓库 / worktree、
 *   路径与「本次会话」变更数，当前根打勾，AI 所在的根标「AI 在这里」，底部是
 *   「跟随 AI 切换」开关。
 */
export function WorkRootCapsule({ sessionId, cwd, ref }: CapsuleProps) {
  const { t } = useTranslation();
  const sessionRoot = useSessionRoot(sessionId);
  // 分支只对当前根取：胶囊只说当前根的分支，下拉的每一行不需要它。
  const git = useGitState({
    sessionId,
    cwd,
    root: sessionRoot.rootArg,
    enabled: cwd !== "",
  });

  const root = sessionRoot.currentRoot;
  const name = root?.name || basename(sessionRoot.current || cwd);
  const isWorktree = root?.isWorktree ?? false;
  const branch = git && !git.notARepo ? git.branch : "";
  const clickable = sessionRoot.roots.length >= 2;

  const body = isWorktree ? (
    <>
      <GitBranch className="size-3 shrink-0" aria-hidden="true" />
      <span className="min-w-0 truncate font-medium">{branch || name}</span>
      <span className="shrink-0 opacity-80">
        {t("chatContext.workRoots.worktree")}
      </span>
    </>
  ) : (
    <>
      {branch ? (
        <GitBranch className="size-3 shrink-0" aria-hidden="true" />
      ) : (
        <Folder className="size-3 shrink-0" aria-hidden="true" />
      )}
      <span className="min-w-0 truncate text-foreground">{name}</span>
      {branch ? <span className="min-w-0 truncate">{branch}</span> : null}
    </>
  );

  const className = cn(
    "inline-flex h-5 min-w-0 max-w-64 items-center gap-1 rounded-md px-1.5",
    isWorktree ? "bg-primary-soft text-primary-text" : "text-muted-foreground",
  );
  const common = {
    "data-testid": "work-root-capsule",
    "data-kind": isWorktree ? "worktree" : "main",
    title: sessionRoot.current || cwd,
  };

  if (!clickable) {
    return (
      <span ref={ref} className="inline-flex min-w-0">
        <span {...common} className={className}>
          {body}
        </span>
      </span>
    );
  }

  return (
    <span ref={ref} className="inline-flex min-w-0">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            {...common}
            className={cn(
              className,
              "cursor-pointer transition-colors focus-visible:focus-outline",
              isWorktree ? "hover:bg-primary-soft/70" : "hover:bg-field-fill",
            )}
          >
            {body}
            <ChevronDown
              className="size-3 shrink-0 opacity-70"
              aria-hidden="true"
            />
          </button>
        </DropdownMenuTrigger>
        <WorkRootMenu sessionId={sessionId} />
      </DropdownMenu>
    </span>
  );
}

/** 下拉内容：只在打开时挂载，变更数也只在这时读。 */
function WorkRootMenu({ sessionId }: { sessionId: number }) {
  const { t } = useTranslation();
  const sessionRoot = useSessionRoot(sessionId);
  const counts = React.useContext(WorkRootChangeCountsContext);

  return (
    <DropdownMenuContent align="start" className="w-80 max-w-[90vw]">
      <DropdownMenuLabel className="text-3xs font-normal text-muted-foreground">
        {t("chatContext.workRoots.menuTitle")}
      </DropdownMenuLabel>
      {sessionRoot.roots.map((root) => {
        const isCurrent = root.path === sessionRoot.currentRoot?.path;
        return (
          <DropdownMenuItem
            key={root.path}
            data-testid="work-root-option"
            data-root={root.path}
            current={isCurrent}
            title={root.path}
            onSelect={() => sessionRoot.select(root.path)}
            className="items-start gap-2 text-xs"
          >
            <span className="mt-0.5 flex size-3.5 shrink-0 items-center justify-center">
              {isCurrent ? (
                <Check
                  data-testid="work-root-check"
                  className="size-3.5 text-primary-text"
                  aria-hidden="true"
                />
              ) : null}
            </span>
            <span className="flex min-w-0 flex-1 flex-col gap-0.5">
              <span className="truncate font-medium">{root.name}</span>
              <span className="flex min-w-0 items-center gap-1 text-3xs text-muted-foreground">
                <span className="shrink-0">
                  <RootKind root={root} />
                </span>
                <span aria-hidden="true">·</span>
                {/* 长路径从左侧省略，留住离根最近的几段（rtl + bdi 保住书写方向）。 */}
                <span
                  dir="rtl"
                  className="min-w-0 truncate text-left font-mono"
                >
                  <bdi>{root.path}</bdi>
                </span>
              </span>
            </span>
            {root.aiHere ? (
              <span className="mt-0.5 inline-flex shrink-0 items-center gap-1 text-3xs text-status-running-text">
                <span
                  aria-hidden="true"
                  className="size-1.5 rounded-full bg-status-running"
                />
                {t("chatContext.workRoots.aiHere")}
              </span>
            ) : null}
            <span
              data-testid="work-root-count"
              title={t("chatContext.changes.scope.session")}
              className="mt-0.5 w-4 shrink-0 text-right font-mono text-3xs text-muted-foreground"
            >
              {counts.get(root.path) ?? 0}
            </span>
          </DropdownMenuItem>
        );
      })}
      <DropdownMenuSeparator />
      {/*
        跟随开关是一项可勾选的菜单项（读屏按 menuitemcheckbox 报开 / 关），开关
        只是它的视觉；点它不关菜单，用户能看到当前根随之跳到 AI 所在的根。
      */}
      <DropdownMenuItem
        role="menuitemcheckbox"
        aria-checked={sessionRoot.follow}
        onSelect={(event) => {
          event.preventDefault();
          sessionRoot.setFollow(!sessionRoot.follow);
        }}
        className="gap-2 text-xs"
      >
        <span className="min-w-0 flex-1">
          {t("chatContext.workRoots.follow")}
        </span>
        <Switch
          size="sm"
          checked={sessionRoot.follow}
          tabIndex={-1}
          aria-hidden="true"
          className="pointer-events-none"
        />
      </DropdownMenuItem>
    </DropdownMenuContent>
  );
}

/** 一个根是主仓库还是 worktree（会话本身在 worktree 里启动时，主根也是 worktree）。 */
function RootKind({ root }: { root: WorkRoot }) {
  const { t } = useTranslation();
  return root.isWorktree
    ? t("chatContext.workRoots.worktree")
    : t("chatContext.workRoots.mainRepo");
}

type NoticeProps = {
  sessionId: number;
  /** 胶囊的外层：提示的左边缘对齐它。 */
  anchorRef: React.RefObject<HTMLElement | null>;
};

/**
 * WorkRootNotice 是胶囊下方约 5 秒的一次性提示（spec「跟随」「worktree 被移除」）：
 * 「已跟随 AI 切到 worktree <名称>」附「留在 <原根名>」，或「worktree <名称> 已被移除，
 * 回到 <主根名>」。
 *
 * 它就地渲染在标题栏下方（不走 portal）：后台标签页整页 `invisible`，一个后台会话
 * 跟随时弹出的提示必须跟着它的标签页一起隐藏，而不是浮在当前标签页上。
 */
export function WorkRootNotice({ sessionId, anchorRef }: NoticeProps) {
  const { t } = useTranslation();
  const sessionRoot = useSessionRoot(sessionId);
  const notice = sessionRoot.notice;
  const noticeId = notice?.id ?? null;
  const ref = React.useRef<HTMLDivElement>(null);
  const [left, setLeft] = React.useState<number | null>(null);

  React.useEffect(() => {
    if (noticeId === null) return;
    const timer = window.setTimeout(
      () => useSessionRootStore.getState().dismissNotice(sessionId, noticeId),
      SESSION_ROOT_NOTICE_MS,
    );
    return () => window.clearTimeout(timer);
  }, [sessionId, noticeId]);

  React.useLayoutEffect(() => {
    if (noticeId === null) return;
    const anchor = anchorRef.current;
    const box = ref.current?.offsetParent;
    if (!anchor || !box) return;
    setLeft(
      anchor.getBoundingClientRect().left - box.getBoundingClientRect().left,
    );
  }, [noticeId, anchorRef]);

  if (notice === null) return null;

  const text =
    notice.kind === "removed"
      ? t("chatContext.workRoots.removed", {
          name: notice.removed.name,
          main: notice.primary.name,
        })
      : notice.to.isWorktree
        ? t("chatContext.workRoots.followed", { name: notice.to.name })
        : t("chatContext.workRoots.followedRepo", { name: notice.to.name });

  return (
    <div
      ref={ref}
      role="status"
      data-testid="work-root-notice"
      style={left === null ? undefined : { left }}
      className="absolute top-full left-5 z-20 -mt-2 flex max-w-[calc(100%-2.5rem)] items-center gap-2 rounded-md bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-overlay"
    >
      <GitBranch
        className="size-3.5 shrink-0 text-muted-foreground"
        aria-hidden="true"
      />
      <span className="min-w-0 truncate">{text}</span>
      {notice.kind === "followed" ? (
        <button
          type="button"
          onClick={sessionRoot.stay}
          className="shrink-0 cursor-pointer rounded px-1 font-medium text-primary-text underline-offset-2 hover:underline focus-visible:focus-outline"
        >
          {t("chatContext.workRoots.stay", { name: notice.from.name })}
        </button>
      ) : null}
    </div>
  );
}
