import * as React from "react";

import { WorkspaceFsGitChanges } from "@/../wailsjs/go/app/App";
import { useSessionStatus } from "@/stores/session-status-store";

import type { workspace_fs_svc } from "@/../wailsjs/go/models";

import { errorText } from "./panel-feedback";

type Change = workspace_fs_svc.ChangeView;

export type GitChangesState =
  | { status: "loading" }
  | { status: "loaded"; view: workspace_fs_svc.GitChangesView }
  | { status: "error"; message: string };

type Params = {
  sessionId: number;
  /** 会话工作目录；空串表示没有工作目录，不打后端。 */
  cwd: string;
  /** 当前工作根的 root 实参（空串 = 会话 cwd）；两页共享同一个根。 */
  root: string;
  /**
   * 只有需要这份数据的页可见时才取数（决策 13：无手动刷新、也不在别的页后台
   * 拉）。「变更」页要它撑起「未提交」档与那一档的角标、并据 notARepo 决定
   * 那一档是否存在；「目录」页要它做状态叠加。两者要的是同一份「未提交」快照，
   * 所以共用一个 hook 实例、一个在途请求，切页不会重复打后端。
   */
  enabled: boolean;
};

export type GitChanges = {
  state: GitChangesState;
  /** 已加载且是 git 仓库时的变动文件数，用于 tab 角标；否则为 null。 */
  count: number | null;
  notARepo: boolean;
  reload: () => void;
  /**
   * 目录页叠加用的变动清单：非 git 仓库、读取失败或尚未加载时为 null——状态是
   * 增强信息，缺失时目录树只是不带叠加，不因此不可用。
   */
  overlayChanges: Change[] | null;
};

/**
 * useGitChanges 管「未提交」这份 git 快照的取数。数据是快照，在「需要它的页可见
 * 且无缓存」与「当前会话轮次结束」两个时机自动重拉（决策 13）——轮次结束是「文件
 * 可能变了」的唯一强信号，所以订阅 session-status-store 的 doneTick。
 *
 * 本轮之后它只有一个口径：工作区相对 HEAD（spec 决策 5 去掉了「本分支」档与任意
 * 基线选择），因此不再有 scope / baseRef / 分支清单。
 */
export function useGitChanges({
  sessionId,
  cwd,
  root,
  enabled,
}: Params): GitChanges {
  const [state, setState] = React.useState<GitChangesState>({
    status: "loading",
  });
  const [reloadTick, setReloadTick] = React.useState(0);

  // doneTick 每次本会话轮次结束自增一次；别的会话结束不会动它。
  const doneTick = useSessionStatus(sessionId)?.doneTick ?? 0;

  // 取数代际：会话变化后，先前在途的响应必须丢弃，否则慢的那一次会把新快照
  // 覆盖回旧数据。
  const genRef = React.useRef(0);

  // 换会话、换工作目录或换工作根时先把上一个仓库的快照丢掉：tab 角标读的是这里
  // 的文件数，而本页不可见时下面的取数 effect 不会跑，不清就会拿旧的数字。
  React.useEffect(() => {
    setState({ status: "loading" });
  }, [sessionId, cwd, root]);

  React.useEffect(() => {
    // 代际先于 return 自增：换到一个没有工作目录的会话、或切走到不取数的页时，
    // 此前在途的响应同样必须作废——否则它回来时代际仍然相等，会把上一个仓库的
    // 变动清单（连同 tab 角标与目录页的状态叠加）写到新会话上。
    genRef.current += 1;
    const gen = genRef.current;
    if (!enabled || cwd === "") return;
    setState({ status: "loading" });
    // 后两个参数是后端既有的 scope / baseRef 形状；本档恒定比 HEAD。
    WorkspaceFsGitChanges(sessionId, root, "uncommitted", "").then(
      (view) => {
        if (genRef.current !== gen) return;
        setState({ status: "loaded", view });
      },
      (err: unknown) => {
        if (genRef.current !== gen) return;
        setState({ status: "error", message: errorText(err) });
      },
    );
  }, [enabled, cwd, root, sessionId, doneTick, reloadTick]);

  const loaded = state.status === "loaded" ? state.view : null;
  const notARepo = loaded?.notARepo === true;

  return {
    state,
    count: loaded && !notARepo ? (loaded.changes?.length ?? 0) : null,
    notARepo,
    reload: () => setReloadTick((tick) => tick + 1),
    overlayChanges: loaded && !notARepo ? (loaded.changes ?? []) : null,
  };
}
