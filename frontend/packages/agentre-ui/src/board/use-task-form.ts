import * as React from "react";

import type { ProjectScope, TaskFormValue } from "./query-types";
import type { BoardStage } from "./types";

export interface InitialTaskFormInput {
  /** 编辑态：这条任务已有的值，优先于任何上下文默认。 */
  issue?: Partial<TaskFormValue>;
  /** 从某一列的「+」进来：阶段预置为该列。 */
  stage?: BoardStage;
  /** 看板当前范围：是某个具体项目时，项目预置为它。 */
  scope?: ProjectScope;
}

/**
 * 表单的初值。**默认值跟随上下文**：阶段来自那一列，项目来自当前范围 —— 从「进行
 * 中」那一列的 + 建出来的任务落在待办里，是每次都要手动改回去的一步。
 */
export function initialTaskFormValue({
  issue,
  stage,
  scope,
}: InitialTaskFormInput): TaskFormValue {
  const scopeProject = scope?.kind === "project" ? scope.projectId : null;

  return {
    id: issue?.id,
    title: issue?.title ?? "",
    description: issue?.description ?? "",
    stage: issue?.stage ?? stage ?? "todo",
    projectId: issue?.projectId ?? scopeProject,
    labelIds: issue?.labelIds ?? [],
    assigneeAgentId: issue?.assigneeAgentId ?? null,
    agentBackendId: issue?.agentBackendId ?? null,
    llmProviderKey: issue?.llmProviderKey ?? "",
    llmModelKey: issue?.llmModelKey ?? "",
    updatedAt: issue?.updatedAt,
  };
}

export interface UseTaskFormResult {
  value: TaskFormValue;
  patch: (partial: Partial<TaskFormValue>) => void;
  /** 提交在途：按钮里转圈，其余字段可读不可改。 */
  submitting: boolean;
  /** 上一次提交的失败原因；错误块画在提交按钮正上方。 */
  error: string | null;
  submit: () => Promise<void>;
  /** 标题是唯一必填。 */
  canSubmit: boolean;
}

/**
 * 表单**唯一**的一支 hook：值、提交态与错误都在这里，外壳与几排 pill 只收结果。
 */
export function useTaskForm(
  initial: TaskFormValue,
  onSave: (value: TaskFormValue) => Promise<void> | void,
): UseTaskFormResult {
  const [value, setValue] = React.useState(initial);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const patch = React.useCallback((partial: Partial<TaskFormValue>) => {
    setValue((current) => ({ ...current, ...partial }));
  }, []);

  const submit = React.useCallback(async () => {
    if (!value.title.trim() || submitting) return;
    setSubmitting(true);
    setError(null);
    try {
      await onSave(value);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setSubmitting(false);
    }
  }, [onSave, submitting, value]);

  return {
    value,
    patch,
    submitting,
    error,
    submit,
    canSubmit: value.title.trim().length > 0 && !submitting,
  };
}
