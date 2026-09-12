import * as React from "react";

import { WorkspaceFsGitState } from "@/../wailsjs/go/app/App";
import { useSessionStatus } from "@/stores/session-status-store";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

export type GitState = workspace_fs_svc.GitStateView;

type Params = {
  sessionId: number;
  /** 会话工作目录；空串表示没有工作目录，不打后端。 */
  cwd: string;
  /** 当前工作根的 root 实参（空串 = 会话 cwd）。 */
  root: string;
  /** 只有需要它的「目录」页可见时才取数（决策 7：状态条只挂目录页）。 */
  enabled: boolean;
};

/**
 * useGitState 取当前工作根的分支状态（分支名 / 领先落后 / 未提交数），供目录页
 * 第二行的 git 状态条使用。
 *
 * 它走的是 `WorkspaceFsGitState` 而**不是** chat_svc 的 GetSessionGitState：后者
 * 把任何远端调用失败一律折成 `notARepo=true`，用它远端会话就永远显示「不是 git
 * 仓库」，而本轮要的正是本地与远端一致（硬不变量 5）。
 *
 * 快照的重取时机与 useGitChanges 同源：本会话轮次结束（doneTick）是「文件可能
 * 变了」的唯一强信号。读不到时返回 null——状态条是增强信息，缺失就整条收起，
 * 目录树本身照常可用。
 */
export function useGitState({
  sessionId,
  cwd,
  root,
  enabled,
}: Params): GitState | null {
  const [state, setState] = React.useState<GitState | null>(null);
  const doneTick = useSessionStatus(sessionId)?.doneTick ?? 0;
  const genRef = React.useRef(0);

  React.useEffect(() => {
    // 代际先于 return 自增：换会话、换根或切走到不取数的页时，此前在途的响应
    // 同样必须作废，否则它回来时会把上一个根的分支状态摆到这个根上。
    genRef.current += 1;
    const gen = genRef.current;
    setState(null);
    if (!enabled || cwd === "") return;
    WorkspaceFsGitState(sessionId, root).then(
      (view) => {
        if (genRef.current !== gen) return;
        setState(view);
      },
      () => {
        if (genRef.current !== gen) return;
        setState(null);
      },
    );
  }, [sessionId, cwd, root, enabled, doneTick]);

  return state;
}
