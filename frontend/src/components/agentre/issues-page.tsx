import * as React from "react";
import { Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  BOARD_STAGES,
  BoardFilterBar,
  Button,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  EMPTY_BOARD_QUERY,
  IssueBoard,
  LabelManagerPanel,
  ProjectScopePicker,
  TaskFormShell,
  initialTaskFormValue,
  type BoardAgentOption,
  type BoardCardProject,
  type BoardQuery,
  type BoardStage,
  type ExecPillContext,
  type LabelUsageView,
  type ScopeProjectNode,
  type TaskFormValue,
} from "@agentre-hub/agentre-ui";

import { useBoard } from "@/hooks/use-board";
import { useChatAgents, type AgentSlim } from "@/hooks/use-chat-agents";
import { useProjectList } from "@/hooks/use-project-list";

import { scopeShowsGlyphs } from "./board/board-wire";
import { BoardJoinNotice } from "./board/board-join-notice";
import { BoardExecTargetPill } from "./board/exec-target-pill";
import { BoardModelPill } from "./board/model-pill";
import { useBoardDrag } from "./board/use-board-drag";
import { agentIconNode } from "./primitives";

/** 任务表单里那三个执行字段：共享包的端口只画 pill，值留在宿主这一层。 */
type ExecSelection = {
  agentBackendId: number | null;
  llmProviderKey: string;
  llmModelKey: string;
};

/** 没有 Agent 就没有机器与模型 —— 三个字段一起空着，不留上一位的档。 */
const EMPTY_EXEC: ExecSelection = {
  agentBackendId: null,
  llmProviderKey: "",
  llmModelKey: "",
};

