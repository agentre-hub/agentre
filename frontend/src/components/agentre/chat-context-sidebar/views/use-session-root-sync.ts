import * as React from "react";

import { WorkspaceFsWorkRoots } from "@/../wailsjs/go/app/App";
import { EventsOn } from "@/../wailsjs/runtime/runtime";
import {
  useSessionRootStore,
  type WorkRoot,
} from "@/stores/session-root-store";
import { useSessionStatus } from "@/stores/session-status-store";

type Params = {
  sessionId: number;
  /** 会话工作目录；空串表示没有工作目录，不认领工作根。 */
  cwd: string;
};

/** 后端 chat_svc.WorkRootsEvent：认领集合或 AI 所在根变化时推送（含轮次进行中）。 */
type WorkRootsEvent = { sessionId: number; roots: WorkRoot[] };

/**
 * useSessionRootSync 把一个会话的认领集合喂进会话根 store（session-root-store）：
 *   - 挂载、换会话 / 换目录与轮次结束（doneTick）时取一次——worktree 被移除只在
 *     重取里看得到；
 *   - 订阅 `chat:workroots:<sessionId>`：AI 进出 worktree 在轮次进行中就推过来，
 *     跟随因此不等轮末。
 *
 * 每个会话只挂一份（对话页的工具窗骨架里）；读当前根的地方一律用 useSessionRoot。
 */
export function useSessionRootSync({ sessionId, cwd }: Params): void {
  const doneTick = useSessionStatus(sessionId)?.doneTick ?? 0;
  // 推送与取数共用一个代际：推送比在途的取数新，取数晚到时必须作废。
  const genRef = React.useRef(0);

  React.useEffect(() => {
    if (sessionId <= 0 || cwd === "") return;
    return EventsOn(`chat:workroots:${sessionId}`, (payload: unknown) => {
      const roots = (payload as WorkRootsEvent | null)?.roots;
      if (!Array.isArray(roots)) return;
      genRef.current += 1;
      useSessionRootStore.getState().applyRoots(sessionId, cwd, roots);
    });
  }, [sessionId, cwd]);

  // 认下会话 cwd 放在 layout effect：首帧之前当前根就已是 cwd，目录与胶囊不会先画
  // 一帧「没有工作目录」。
  React.useLayoutEffect(() => {
    if (sessionId <= 0) return;
    useSessionRootStore.getState().attach(sessionId, cwd);
  }, [sessionId, cwd]);

  React.useEffect(() => {
    genRef.current += 1;
    const gen = genRef.current;
    if (sessionId <= 0 || cwd === "") return;
    WorkspaceFsWorkRoots(sessionId).then(
      (views) => {
        if (genRef.current !== gen) return;
        useSessionRootStore.getState().applyRoots(sessionId, cwd, views ?? []);
      },
      () => {
        // 取不到就保留已知集合：空集合会被读成「根都被移除了」。认领集合是增强
        // 信息，目录与变更照常按已知的当前根（至少是 cwd）工作。
      },
    );
  }, [sessionId, cwd, doneTick]);
}
