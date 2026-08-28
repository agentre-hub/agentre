import * as React from "react";
import { ChevronRight, MoreHorizontal, Trash2, X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { formatRelativeTime } from "../lib/relative-time";
import { cn } from "../lib/utils";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { Input } from "../ui/input";
import { Spinner } from "../ui/spinner";
import { Textarea } from "../ui/textarea";

import { TaskExecPills } from "./exec-pills";
import {
  TaskLabelChips,
  TaskProjectPill,
  TaskStagePill,
} from "./task-form-pills";
import { useTaskForm } from "./use-task-form";
import type {
  BoardAgentOption,
  ExecTargetPort,
  ModelTargetPort,
} from "./exec-ports";
import type {
  LabelUsageView,
  ScopeProjectNode,
  TaskFormValue,
} from "./query-types";

export interface TaskFormShellProps {
  /** 初值（含从列与范围继承来的默认值）；见 `initialTaskFormValue`。 */
  initial: TaskFormValue;
  projects: ScopeProjectNode[];
  labels: LabelUsageView[];
  agentOptions: BoardAgentOption[];
  /** 机器与模型两颗 pill 的实现留在宿主，只经端口进来。 */
  execTargetPort?: ExecTargetPort;
  modelTargetPort?: ModelTargetPort;
  onSave: (value: TaskFormValue) => Promise<void> | void;
  onClose?: () => void;
  /** 编辑态才给：删除收在头部的更多菜单里。 */
  onDelete?: () => void;
  /** 相对时间的「现在」；不给就取 `Date.now()`。 */
  nowMs?: number;
  className?: string;
}

/**
 * 新建与编辑**同一个壳**。
 *
 * 主体只有标题与描述，两者都不画输入框边框、靠字号分层级；属性是正文底部的一排
 * pill，不写字段名。头部是面包屑而不是标题 —— 「新建任务」这四个字当标题占掉一整
 * 行，却没说这条任务落在哪。
 *
 * 界面不显示任何快捷键提示。
 */
export function TaskFormShell({
  initial,
  projects,
  labels,
  agentOptions,
  execTargetPort,
  modelTargetPort,
  onSave,
  onClose,
  onDelete,
  nowMs,
  className,
}: TaskFormShellProps) {
  const { t } = useUiTranslation();
  const form = useTaskForm(initial, onSave);
  const { value, patch, submitting } = form;
  const project = projects.find((item) => item.id === value.projectId);
  // 宿主喂一个走动的 nowMs 时相对时间跟着跳；不给就锚在表单打开那一刻 —— 渲染期
  // 直接调 Date.now() 是不纯的（同一次渲染两次调用会给出两个答案）。
  const [openedAt] = React.useState(Date.now);
  const resolvedNow = nowMs ?? openedAt;

  return (
    <form
      data-testid="task-form"
      onSubmit={(event) => {
        event.preventDefault();
        void form.submit();
      }}
      className={cn("flex min-h-0 flex-col", className)}
    >
      <header className="flex shrink-0 items-center gap-2 border-b border-border-strong px-4 py-2.5">
        <nav
          data-testid="task-form-breadcrumb"
          aria-label={t("board.form.breadcrumb")}
          className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground"
        >
          <span className="truncate">
            {project?.name ?? t("board.scope.unassigned")}
          </span>
          <ChevronRight className="size-3 shrink-0" aria-hidden="true" />
          <span className="shrink-0 font-medium text-foreground">
            {value.id ? `#${value.id}` : t("board.form.newTask")}
          </span>
        </nav>
        <div className="ml-auto flex shrink-0 items-center gap-1">
          {value.updatedAt ? (
            <span
              data-testid="task-form-updated"
              className="text-2xs text-muted-foreground"
            >
              {formatRelativeTime(value.updatedAt, resolvedNow, t)}
            </span>
          ) : null}
          {onDelete ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  type="button"
                  data-testid="task-form-more"
                  aria-label={t("board.form.more")}
                  className="cursor-pointer rounded-md p-1 text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40"
                >
                  <MoreHorizontal className="size-4" aria-hidden="true" />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem
                  data-testid="task-form-delete"
                  variant="destructive"
                  onSelect={onDelete}
                >
                  <Trash2 className="size-3.5" aria-hidden="true" />
                  {t("board.delete")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : null}
          {onClose ? (
            <button
              type="button"
              data-testid="task-form-close"
              aria-label={t("board.form.close")}
              onClick={onClose}
              className="cursor-pointer rounded-md p-1 text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40"
            >
              <X className="size-4" aria-hidden="true" />
            </button>
          ) : null}
        </div>
      </header>

      <div
        data-testid="task-form-body"
        className="flex min-h-0 flex-1 flex-col gap-1 px-4 py-3"
      >
        <Input
          data-testid="task-title"
          value={value.title}
          readOnly={submitting}
          onChange={(event) => patch({ title: event.target.value })}
          placeholder={t("board.form.titlePlaceholder")}
          aria-label={t("board.form.title")}
          className="h-auto border-0 bg-transparent px-0 py-1 text-base font-semibold shadow-none focus-visible:ring-0"
        />
        <Textarea
          data-testid="task-description"
          value={value.description}
          readOnly={submitting}
          onChange={(event) => patch({ description: event.target.value })}
          placeholder={t("board.form.descriptionPlaceholder")}
          aria-label={t("board.form.description")}
          className="min-h-32 flex-1 resize-none border-0 bg-transparent px-0 text-sm shadow-none focus-visible:ring-0"
        />
      </div>

      <div
        data-testid="task-form-pills"
        className="flex shrink-0 flex-wrap items-center gap-1.5 px-4 py-2"
      >
        <TaskStagePill
          stage={value.stage}
          onChange={(stage) => patch({ stage })}
          disabled={submitting}
        />
        <TaskProjectPill
          projectId={value.projectId}
          projects={projects}
          onChange={(projectId) => patch({ projectId })}
          disabled={submitting}
        />
        <TaskLabelChips
          labelIds={value.labelIds}
          labels={labels}
          onChange={(labelIds) => patch({ labelIds })}
          disabled={submitting}
        />
        {/* 左边说「这条任务是什么」，右边说「谁在哪台机器上用什么模型做它」。 */}
        <span
          data-testid="task-pill-divider"
          aria-hidden="true"
          className="mx-1 h-5 w-px shrink-0 bg-border-strong"
        />
        <TaskExecPills
          agentId={value.assigneeAgentId}
          projectId={value.projectId}
          backendId={value.agentBackendId}
          agentOptions={agentOptions}
          onAgentChange={(assigneeAgentId) =>
            // 换 Agent 后机器与模型要重新解析：留着上一位的档等于说谎。
            patch({ assigneeAgentId, agentBackendId: null })
          }
          execTargetPort={execTargetPort}
          modelTargetPort={modelTargetPort}
          disabled={submitting}
        />
      </div>

      <footer
        data-testid="task-form-footer"
        className="flex shrink-0 flex-col gap-2 border-t border-border-strong px-4 py-3"
      >
        {/* 错误块在提交按钮**正上方**：摆在表单末尾时，长表单里失败在视野之外。 */}
        {form.error ? (
          <p
            data-testid="task-form-error"
            role="alert"
            className="rounded-md bg-destructive-soft px-2.5 py-1.5 text-2xs text-destructive-text"
          >
            {form.error}
          </p>
        ) : null}
        <div className="flex items-center justify-end gap-2">
          {onClose ? (
            <button
              type="button"
              data-testid="task-form-cancel"
              onClick={onClose}
              disabled={submitting}
              className="h-8 cursor-pointer rounded-lg px-3 text-xs text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {t("board.form.cancel")}
            </button>
          ) : null}
          <button
            type="submit"
            data-testid="task-form-submit"
            disabled={!form.canSubmit}
            className="inline-flex h-8 cursor-pointer items-center gap-1.5 rounded-lg bg-primary px-3 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {submitting ? <Spinner className="size-3" /> : null}
            {value.id ? t("board.form.save") : t("board.form.create")}
          </button>
        </div>
      </footer>
    </form>
  );
}