function IssuesPage() {
  const { t } = useTranslation();
  const [query, setQuery] = React.useState<BoardQuery>(EMPTY_BOARD_QUERY);
  const [editing, setEditing] = React.useState<TaskFormValue | null>(null);
  const [labelsOpen, setLabelsOpen] = React.useState(false);

  const { projects } = useProjectList();
  const { agents } = useChatAgents();

  const projectByID = React.useMemo(
    () => new Map(projects.map((project) => [project.id, project])),
    [projects],
  );

  // 卡片上画不画项目字形的判据是「当前范围里是否不止一个项目」；范围是某个含子项目
  // 的父项目时，子项目那些任务额外挂一枚「↳」把层级说出来。
  const showGlyphs = scopeShowsGlyphs(query.scope, projects);
  const board = useBoard(
    query,
    React.useCallback(
      (projectID: number): BoardCardProject | null => {
        if (!showGlyphs) return null;
        const project = projectByID.get(projectID);
        if (!project) return null;
        return {
          name: project.name,
          color: project.color,
          glyph: agentIconNode(project.icon),
          nested:
            query.scope.kind === "project" &&
            projectID !== query.scope.projectId,
        };
      },
      [projectByID, query.scope, showGlyphs],
    ),
  );

  const scopeProjects = React.useMemo<ScopeProjectNode[]>(
    () =>
      projects.map((project) => ({
        id: project.id,
        name: project.name,
        depth: project.depth ?? 0,
        color: project.color,
        glyph: agentIconNode(project.icon),
        unfinished: board.projectCountOf(project.id),
      })),
    [board, projects],
  );

  const agentByID = React.useMemo(
    () => new Map(agents.map((agent) => [agent.id, agent])),
    [agents],
  );

  const agentOptions = React.useMemo<BoardAgentOption[]>(
    () =>
      agents.map((agent) => ({
        id: agent.id,
        name: agent.name,
        color: agent.avatarColor,
      })),
    [agents],
  );

  const { bindings } = useBoardDrag(
    React.useCallback(
      (id: number, stage: BoardStage, afterID: number) => {
        void board.moveIssue(id, stage, afterID);
      },
      [board],
    ),
  );

  const openTask = React.useCallback(
    (cardId: number) => {
      const task = board.taskOf(cardId);
      if (task) setEditing(task);
    },
    [board],
  );

  const totals = board.viewModel.columns;
  const totalTasks = BOARD_STAGES.reduce(
    (sum, stage) => sum + (totals[stage]?.total ?? 0),
    0,
  );

  return (
    <main
      className="flex min-h-0 min-w-0 flex-1 flex-col bg-background"
      data-slot="issues-page"
    >
      <BoardJoinNotice />
      {/* 860px 是最小窗口宽度（`internal/desktop` 那一条）。窄到那里时项目选择器
          整条换到第二行占满宽度：三件东西挤一行会先把它压没。 */}
      <header className="flex min-h-[60px] shrink-0 flex-wrap items-center gap-3 border-b border-border bg-background px-5 py-3 min-[861px]:h-[60px] min-[861px]:flex-nowrap min-[861px]:py-0">
        <div className="flex min-w-0 flex-col gap-0.5">
          <h1 className="truncate text-base font-semibold tracking-normal">
            {t("issues.title")}
          </h1>
          <p className="truncate text-2xs text-muted-foreground">
            {t("issues.summary.counts", {
              total: totalTasks,
              doing: totals.doing?.total ?? 0,
            })}
          </p>
        </div>
        <div className="min-w-0 flex-1 max-[860px]:hidden" />
        <ProjectScopePicker
          scope={query.scope}
          projects={scopeProjects}
          unassignedCount={board.unassignedCount}
          onScopeChange={(scope) =>
            setQuery((current) => ({ ...current, scope }))
          }
          className="max-[860px]:order-last max-[860px]:w-full max-[860px]:max-w-none"
        />
        <div className="min-w-0 flex-1 max-[860px]:hidden" />
        <Button
          type="button"
          size="sm"
          className="h-[30px] max-[860px]:ml-auto"
          onClick={() =>
            setEditing(initialTaskFormValue({ scope: query.scope }))
          }
        >
          <Plus data-icon="inline-start" aria-hidden="true" />
          {t("issues.actions.newTask")}
        </Button>
      </header>

      <div className="flex min-h-12 shrink-0 items-center gap-2 overflow-x-auto border-b border-border bg-sidebar px-5 py-2">
        <BoardFilterBar
          query={query}
          labels={board.labels}
          projects={scopeProjects}
          matchedCount={
            board.viewModel.filtering ? board.matchedCount : undefined
          }
          searching={board.searching}
          ports={{ onQueryChange: setQuery }}
          onManageLabels={() => setLabelsOpen(true)}
          className="min-w-0 flex-1"
        />
      </div>

      {board.error ? (
        <p
          role="alert"
          className="shrink-0 bg-destructive-soft px-5 py-2 text-2xs text-destructive-text"
        >
          {board.error}
        </p>
      ) : null}

      <IssueBoard
        viewModel={board.viewModel}
        drag={bindings}
        ports={{
          onEdit: openTask,
          onDelete: (cardId) => void board.deleteTask(cardId),
          onMove: (cardId, stage) => void board.moveIssue(cardId, stage, 0),
          onCreateTask: (stage) =>
            setEditing(initialTaskFormValue({ stage, scope: query.scope })),
          onClearFilters: () => setQuery(EMPTY_BOARD_QUERY),
        }}
      />

      <TaskFormDialog
        value={editing}
        projects={scopeProjects}
        labels={board.labels}
        agentOptions={agentOptions}
        agentByID={agentByID}
        onClose={() => setEditing(null)}
        onSave={async (next) => {
          // 写失败时对话框留在原位：关掉它等于把用户刚填的东西连同那条报错一起丢掉。
          if (await board.saveTask(next)) setEditing(null);
        }}
        onDelete={async (id) => {
          if (await board.deleteTask(id)) setEditing(null);
        }}
      />

      <Dialog open={labelsOpen} onOpenChange={setLabelsOpen}>
        <DialogContent className="max-w-[420px]">
          <DialogHeader>
            <DialogTitle>{t("issues.labelsTitle")}</DialogTitle>
          </DialogHeader>
          <LabelManagerPanel
            labels={board.labels}
            onLabelMutate={board.mutateLabel}
            className="max-h-[60vh]"
          />
        </DialogContent>
      </Dialog>
    </main>
  );
}

