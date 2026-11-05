import * as React from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import {
  type BoardEmptyScope,
  type BoardQuery,
  type BoardStage,
  type BoardViewModel,
  type LabelMutation,
  type LabelUsageView,
  type TaskFormValue,
} from "@agentre-hub/agentre-ui";

import {
  isFiltering,
  matchedTotal,
  projectCountOf,
  toBoardColumns,
  toIssueListRequest,
  toTaskFormValue,
  type BoardCardProjectResolver,
} from "@/components/agentre/board/board-wire";
import { classifyAppError, type AppErrorKind } from "@/lib/app-error";

import {
  IssueCreate,
  IssueCreateLabel,
  IssueDelete,
  IssueDeleteLabel,
  IssueList,
  IssueListLabels,
  IssueMove,
  IssueUpdate,
  IssueUpdateLabel,
} from "../../wailsjs/go/app/App";
import type { app } from "../../wailsjs/go/models";

export interface UseBoardResult {
  viewModel: BoardViewModel;
  labels: LabelUsageView[];
  /** 项目选择器每一项右侧的子树未完成数（不随筛选变）。 */
  projectCountOf: (projectID: number) => number;
  /** 「未归属」那一项的计数；0 = 该入口不出现。 */
  unassignedCount: number;
  /** 搜索框右侧那个命中数。 */
  matchedCount: number;
  /** 这一条任务摊回表单要编辑的那些字段；不在当前结果里就是 `null`。 */
  taskOf: (id: number) => TaskFormValue | null;
  /** 取数在途；旧结果留在原地，只有输入框右端那枚转圈在动。 */
  searching: boolean;
  error: BoardError | null;
  reload: () => Promise<void>;
  /**
   * 四条写路径**都不会 reject**：失败就地记进 `error`（页面上那条 role="alert"
   * 就是它），并回 `false`。调用方据此决定关不关对话框——把 rejection 交给调用方
   * 的话，`void board.deleteTask(id)` 这一类调用会变成一次没人接的未处理拒绝，
   * 卡片弹回原位而用户什么都看不到。
   */
  moveIssue: (
    id: number,
    stage: BoardStage,
    afterID: number,
  ) => Promise<boolean>;
  saveTask: (value: TaskFormValue) => Promise<boolean>;
  deleteTask: (id: number) => Promise<boolean>;
  mutateLabel: (mutation: LabelMutation) => Promise<boolean>;
}

const EMPTY_RESPONSE = {
  issues: [],
  stageCounts: {},
  stageTotals: {},
  projectCounts: [],
} as unknown as app.IssueListResponse;

/** 删除的撤销窗口：toast 在这段时间里挂着，到点才真的 IssueDelete。 */
const DELETE_UNDO_MS = 6000;

/**
 * 看板失败的呈现形状（S5）：`title` 是 i18n key 之外的中文事实句已经渲染好的文本，
 * `message` 是后端按语言渲染的那句业务文案（可能为空），`detail` 是机器原话，
 * 页面只把它放进默认折叠的「详情」。
 */
export interface BoardError {
  kind: AppErrorKind;
  title: string;
  message: string;
  detail?: string;
}

/**
 * 看板的**唯一**取数口：六个筛选条件翻成一次 `IssueList`，回来的响应摊成共享呈现件
 * 吃的视图模型；建 / 改 / 移 / 删与标签增删改从这里出去，各自写完就地重拉。
 *
 * `query` 每次渲染都是新对象（宿主持有的 state），所以取数的依赖是它**序列化后的
 * 值**而不是引用 —— 按引用挂依赖等于每帧重拉一次。
 */
