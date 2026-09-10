import { Bot } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { AgentAvatar } from "../ui/agent-avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";

import { TASK_PILL_CLASS } from "./task-form-pills";
import type {
  BoardAgentOption,
  ExecTargetPort,
  ModelTargetPort,
} from "./exec-ports";

export interface TaskExecPillsProps {
  agentId: number | null;
  projectId: number | null;
  backendId: number | null;
  agentOptions: BoardAgentOption[];
  onAgentChange: (agentId: number | null) => void;
  execTargetPort?: ExecTargetPort;
  modelTargetPort?: ModelTargetPort;
  disabled?: boolean;
}

/** 未选 Agent 时那两颗禁用 pill：形状照旧在，只是按不动。 */
function DisabledPill({ testId, label }: { testId: string; label: string }) {
  return (
    <button
      type="button"
      data-testid={testId}
      disabled
      className={TASK_PILL_CLASS}
    >
      <span className="truncate text-muted-foreground">{label}</span>
    </button>
  );
}

/**
 * 「谁在哪台机器上用什么模型做它」。
 *
 * 三者有**由接口本身决定的依赖顺序**：执行目标要 `(agentId, projectId)` 才算得出
 * 候选，模型要用生效档的 backendType 过兼容判据。所以未选 Agent 时后两颗是禁用态，
 * 端口**根本不被调用** —— 让宿主拿着 `agentId: null` 去解析一遍再自己发现解不出来，
 * 等于把这条顺序在两端各写一遍。
 */
export function TaskExecPills({
  agentId,
  projectId,
  backendId,
  agentOptions,
  onAgentChange,
  execTargetPort,
  modelTargetPort,
  disabled,
}: TaskExecPillsProps) {
  const { t } = useUiTranslation();
  const agent = agentOptions.find((option) => option.id === agentId);
  const context = {
    className: TASK_PILL_CLASS,
    agentId: agentId ?? 0,
    projectId,
    disabled,
  };

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            data-testid="task-pill-agent"
            disabled={disabled}
            className={TASK_PILL_CLASS}
          >
            {agent ? (
              <AgentAvatar
                name={agent.name}
                color={agent.color}
                size="xs"
                className="size-3.5 shrink-0"
              />
            ) : (
              <Bot
                className="size-3 shrink-0 text-muted-foreground"
                aria-hidden="true"
              />
            )}
            <span className={cn("truncate", !agent && "text-muted-foreground")}>
              {agent?.name ?? t("board.form.noAgent")}
            </span>
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="max-h-64 overflow-y-auto">
          <DropdownMenuItem
            data-testid="task-agent-none"
            onSelect={() => onAgentChange(null)}
          >
            {t("board.form.noAgent")}
          </DropdownMenuItem>
          {agentOptions.map((option) => (
            <DropdownMenuItem
              key={option.id}
              data-testid={`task-agent-${option.id}`}
              onSelect={() => onAgentChange(option.id)}
            >
              <AgentAvatar
                name={option.name}
                color={option.color}
                size="xs"
                className="size-4 shrink-0"
              />
              {option.name}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      {agentId && execTargetPort ? (
        execTargetPort({ ...context, agentId })
      ) : (
        <DisabledPill
          testId="exec-target-pill"
          label={t("board.form.machine")}
        />
      )}
      {agentId && modelTargetPort ? (
        modelTargetPort({ ...context, agentId, backendId })
      ) : (
        <DisabledPill testId="model-pill" label={t("board.form.model")} />
      )}
    </>
  );
}
