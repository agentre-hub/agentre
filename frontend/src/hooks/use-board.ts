import * as React from "react";

import {
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
  error: string | null;
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

function reasonOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
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
  const [error, setError] = React.useState<string | null>(null);

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
      setError(reasonOf(cause));
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

  const viewModel = React.useMemo<BoardViewModel>(
    () => ({
      columns: toBoardColumns(response, projectOf),
      filtering: isFiltering(query),
      keyword: query.keyword.trim(),
      loading: !loaded,
    }),
    [loaded, projectOf, query, response],
  );

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
        setError(reasonOf(cause));
        return false;
      }
      await reload();
      return true;
    },
    [reload],
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

  const deleteTask = React.useCallback(
    (id: number) => write(() => IssueDelete(id)),
    [write],
  );

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
    matchedCount: matchedTotal(response.stageCounts ?? {}),
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