export function useBoard(
  query: BoardQuery,
  projectOf: BoardCardProjectResolver,
): UseBoardResult {
  const [response, setResponse] = React.useState(EMPTY_RESPONSE);
  const [labels, setLabels] = React.useState<LabelUsageView[]>([]);
  const [loaded, setLoaded] = React.useState(false);
  const [searching, setSearching] = React.useState(false);
  const [error, setError] = React.useState<BoardError | null>(null);
  const { t } = useTranslation();
  // 失败翻成中文事实：已经不在了 / 连不上各说各的，其余统一「没保存」；
  // 取数失败单独一句。机器原话只留在 detail。
  const toBoardError = React.useCallback(
    (cause: unknown, fallbackTitle: string): BoardError => {
      const failure = classifyAppError(cause);
      const title =
        failure.kind === "notFound"
          ? t("issues.errors.notFound")
          : failure.kind === "offline"
            ? t("issues.errors.offline")
            : t(fallbackTitle);
      return { ...failure, title };
    },
    [t],
  );
  // 已点删除但还没落库的卡片：视图模型把它们滤掉，撤销就是把它们放回来。
  const [pendingDeletes, setPendingDeletes] = React.useState<
    ReadonlySet<number>
  >(new Set());

  // 只有最新一次请求可以写状态：连打几个字会让多次取数重叠，先发的那次晚返回时
  // 不能把新结果盖回旧的。
  const requestRef = React.useRef(0);
  const queryKey = JSON.stringify(query);
  // 取数要用的是**最新**的 query，但它不能进 reload 的依赖数组：宿主每次渲染新建
  // 一个 query 对象就会让 reload 换身份、effect 再跑一遍，等于每帧重拉。
  const queryRef = React.useRef(query);
  React.useEffect(() => {
    queryRef.current = query;
  });

  const reload = React.useCallback(async () => {
    const request = ++requestRef.current;
    setSearching(true);
    try {
      const [board, labelList] = await Promise.all([
        IssueList(toIssueListRequest(queryRef.current, Date.now())),
        IssueListLabels(),
      ]);
      if (request !== requestRef.current) return;
      setResponse(board ?? EMPTY_RESPONSE);
      setLabels(
        (labelList ?? []).map((label) => ({
          id: label.id,
          name: label.name,
          tone: label.tone as LabelUsageView["tone"],
          usageCount: label.usageCount,
        })),
      );
      setError(null);
    } catch (cause) {
      if (request !== requestRef.current) return;
      setError(toBoardError(cause, "issues.errors.loadFailed"));
    } finally {
      if (request === requestRef.current) {
        setSearching(false);
        setLoaded(true);
      }
    }
    // queryKey 是 query 的**值**：宿主每帧新建的那个对象不该触发重拉。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queryKey]);

  React.useEffect(() => {
    void reload();
  }, [reload]);

  React.useEffect(
    () => () => {
      requestRef.current++;
    },
    [],
  );

  // 待删卡片不只从列表里滤掉，也要从各列总数 / 命中数里扣掉，头部计数才与列表一致；
  // 撤销后 pendingDeletes 清空，原始计数原样回来。
  const visibleResponse = React.useMemo(() => {
    if (pendingDeletes.size === 0) return response;
    const issues = response.issues ?? [];
    const stageTotals = { ...(response.stageTotals ?? {}) };
    const stageCounts = { ...(response.stageCounts ?? {}) };
    for (const issue of issues) {
      if (!pendingDeletes.has(issue.id)) continue;
      if (stageTotals[issue.stage] !== undefined)
        stageTotals[issue.stage] = Math.max(0, stageTotals[issue.stage] - 1);
      if (stageCounts[issue.stage] !== undefined)
        stageCounts[issue.stage] = Math.max(0, stageCounts[issue.stage] - 1);
    }
    return {
      ...response,
      issues: issues.filter((issue) => !pendingDeletes.has(issue.id)),
      stageTotals,
      stageCounts,
    } as app.IssueListResponse;
  }, [pendingDeletes, response]);

  const viewModel = React.useMemo<BoardViewModel>(() => {
    // 空态那句的范围：`unassigned` 是范围本身说死的；`project` 的名字从
    // `projectOf` 取，取不到（例如宿主为别的判据让它返回 null）就不给 scope，
    // 退化为泛化文案 —— 宁可不点名，也不编一个名字。
    const scope = query.scope;
    const scopedProject =
      scope.kind === "project" ? projectOf(scope.projectId) : null;
    const emptyScope: BoardEmptyScope | undefined =
      scope.kind === "unassigned"
        ? { kind: "unassigned" }
        : scopedProject
          ? { kind: "project", name: scopedProject.name }
          : undefined;

    return {
      columns: toBoardColumns(visibleResponse, projectOf),
      filtering: isFiltering(query),
      keyword: query.keyword.trim(),
      loading: !loaded,
      emptyScope,
    };
  }, [loaded, projectOf, query, visibleResponse]);

  const taskOf = React.useCallback(
    (id: number) => {
      const issue = (response.issues ?? []).find((row) => row.id === id);
      return issue ? toTaskFormValue(issue) : null;
    },
    [response.issues],
  );

  // 每条写路径都经过它：写失败当场记进 error 并回 false，绝不把 rejection 抛给
  // 调用方（见 UseBoardResult 上的说明）。写成功后就地重拉。
  const write = React.useCallback(
    async (run: () => Promise<unknown>) => {
      try {
        await run();
        setError(null);
      } catch (cause) {
        setError(toBoardError(cause, "issues.errors.writeFailed"));
        return false;
      }
      await reload();
      return true;
    },
    [reload, toBoardError],
  );

  const moveIssue = React.useCallback(
    (id: number, stage: BoardStage, afterID: number) =>
      write(() => IssueMove({ id, stage, afterID })),
    [write],
  );

  const saveTask = React.useCallback(
    (value: TaskFormValue) => {
      const shared = {
        projectID: value.projectId ?? 0,
        title: value.title.trim(),
        body: value.description,
        labelIDs: value.labelIds,
        stage: value.stage,
        assigneeAgentID: value.assigneeAgentId ?? 0,
        agentBackendID: value.agentBackendId ?? 0,
        llmProviderKey: value.llmProviderKey,
        llmModelKey: value.llmModelKey,
      };
      return write(() =>
        value.id
          ? IssueUpdate({ id: value.id, ...shared })
          : IssueCreate(shared),
      );
    },
    [write],
  );

  // 撤销窗口内的删除：id → 定时器与 toast。落库（到点或卸载）后条目即删，
  // 之后再点「撤销」什么都不会发生。
  const pendingRef = React.useRef(
    new Map<
      number,
      { timer: ReturnType<typeof setTimeout>; toastId: string | number }
    >(),
  );

  const releasePending = React.useCallback((id: number) => {
    setPendingDeletes((prev) => {
      if (!prev.has(id)) return prev;
      const next = new Set(prev);
      next.delete(id);
      return next;
    });
  }, []);

  const commitDelete = React.useCallback(
    async (id: number) => {
      const entry = pendingRef.current.get(id);
      if (!entry) return;
      clearTimeout(entry.timer);
      pendingRef.current.delete(id);
      toast.dismiss(entry.toastId);
      // 写成功并重拉后才放开隐藏，否则卡片会在重拉回来前闪一下；失败则放开后
      // 卡片回到原位，报错由 write 记进 error。
      await write(() => IssueDelete(id));
      releasePending(id);
    },
    [releasePending, write],
  );

  const undoDelete = React.useCallback(
    (id: number) => {
      const entry = pendingRef.current.get(id);
      if (!entry) return;
      clearTimeout(entry.timer);
      pendingRef.current.delete(id);
      releasePending(id);
    },
    [releasePending],
  );

  const deleteTask = React.useCallback(
    async (id: number) => {
      if (pendingRef.current.has(id)) return true;
      const title =
        (response.issues ?? []).find((issue) => issue.id === id)?.title ?? "";
      const timer = setTimeout(() => void commitDelete(id), DELETE_UNDO_MS);
      const toastId = toast(t("issues.deleted", { title }), {
        duration: DELETE_UNDO_MS,
        action: {
          label: t("issues.undo"),
          onClick: () => undoDelete(id),
        },
      });
      pendingRef.current.set(id, { timer, toastId });
      setPendingDeletes((prev) => new Set(prev).add(id));
      return true;
    },
    [commitDelete, response.issues, t, undoDelete],
  );

  // 卸载或窗口关闭前立即提交未决删除：不能让用户以为删了、实际没删。关窗 / 退出
  // 应用不会卸载 React，只会发 beforeunload / pagehide，所以两条路都接上。
  React.useEffect(() => {
    const pending = pendingRef.current;
    const flush = () => {
      for (const [id, entry] of pending) {
        clearTimeout(entry.timer);
        toast.dismiss(entry.toastId);
        void Promise.resolve(IssueDelete(id)).catch(() => undefined);
      }
      pending.clear();
    };
    window.addEventListener("beforeunload", flush);
    window.addEventListener("pagehide", flush);
    return () => {
      window.removeEventListener("beforeunload", flush);
      window.removeEventListener("pagehide", flush);
      flush();
    };
  }, []);

  const mutateLabel = React.useCallback(
    (mutation: LabelMutation) =>
      write(() => {
        switch (mutation.kind) {
          case "create":
            return IssueCreateLabel({
              id: 0,
              name: mutation.name,
              tone: mutation.tone,
            });
          case "update":
            return IssueUpdateLabel({
              id: mutation.id,
              name: mutation.name,
              tone: mutation.tone,
            });
          case "delete":
            return IssueDeleteLabel(mutation.id);
        }
      }),
    [write],
  );

  return {
    viewModel,
    labels,
    projectCountOf: React.useCallback(
      (projectID: number) => projectCountOf(response.projectCounts, projectID),
      [response.projectCounts],
    ),
    unassignedCount: projectCountOf(response.projectCounts, 0),
    matchedCount: matchedTotal(visibleResponse.stageCounts ?? {}),
    taskOf,
    searching,
    error,
    reload,
    moveIssue,
    saveTask,
    deleteTask,
    mutateLabel,
  };
}
