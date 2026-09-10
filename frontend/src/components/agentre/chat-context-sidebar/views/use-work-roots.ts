import * as React from "react";

import { WorkspaceFsWorkRoots } from "@/../wailsjs/go/app/App";
import { useSessionStatus } from "@/stores/session-status-store";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

import type { chat_svc } from "../../../../../wailsjs/go/models";

import { deriveLatestWriteRoot } from "../derive";

export type WorkRoot = workspace_fs_svc.WorkRootView;

/** 自动跟随的提示行在屏幕上停留多久（spec：「短暂存在后自行收起」）。 */
const NOTICE_MS = 5000;

type Params = {
  sessionId: number;
  /** 会话工作目录；空串表示没有工作目录，不去认领工作根。 */
  cwd: string;
  messages: chat_svc.ChatMessage[];
};

export type WorkRoots = {
  /** 已认领的工作根，按后端给的顺序（主仓库在前）。少于 2 个时切换器不渲染。 */
  roots: WorkRoot[];
  /** 当前工作根的绝对路径；根集合还没回来时就是会话 cwd。 */
  current: string;
  /**
   * 传给 `workspacefs.*` 绑定的 root 实参：当前根就是会话 cwd 时用空串——
   * 后端把空串解释成「会话 cwd」，单根会话因此完全走本轮之前的那条路径。
   */
  rootArg: string;
  /** 用户手动选过根：本会话内停止自动跟随（spec 决策 9）。 */
  pinned: boolean;
  /** 刚刚自动跟随切过去的那个根；null = 不显示提示行。 */
  followedTo: WorkRoot | null;
  /** 手动选根：切过去，并从此停止自动跟随。 */
  select: (path: string) => void;
  /** 提示行上的即时撤销：回到主仓库并固定在那里。 */
  stayInMain: () => void;
};

/**
 * useWorkRoots 是「当前看的是哪个工作根」唯一的持有者：认领集合的取数、自动
 * 跟随、手动固定与根消失时的回落都在这里，「变更」页与「目录」页共享它的结果
 * （spec：切一级 tab 不改变工作根）。
 *
 * 认领集合是**运行期**的：AI 中途 `git worktree add` 之后本会话才会多出一个根，
 * 所以和 useGitChanges 同样订阅本会话的 doneTick，在轮次结束时重取。
 */
export function useWorkRoots({ sessionId, cwd, messages }: Params): WorkRoots {
  const [roots, setRoots] = React.useState<WorkRoot[]>([]);
  // selected 是「被选中的根」，可能来自用户手动选择、也可能来自自动跟随；
  // null = 还没选过，用 cwd 对应的那个根。
  const [selected, setSelected] = React.useState<string | null>(null);
  const [pinned, setPinned] = React.useState(false);
  const [followed, setFollowed] = React.useState<string | null>(null);

  const doneTick = useSessionStatus(sessionId)?.doneTick ?? 0;
  const genRef = React.useRef(0);

  // 换会话：认领集合、选中的根、固定态与提示行整套重置，一样都不带进新会话。
  React.useEffect(() => {
    setRoots([]);
    setSelected(null);
    setPinned(false);
    setFollowed(null);
  }, [sessionId]);

  React.useEffect(() => {
    // 代际先于 return 自增：换到一个没有工作目录的会话时，此前在途的响应同样
    // 必须作废，否则它回来时代际仍然相等，会把上一个会话的根集合摆上来。
    genRef.current += 1;
    const gen = genRef.current;
    if (cwd === "") {
      setRoots([]);
      return;
    }
    WorkspaceFsWorkRoots(sessionId).then(
      (views) => {
        if (genRef.current !== gen) return;
        setRoots(views ?? []);
      },
      () => {
        // 认领集合是增强信息：取不到就当作只有 cwd 一个根（切换器不渲染），
        // 目录与变更照常按 cwd 工作，不为此弹一条用户帮不上忙的错误。
        if (genRef.current !== gen) return;
        setRoots([]);
      },
    );
  }, [sessionId, cwd, doneTick]);

  const rootPaths = React.useMemo(() => roots.map((r) => r.path), [roots]);
  const mainPath = roots.find((r) => r.isPrimary)?.path ?? cwd;
  // 根消失（worktree 被移除）时选中的那条路径不再在集合里：回落到 cwd 对应的
  // 根而不是留一个空白（spec「呈现」）。
  const current =
    selected !== null && rootPaths.includes(selected) ? selected : cwd;

  const latestWriteRoot = React.useMemo(
    () => deriveLatestWriteRoot(messages, rootPaths),
    [messages, rootPaths],
  );

  // 自动跟随：AI 写进另一个已认领的根就切过去，并留下一条可撤销的提示行。
  // 用户手动选过之后（pinned）不再跟随——自动跟随不跟用户抢（决策 9）。
  React.useEffect(() => {
    if (pinned) return;
    if (latestWriteRoot === null || latestWriteRoot === current) return;
    setSelected(latestWriteRoot);
    setFollowed(latestWriteRoot);
  }, [pinned, latestWriteRoot, current]);

  // 提示行短暂存在后自行收起，收起不改变已经切过去的工作根。
  React.useEffect(() => {
    if (followed === null) return;
    const timer = window.setTimeout(() => setFollowed(null), NOTICE_MS);
    return () => window.clearTimeout(timer);
  }, [followed]);

  const select = React.useCallback((path: string) => {
    setSelected(path);
    setPinned(true);
    setFollowed(null);
  }, []);

  const stayInMain = React.useCallback(() => {
    setSelected(mainPath);
    setPinned(true);
    setFollowed(null);
  }, [mainPath]);

  return {
    roots,
    current,
    rootArg: current === cwd ? "" : current,
    pinned,
    followedTo:
      followed === null
        ? null
        : (roots.find((r) => r.path === followed) ?? null),
    select,
    stayInMain,
  };
}