type TaskFormDialogProps = {
  value: TaskFormValue | null;
  projects: ScopeProjectNode[];
  labels: LabelUsageView[];
  agentOptions: BoardAgentOption[];
  /** 模型那一颗要 Agent 自己的后端类型与绑定供应商，两者都只有宿主有。 */
  agentByID: Map<number, AgentSlim>;
  onClose: () => void;
  onSave: (value: TaskFormValue) => Promise<void>;
  onDelete: (id: number) => Promise<void>;
};

/**
 * 任务表单的宿主外壳。
 *
 * 壳本体是共享包的 `TaskFormShell`；留在这里的是它按定义要不到的两样东西 ——
 * 执行段的机器与模型两颗 pill（Wails 可用性判定 + 本机供应商目录），以及这两颗
 * pill 选出来的值：端口只负责画，所以三个执行字段里的后两个由这一层自己持有，
 * 提交时并回表单的值。
 */
function TaskFormDialog({
  value,
  projects,
  labels,
  agentOptions,
  agentByID,
  onClose,
  onSave,
  onDelete,
}: TaskFormDialogProps) {
  const [exec, setExec] = React.useState<ExecSelection>(EMPTY_EXEC);
  // 生效执行档的后端类型：机器那一颗解析出来往上报，模型那一颗拿它过兼容判据。
  // 空串 = 没选机器（「跟随 Agent 绑定」），这时才轮到 Agent 自己那个后端类型。
  const [execBackendType, setExecBackendType] = React.useState("");

  // 每次打开都从这条任务已有的值起步：上一条任务选的机器与它无关。
  const formKey = value ? (value.id ?? 0) : null;
  React.useEffect(() => {
    if (!value) return;
    setExec({
      agentBackendId: value.agentBackendId,
      llmProviderKey: value.llmProviderKey,
      llmModelKey: value.llmModelKey,
    });
    setExecBackendType("");
    // formKey 就是「这次打开的是哪一条」；value 每次渲染都是新对象。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [formKey]);

  const execTargetPort = React.useCallback(
    (ctx: ExecPillContext) => (
      <BoardExecTargetPill
        className={ctx.className}
        agentId={ctx.agentId}
        projectId={ctx.projectId}
        backendId={exec.agentBackendId}
        onChange={(agentBackendId) =>
          setExec((current) => ({ ...current, agentBackendId }))
        }
        onResolvedBackendType={setExecBackendType}
        disabled={ctx.disabled}
      />
    ),
    [exec.agentBackendId],
  );

  const modelTargetPort = React.useCallback(
    (ctx: ExecPillContext) => (
      <BoardModelPill
        className={ctx.className}
        // 生效档优先：选了机器就按那台机器的后端类型过兼容判据，「跟随 Agent
        // 绑定」时才退回 Agent 自己那个。拿 Agent 那个一路用到底的话，挑一台
        // codex 的机器，模型仍按 claudecode 筛。
        backendType={
          execBackendType || (agentByID.get(ctx.agentId)?.backendType ?? "")
        }
        boundProviderKey={agentByID.get(ctx.agentId)?.llmProviderKey}
        providerKey={exec.llmProviderKey}
        modelKey={exec.llmModelKey}
        onChange={(target) =>
          setExec((current) => ({
            ...current,
            llmProviderKey: target.providerKey ?? "",
            llmModelKey: target.modelKey ?? "",
          }))
        }
        disabled={ctx.disabled}
      />
    ),
    [agentByID, execBackendType, exec.llmModelKey, exec.llmProviderKey],
  );

  if (!value) return null;

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="max-w-[640px] p-0" showCloseButton={false}>
        <TaskFormShell
          initial={value}
          projects={projects}
          labels={labels}
          agentOptions={agentOptions}
          execTargetPort={execTargetPort}
          modelTargetPort={modelTargetPort}
          onClose={onClose}
          onDelete={
            value.id ? () => void onDelete(value.id as number) : undefined
          }
          // 没有 Agent 时那两颗 pill 是禁用态、端口根本没被调用，此刻的 exec 只
          // 可能是换 Agent 之前留下的：跟着存下去等于记了一件从没成立过的事。
          onSave={(next) =>
            onSave({ ...next, ...(next.assigneeAgentId ? exec : EMPTY_EXEC) })
          }
          className="max-h-[75vh]"
        />
      </DialogContent>
    </Dialog>
  );
}

export { IssuesPage };
