import * as React from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  Check,
  ChevronRight,
  CircleHelp,
  CircleSlash,
  Clock,
  LoaderCircle,
  Square,
  TriangleAlert,
  Users,
  Wrench,
} from "lucide-react";
import {
  shouldIgnoreClickForSelection,
  useCollapsible,
  CollapsibleCode,
  TranscriptCard,
  TranscriptCardHeader,
  TranscriptPill,
  useTranscriptBooleanState,
} from "@agentre-ai/agentre-ui";

import { cn } from "@/lib/utils";
import type { ChatBlockData } from "@/stores/chat-streams-store";

import { ActivityBlock } from "../../activity-block/block";
import type { PendingOutcome } from "../../activity-block/facts";
import { summarizeActivity, type ActivityStep } from "../../transcript-rows";
import { statusConfig, type AgentStatus } from "../../types";
import type { AgentSpawnChildBlocks, CanonicalCardProps } from "../props";
import type {
  AgentSpawnDTO,
  AgentSpawnMode,
  AgentSpawnRunDTO,
  AgentSpawnStatus,
  CanonicalDTO,
} from "../types";

// readSpawn 合并 canonical.agentSpawn(translator 算的静态字段:description/subagentType/
// prompt/taskId)与 toolBlock.subagent(SubagentStarted/Progress/Done 经 mergeSubagentMeta
// 累积的运行时字段:toolUses/totalTokens/durationMs/status/lastToolName)。零值不覆盖,避免
// 早到的空 meta 把已有进度抹掉。
function readSpawn(toolBlock: ChatBlockData): AgentSpawnDTO | undefined {
  const c = (toolBlock as { canonical?: CanonicalDTO }).canonical;
  if (!c || c.kind !== "agent.spawn") return undefined;
  const base = c.agentSpawn;
  if (!base) return undefined;
  const meta = toolBlock.subagent;
  if (!meta) return base;
  return {
    ...base,
    lastToolName: meta.lastToolName || base.lastToolName,
    toolUses: meta.toolUses ?? base.toolUses,
    totalTokens: meta.totalTokens ?? base.totalTokens,
    durationMs: meta.durationMs ?? base.durationMs,
    status: narrowSpawnStatus(meta.status) ?? base.status,
    mode: narrowSpawnMode(meta.mode) ?? base.mode,
    runs: mergeSpawnRuns(
      base.runs,
      meta.runs as AgentSpawnRunDTO[] | undefined,
    ),
    // 子代理首帧的实际模型覆盖派遣瞬间的入参别名(R2);两者都缺时 model 仍是 undefined。
    model: meta.model || base.model,
  };
}

function mergeSpawnRuns(
  declared: AgentSpawnRunDTO[] | undefined,
  snapshot: AgentSpawnRunDTO[] | undefined,
): AgentSpawnRunDTO[] | undefined {
  if (snapshot === undefined) return declared;
  const declaredByID = new Map((declared ?? []).map((run) => [run.id, run]));
  const declaredByIndex = new Map(
    (declared ?? []).map((run) => [run.index, run]),
  );
  return snapshot
    .map((run) => ({
      ...(declaredByID.get(run.id) ?? declaredByIndex.get(run.index) ?? {}),
      ...run,
    }))
    .sort((a, b) => a.index - b.index);
}

// shortenModelName 归一化模型短名供头部徽标显示(R7):只剥 "claude-" 前缀与结尾
// 8 位日期段,其余字符原样保留。第三方模型名(网关接入的任意串,如 glm-4.6)不含
// 这两种模式,原样返回——裁剪规则不做进一步"美化",避免把不认识的名字改坏。
// 展开区 meta 行使用未经裁剪的原值,不调用这个函数。
export function shortenModelName(model: string): string {
  const withoutPrefix = model.startsWith("claude-")
    ? model.slice("claude-".length)
    : model;
  return withoutPrefix.replace(/-\d{8}$/, "");
}

function narrowSpawnStatus(
  s: string | undefined,
): AgentSpawnDTO["status"] | undefined {
  return s === "waiting" ||
    s === "running" ||
    s === "completed" ||
    s === "failed" ||
    s === "canceled" ||
    s === "skipped" ||
    s === "unknown" ||
    s === "partial"
    ? s
    : undefined;
}

function narrowSpawnMode(s: string | undefined): AgentSpawnMode | undefined {
  return s === "single" || s === "parallel" || s === "chain" ? s : undefined;
}

type StepRow = { tool: ChatBlockData; result?: ChatBlockData };

function pairChildBlocks(blocks: ChatBlockData[]): StepRow[] {
  const steps: StepRow[] = [];
  const byId = new Map<string, number>();
  for (const b of blocks) {
    if (b.type === "tool_use" && b.toolUseId) {
      steps.push({ tool: b });
      byId.set(b.toolUseId, steps.length - 1);
    } else if (b.type === "tool_result" && b.toolUseId) {
      const idx = byId.get(b.toolUseId);
      if (idx !== undefined) {
        steps[idx].result = b;
      }
    }
  }
  return steps;
}

function statusFromSpawn(
  spawn: AgentSpawnDTO,
  resultBlock: ChatBlockData | undefined,
): AgentStatus {
  if (resultBlock?.isError || spawn.status === "failed") return "error";
  if (spawn.status === "running") return "running";
  if (spawn.status === "waiting") return "waiting";
  if (spawn.status) return "idle";
  if (resultBlock) return "idle";
  return "running";
}

// buildStatusLabel: cancelled 走 idle 通道但 label 单独显示 STOPPED,避免和正常
// 完成的 DONE 混淆。
function buildStatusLabel(
  status: AgentStatus,
  spawnStatus: AgentSpawnDTO["status"] | undefined,
  t: TFunction,
  durationMs?: number,
): string {
  const dur = durationMs ? formatDuration(durationMs) : "";
  if (spawnStatus === "canceled") {
    return dur
      ? t("canonical.agentSpawn.status.withDuration", {
          status: t("canonical.agentSpawn.status.stopped"),
          duration: dur,
        })
      : t("canonical.agentSpawn.status.stopped");
  }
  const label =
    status === "error"
      ? t("canonical.agentSpawn.status.error")
      : status === "running" || status === "waiting"
        ? t("canonical.agentSpawn.status.running")
        : t("canonical.agentSpawn.status.done");
  switch (status) {
    case "error":
    case "running":
    case "waiting":
    default:
      return dur
        ? t("canonical.agentSpawn.status.withDuration", {
            status: label,
            duration: dur,
          })
        : label;
  }
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  const m = Math.floor(s / 60);
  const rem = Math.round(s % 60);
  return `${m}m${rem.toString().padStart(2, "0")}s`;
}

// formatTokenCount 是纯数字形式(无 "tok" 单位),供头部极简进度(R9)使用;
// formatTokens 在其后拼接单位,供展开区 meta 行(R8)使用。
function formatTokenCount(n: number): string {
  if (n < 1000) return `${n}`;
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}K`;
  return `${(n / 1_000_000).toFixed(2)}M`;
}

function formatTokens(n: number): string {
  return `${formatTokenCount(n)} tok`;
}

// buildProgressTick 拼出头部极简进度(R9):纯数字、无「个工具」「tok」字样,
// 用 " · " 连接;任一侧为零则只留另一侧;两侧都为零时返回空串(不渲染)。
function buildProgressTick(toolUses: number, totalTokens: number): string {
  const parts: string[] = [];
  if (toolUses > 0) parts.push(`${toolUses}`);
  if (totalTokens > 0) parts.push(formatTokenCount(totalTokens));
  return parts.join(" · ");
}

// buildProgressAriaLabel 给极简进度(R9)补一个无障碍标签:可见文案仍是
// 无单位的 "3 · 14.5K"(不把「个工具」「tok」文案加回可见位置,否则推翻 R9),
// 但 aria-label 让折叠态下的读屏用户也能知道这两个数字分别是什么,不必强制
// 展开卡片才能获得语义。只用 aria-label,不用 title——title 会在鼠标悬停时
// 可见地弹出同样的文案,等于把 R9 刚去掉的文案又从后门带回来。与
// buildProgressTick 同样的"任一侧为零则只留另一侧"规则。
function buildProgressAriaLabel(
  t: TFunction,
  toolUses: number,
  totalTokens: number,
): string {
  const parts: string[] = [];
  if (toolUses > 0) {
    parts.push(
      t("canonical.agentSpawn.progressAria.tools", { count: toolUses }),
    );
  }
  if (totalTokens > 0) {
    // count 是 i18next 的保留选项,会被送进 Intl.PluralRules.select() 做单
    // 复数判定;formatTokenCount 产出的是展示用字符串(如 "14.5K"),不是可
    // 数的语法数量,不应该借用这个保留名字,改用普通具名占位符 value。
    parts.push(
      t("canonical.agentSpawn.progressAria.tokens", {
        value: formatTokenCount(totalTokens),
      }),
    );
  }
  return parts.join(", ");
}

// buildOutcomeSummary 拼出成功态卡头的次级信息「N 步 · <token> · <耗时>」。
// 成功不再挂状态胶囊(spec 决策 10:没有标记 = 成功),腾出来的位置放实义信息;
// 缺失的项整项不出现,三项都缺就返回空串(不渲染)。
function buildOutcomeSummary(
  t: TFunction,
  toolUses: number,
  totalTokens: number,
  durationMs: number,
): string {
  const parts: string[] = [];
  if (toolUses > 0) {
    parts.push(t("canonical.agentSpawn.outcome.steps", { count: toolUses }));
  }
  if (totalTokens > 0) parts.push(formatTokens(totalTokens));
  if (durationMs > 0) parts.push(formatDuration(durationMs));
  return parts.join(" · ");
}

// 子代理内部的活动块阈值(spec 决策 9)。转录里的块只看运行态,子代理这层另有
// 阈值 —— 用户已经主动展开过卡片一次,再默认折叠一遍步骤反而多一次点击;但
// 200 步的子代理默认展开就是一面墙,所以超过阈值仍然只留组头。
const STEPS_DEFAULT_EXPANDED_MAX = 20;

// AgentSpawnSteps —— 子代理的步骤区就是转录里的那一个活动块(spec 子代理 §1):
// 同一个组头、同一套活动行、同一套展开体。StepRow 只是换形状成 ActivityStep,
// 汇总走 transcript-rows 导出的 summarizeActivity —— 子代理组头与转录组头因此
// 不可能分叉(自建一份子代理专用汇总迟早跟着两边各改一次)。
function AgentSpawnSteps({
  cwd,
  keyPrefix,
  runStatus,
  steps,
}: {
  cwd?: string;
  /** 组头 / 每一步折叠态的持久化前缀,沿用旧 step key 形态(折叠态零迁移)。 */
  keyPrefix: string;
  /** 这些步骤所属 run 的状态 —— 决定没配到结果的一步怎么报(见 unmatchedOutcome)。 */
  runStatus?: AgentSpawnStatus;
  steps: StepRow[];
}): React.ReactElement {
  const activitySteps: ActivityStep[] = steps.map((step, index) => ({
    resultBlock: step.result,
    toolBlock: step.tool,
    type: "tool",
    uiStateKey: `${keyPrefix}:step:${step.tool.toolUseId || index}`,
  }));
  const pendingOutcome = unmatchedOutcome(runStatus);
  return (
    <ActivityBlock
      steps={activitySteps}
      // 组头失败计数与活动行走同一个 pendingOutcome:run 以失败终结时,没配到
      // 结果的那些步在行里是红的,组头也必须把它们算进去 —— 21 步以上默认折叠,
      // 组头漏算就等于宣称「一个失败都没有」。
      summary={summarizeActivity(activitySteps, pendingOutcome === "failed")}
      uiStateKey={`${keyPrefix}:activity`}
      cwd={cwd}
      defaultExpanded={activitySteps.length <= STEPS_DEFAULT_EXPANDED_MAX}
      pendingOutcome={pendingOutcome}
    />
  );
}

// unmatchedOutcome —— run 已经终结、某一步却始终没配到 tool_result 时,那一步
// 归属不明:按 run 的终态报 失败 / 已取消,其余(含 completed)一律「结果未知」。
// 判据与旧 AgentSpawnStepCard 的 terminalFallbackStatus 完全一致 —— 步骤区换成
// 活动块不改变「它到底发生了什么」,只改变它长什么样。
function unmatchedOutcome(status?: AgentSpawnStatus): PendingOutcome {
  if (!status || status === "running" || status === "waiting") return "running";
  if (status === "failed" || status === "canceled") return status;
  return "unknown";
}

const EMPTY_CHILD_BLOCKS: AgentSpawnChildBlocks = {
  all: [],
  byRun: new Map(),
};

function allChildBlocks(children: AgentSpawnChildBlocks): ChatBlockData[] {
  return children.all;
}

function runStatus(
  run: AgentSpawnRunDTO,
  mode: AgentSpawnMode | undefined,
): AgentSpawnStatus {
  if (run.status) return run.status;
  if (mode === "parallel") return "waiting";
  return run.index === 0 ? "running" : "waiting";
}

function isGroupedSpawn(spawn: AgentSpawnDTO): boolean {
  return (
    spawn.mode === "parallel" ||
    spawn.mode === "chain" ||
    (spawn.runs?.length ?? 0) > 1
  );
}

// AgentSpawnCard remains the single production renderer. Legacy/no-runs data keeps
// the existing layout; normalized parallel/chain state branches into grouped runs.
export const AgentSpawnCard: React.FC<CanonicalCardProps> = ({
  toolBlock,
  resultBlock,
  cwd,
  childBlocks = EMPTY_CHILD_BLOCKS,
  uiStateKey,
  onStopSubagent,
}) => {
  const { t } = useTranslation();
  const spawn = readSpawn(toolBlock);
  const [expanded, setExpanded] = useTranscriptBooleanState(uiStateKey, false);
  const { mounted, onTransitionEnd } = useCollapsible(expanded);

  if (!spawn) return null;

  // 折叠态持久化前缀。uiStateKey 在转录里恒定有值(transcript-row-view 传的行
  // 身份);缺省时退到 tool_use id,保证同一屏的两张卡不会共用一个 key。
  const keyBase = uiStateKey ?? `agent-spawn:${toolBlock.toolUseId ?? ""}`;

  if (isGroupedSpawn(spawn)) {
    return (
      <GroupedAgentSpawnCard
        spawn={spawn}
        toolBlock={toolBlock}
        resultBlock={resultBlock}
        cwd={cwd}
        childBlocks={childBlocks}
        expanded={expanded}
        setExpanded={setExpanded}
        keyBase={keyBase}
        onStopSubagent={onStopSubagent}
      />
    );
  }

  const normalizedRun = spawn.runs?.length === 1 ? spawn.runs[0] : undefined;
  const normalizedStatus = normalizedRun
    ? (normalizedRun.status ?? spawn.status)
    : undefined;
  const singleSpawn: AgentSpawnDTO = normalizedRun
    ? {
        ...spawn,
        taskDescription: normalizedRun.task || spawn.taskDescription,
        prompt: normalizedRun.task || spawn.prompt,
        model:
          normalizedRun.model || normalizedRun.requestedModel || spawn.model,
        status: normalizedStatus,
      }
    : spawn;
  const status = normalizedStatus
    ? statusAgentState(normalizedStatus)
    : statusFromSpawn(singleSpawn, resultBlock);
  const { pillClassName } = statusConfig[status];
  const StatusIcon =
    status === "error" || singleSpawn.status === "partial"
      ? TriangleAlert
      : singleSpawn.status === "canceled"
        ? CircleSlash
        : singleSpawn.status === "unknown"
          ? CircleHelp
          : status === "running" || status === "waiting"
            ? LoaderCircle
            : Check;
  const statusLabel =
    normalizedRun && normalizedStatus
      ? buildNormalizedStatusLabel(normalizedStatus, t, singleSpawn.durationMs)
      : buildStatusLabel(status, singleSpawn.status, t, singleSpawn.durationMs);

  const description = singleSpawn.taskDescription || "";
  const displayName =
    normalizedRun?.agent || t("canonical.agentSpawn.defaultName");
  const subagentType = normalizedRun ? "" : singleSpawn.subagentType || "";
  const profile = normalizedRun?.profile || "";
  const modelBadge = singleSpawn.model
    ? shortenModelName(singleSpawn.model)
    : "";
  const prompt = singleSpawn.prompt || "";
  const normalizedOutput =
    normalizedRun?.summary || normalizedRun?.errorMessage || "";
  const summaryResultBlock =
    resultBlock?.text || !normalizedOutput
      ? resultBlock
      : ({
          type: "tool_result",
          text: normalizedOutput,
          isError: normalizedRun?.status === "failed",
        } as ChatBlockData);
  // Legacy and normalized single both attach every child, including missing or
  // unknown run IDs, to their sole STEPS list.
  const steps = pairChildBlocks(allChildBlocks(childBlocks));
  const toolUses =
    singleSpawn.toolUses ?? normalizedRun?.toolUses ?? steps.length;
  const totalTokens = singleSpawn.totalTokens ?? 0;
  const durationMs = singleSpawn.durationMs ?? 0;
  // R9:极简进度只在运行态渲染,完成/取消/出错都不再显示。
  const isRunningState = status === "running" || status === "waiting";
  const progressTick = isRunningState
    ? buildProgressTick(toolUses, totalTokens)
    : "";
  const progressAriaLabel = isRunningState
    ? buildProgressAriaLabel(t, toolUses, totalTokens)
    : "";
  // R8:展开区 meta 行独立于运行状态存在,只要有值就列出;模型用未裁剪原值。
  const hasMetaRow =
    !!singleSpawn.model || toolUses > 0 || totalTokens > 0 || durationMs > 0;
  // 成功 = 落定且没有取消 / 跳过 / 未知 / 部分失败这些非成功语义(它们同样走
  // idle 通道)。只有成功态摘掉状态胶囊,其余标记一律不动(spec 决策 10)。
  const isSuccess =
    status === "idle" &&
    (singleSpawn.status === undefined || singleSpawn.status === "completed");
  const outcomeSummary = isSuccess
    ? buildOutcomeSummary(t, toolUses, totalTokens, durationMs)
    : "";

  return (
    <TranscriptCard
      data-testid="agent-spawn-card"
      aria-label={t("canonical.agentSpawn.aria", {
        name: description || displayName,
      })}
      data-selectable-text="true"
      tone={status === "error" ? "error" : "default"}
      className="font-mono text-aux"
    >
      <TranscriptCardHeader
        onClick={(event) => {
          if (shouldIgnoreClickForSelection(event)) return;
          setExpanded((v) => !v);
        }}
        aria-expanded={expanded}
      >
        <ChevronRight
          className={cn(
            "size-3 shrink-0 text-muted-foreground transition-transform duration-150 ease-out motion-reduce:transition-none",
            expanded && "rotate-90",
          )}
          aria-hidden="true"
        />
        <Users
          // 名字与图标不再占用品牌色:primary 的语义是「可交互」,归还给真正
          // 可点的元素(spec 决策 10 / docs/design.md §2.3)。
          className="size-3.5 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
        <span
          data-copyable-control-text="true"
          className="shrink-0 font-semibold text-foreground"
        >
          {displayName}
        </span>
        {description ? (
          <>
            <span className="text-muted-foreground">·</span>
            <span
              data-copyable-control-text="true"
              // R11:让位次序第一名——flex-shrink 权重远大于极简进度(见下方
              // agent-spawn-progress 的注释),这样无论两者各自的内容宽度比例
              // 如何,横向空间不足时都是描述先被压缩到裁切,而非按同一收缩
              // 因子与进度同时按比例挤压。
              className="min-w-0 shrink-[20] truncate text-muted-foreground"
            >
              {description}
            </span>
          </>
        ) : null}
        {subagentType ? (
          <span
            data-testid="agent-spawn-role-badge"
            data-copyable-control-text="true"
            className="ml-1 inline-flex shrink-0 items-center rounded-sm bg-secondary px-1.5 py-0.5 text-meta font-medium text-foreground"
          >
            {subagentType}
          </span>
        ) : null}
        {profile ? (
          <span
            data-testid="agent-spawn-profile-badge"
            data-copyable-control-text="true"
            className="ml-1 inline-flex shrink-0 items-center rounded-sm bg-secondary px-1.5 py-0.5 text-meta font-medium text-foreground"
          >
            {profile}
          </span>
        ) : null}
        {modelBadge ? (
          <span
            data-testid="agent-spawn-model-badge"
            data-copyable-control-text="true"
            // R7(修订)/R11:不再用与宽度无关的固定 max-w 裁剪——横向空间充足
            // 时徽标必须完整显示裁剪后的全文。空间不足时改为参与 R11 的
            // flex-shrink 让位次序:排在描述(shrink-[20])与极简进度(默认
            // shrink=1)都让位之后,shrink-[0.2] 是比两者都小的正因子,
            // min-w-0 允许它在挤压下真正收缩到 0 而不是撑破容器。truncate
            // 挪到内层 <span>、overflow-hidden 留在这个 flex 容器上——与下方
            // agent-spawn-progress 同一写法:text-overflow:ellipsis 只在块级
            // 容器生效,加在 inline-flex 容器本身,文本子节点会变成匿名 flex
            // item,省略号永远不会绘制,只会齐字硬切。完整原值仍在展开区
            // meta 行可见,截断不丢信息。
            className="ml-1 inline-flex min-w-0 shrink-[0.2] items-center overflow-hidden rounded-sm border border-border-strong bg-transparent px-1.5 py-0.5 text-meta font-normal text-muted-foreground"
          >
            <span className="truncate">{modelBadge}</span>
          </span>
        ) : null}
        <span className="min-w-0 flex-1" />
        {progressTick ? (
          // R9/R11:纯数字极简进度,次级前景色、无文案。让位次序第二名——
          // 保留默认 flex-shrink:1(通过 `shrink`),远小于描述的 `shrink-[20]`,
          // 所以横向空间不足时几乎全部收缩量先记到描述头上,直到描述被压到
          // min-w-0 的下限,余量才轮到这里继续收缩/裁切;状态胶囊(shrink-0)
          // 完全不参与收缩,任何宽度下都保持完整。
          <span
            data-testid="agent-spawn-progress"
            data-copyable-control-text="true"
            aria-label={progressAriaLabel}
            className="inline-flex min-w-0 shrink items-center gap-1 overflow-hidden text-meta text-subtle-foreground"
          >
            <Wrench className="size-2.5 shrink-0" aria-hidden="true" />
            <span className="truncate">{progressTick}</span>
          </span>
        ) : null}
        {(status === "running" || status === "waiting") &&
          singleSpawn.taskId &&
          onStopSubagent &&
          toolBlock.toolUseId && (
            <button
              type="button"
              onClick={(event) => {
                event.stopPropagation();
                onStopSubagent(toolBlock.toolUseId!);
              }}
              aria-label={t("canonical.agentSpawn.stop")}
              title={t("canonical.agentSpawn.stop")}
              className="inline-flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive focus-visible:ring-2 focus-visible:ring-ring/50"
            >
              <Square className="size-2.5 fill-current" aria-hidden="true" />
            </button>
          )}
        {isSuccess ? (
          // 成功态没有胶囊,位置改放实义信息;三项都缺时整块不渲染。
          outcomeSummary ? (
            <span
              data-testid="agent-spawn-outcome"
              data-copyable-control-text="true"
              className="shrink-0 text-meta text-subtle-foreground"
            >
              {outcomeSummary}
            </span>
          ) : null
        ) : (
          <TranscriptPill
            data-testid="agent-spawn-status-pill"
            data-copyable-control-text="true"
            className={pillClassName}
          >
            <StatusIcon
              className={cn(
                "size-2.5",
                (status === "running" || status === "waiting") &&
                  "animate-spin motion-reduce:animate-none",
              )}
              aria-hidden="true"
            />
            {statusLabel}
          </TranscriptPill>
        )}
      </TranscriptCardHeader>
      <div
        data-slot="agent-spawn-details"
        aria-hidden={!expanded}
        onTransitionEnd={onTransitionEnd}
        className="grid transition-[grid-template-rows] duration-200 ease-out motion-reduce:transition-none"
        style={{ gridTemplateRows: expanded ? "1fr" : "0fr" }}
      >
        <div className="min-h-0 overflow-hidden">
          <div className="flex flex-col gap-3 border-t border-border px-3 py-3">
            {hasMetaRow ? (
              <div
                data-testid="agent-spawn-meta-row"
                className="flex flex-wrap items-center gap-x-3.5 gap-y-1 text-meta"
              >
                {singleSpawn.model ? (
                  <AgentSpawnMetaItem
                    testId="agent-spawn-meta-model"
                    label={t("canonical.agentSpawn.meta.model")}
                    value={singleSpawn.model}
                  />
                ) : null}
                {toolUses > 0 ? (
                  <AgentSpawnMetaItem
                    testId="agent-spawn-meta-tools"
                    label={t("canonical.agentSpawn.meta.tools")}
                    value={`${toolUses}`}
                  />
                ) : null}
                {totalTokens > 0 ? (
                  <AgentSpawnMetaItem
                    testId="agent-spawn-meta-tokens"
                    label={t("canonical.agentSpawn.meta.tokens")}
                    value={formatTokens(totalTokens)}
                  />
                ) : null}
                {durationMs > 0 ? (
                  <AgentSpawnMetaItem
                    testId="agent-spawn-meta-duration"
                    label={t("canonical.agentSpawn.meta.duration")}
                    value={formatDuration(durationMs)}
                  />
                ) : null}
              </div>
            ) : null}
            <AgentSpawnSection
              label={t("canonical.agentSpawn.sections.prompt")}
            >
              <div className="max-h-[160px] overflow-y-auto overscroll-contain whitespace-pre-wrap break-words rounded-sm bg-muted/40 px-2.5 py-2 text-foreground">
                {prompt ? (
                  <span>{prompt}</span>
                ) : (
                  <span className="text-muted-foreground">
                    {t("canonical.agentSpawn.emptyPrompt")}
                  </span>
                )}
              </div>
            </AgentSpawnSection>
            <AgentSpawnSection
              label={t("canonical.agentSpawn.sections.steps")}
              meta={
                steps.length === 0 && status === "running"
                  ? t("canonical.agentSpawn.waitingFirstStep")
                  : null
              }
            >
              {steps.length === 0 ? (
                <div className="rounded-sm bg-muted/30 px-2.5 py-2 text-muted-foreground">
                  {status === "running"
                    ? t("canonical.agentSpawn.emptyStepsRunning")
                    : t("canonical.agentSpawn.emptySteps")}
                </div>
              ) : // 性能:步骤区懒挂载 —— 卡片折叠态不 mount 活动块。一个大
              // subagent 可带 150-200 个嵌套步骤,折叠时把整列表 mount 进 DOM
              // (0fr 网格只是视觉隐藏)会让 transcript 在会话打开 / tab 切换 /
              // 滚动时卡顿;展开才挂载。折叠态下 steps 数量仍从 steps.length
              // 读取,头部/进度/摘要不受影响。收缩过渡期间 mounted 仍为 true。
              mounted ? (
                <AgentSpawnSteps
                  steps={steps}
                  cwd={cwd}
                  keyPrefix={keyBase}
                  runStatus={
                    normalizedRun &&
                    normalizedStatus &&
                    isTerminalRunStatus(normalizedStatus)
                      ? normalizedStatus
                      : undefined
                  }
                />
              ) : null}
            </AgentSpawnSection>
            <AgentSpawnSection
              label={t("canonical.agentSpawn.sections.summary")}
            >
              {renderSummary(status, t, summaryResultBlock)}
            </AgentSpawnSection>
          </div>
        </div>
      </div>
    </TranscriptCard>
  );
};

type GroupedAgentSpawnCardProps = {
  spawn: AgentSpawnDTO;
  toolBlock: ChatBlockData;
  resultBlock?: ChatBlockData;
  cwd?: string;
  childBlocks: AgentSpawnChildBlocks;
  expanded: boolean;
  setExpanded: React.Dispatch<React.SetStateAction<boolean>>;
  /** 卡片级折叠态前缀,run / 步骤的 key 都从它派生。 */
  keyBase: string;
  onStopSubagent?: (toolUseId: string) => void;
};

function isTerminalRunStatus(status: AgentSpawnStatus): boolean {
  return status !== "waiting" && status !== "running" && status !== "partial";
}

function statusLabel(status: AgentSpawnStatus, t: TFunction): string {
  switch (status) {
    case "waiting":
      return t("canonical.agentSpawn.status.waiting");
    case "completed":
      return t("canonical.agentSpawn.status.done");
    case "failed":
      return t("canonical.agentSpawn.status.failed");
    case "canceled":
      return t("canonical.agentSpawn.status.stopped");
    case "skipped":
      return t("canonical.agentSpawn.status.skipped");
    case "unknown":
      return t("canonical.agentSpawn.status.unknown");
    case "partial":
      return t("canonical.agentSpawn.status.partial");
    case "running":
    default:
      return t("canonical.agentSpawn.status.running");
  }
}

function buildNormalizedStatusLabel(
  status: AgentSpawnStatus,
  t: TFunction,
  durationMs?: number,
): string {
  const label = statusLabel(status, t);
  return durationMs
    ? t("canonical.agentSpawn.status.withDuration", {
        status: label,
        duration: formatDuration(durationMs),
      })
    : label;
}

function statusAgentState(status: AgentSpawnStatus): AgentStatus {
  if (status === "failed") return "error";
  if (status === "running") return "running";
  if (status === "waiting") return "waiting";
  return "idle";
}

function statusPillClass(status: AgentSpawnStatus): string {
  if (status === "partial") return statusConfig.waiting.pillClassName;
  return statusConfig[statusAgentState(status)].pillClassName;
}

function StatusGlyph({ status }: { status: AgentSpawnStatus }) {
  const Icon =
    status === "failed" || status === "partial"
      ? TriangleAlert
      : status === "canceled" || status === "skipped"
        ? CircleSlash
        : status === "unknown"
          ? CircleHelp
          : status === "running" || status === "waiting"
            ? LoaderCircle
            : Check;
  return (
    <Icon
      className={cn(
        "size-2.5",
        (status === "running" || status === "waiting") &&
          "animate-spin motion-reduce:animate-none",
      )}
      aria-hidden="true"
    />
  );
}

function modeLabel(mode: AgentSpawnMode | undefined, t: TFunction): string {
  if (mode === "chain") return t("canonical.agentSpawn.mode.chain");
  if (mode === "single") return t("canonical.agentSpawn.mode.single");
  return t("canonical.agentSpawn.mode.parallel");
}

function sourceLabel(source: string | undefined, t: TFunction): string {
  if (source === "project") return t("canonical.agentSpawn.source.project");
  if (source === "user") return t("canonical.agentSpawn.source.user");
  if (source === "both") return t("canonical.agentSpawn.source.both");
  return "";
}

function GroupedAgentSpawnCard({
  spawn,
  toolBlock,
  resultBlock,
  cwd,
  childBlocks,
  expanded,
  setExpanded,
  keyBase,
  onStopSubagent,
}: GroupedAgentSpawnCardProps): React.ReactElement {
  const { t } = useTranslation();
  const { mounted, onTransitionEnd } = useCollapsible(expanded);
  const runs = (spawn.runs ?? []).slice().sort((a, b) => a.index - b.index);
  const outerStatus = spawn.status ?? "running";
  const terminalCount = runs.filter((run) =>
    isTerminalRunStatus(runStatus(run, spawn.mode)),
  ).length;
  const declaredRunIDs = new Set(runs.map((run) => run.id));
  const fallbackBlocks = childBlocks.all.filter(
    (block) => !block.subagentRunId || !declaredRunIDs.has(block.subagentRunId),
  );
  const fallbackSteps = pairChildBlocks(fallbackBlocks);
  const summaryAgentStatus =
    outerStatus === "running"
      ? "running"
      : outerStatus === "waiting"
        ? "waiting"
        : outerStatus === "failed"
          ? "error"
          : "idle";
  // 卡头与单卡同一条规则:成功摘掉胶囊换实义信息,其余状态标记原样保留。
  const outcomeSummary =
    outerStatus === "completed"
      ? buildOutcomeSummary(
          t,
          spawn.toolUses ??
            childBlocks.all.filter((block) => block.type === "tool_use").length,
          spawn.totalTokens ?? 0,
          spawn.durationMs ?? 0,
        )
      : "";

  return (
    <TranscriptCard
      data-testid="agent-spawn-card"
      aria-label={t("canonical.agentSpawn.groupedAria", {
        mode: modeLabel(spawn.mode, t),
      })}
      data-selectable-text="true"
      tone={outerStatus === "failed" ? "error" : "default"}
      className="font-mono text-aux"
    >
      <TranscriptCardHeader
        onClick={(event) => {
          if (shouldIgnoreClickForSelection(event)) return;
          setExpanded((value) => !value);
        }}
        aria-expanded={expanded}
      >
        <ChevronRight
          className={cn(
            "size-3 shrink-0 text-muted-foreground transition-transform duration-150 ease-out motion-reduce:transition-none",
            expanded && "rotate-90",
          )}
          aria-hidden="true"
        />
        <Users
          className="size-3.5 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
        <span
          data-copyable-control-text="true"
          className="shrink-0 font-semibold text-foreground"
        >
          {t("canonical.agentSpawn.groupedName")}
        </span>
        <span className="text-muted-foreground">·</span>
        <span
          data-copyable-control-text="true"
          className="shrink-0 text-muted-foreground"
        >
          {modeLabel(spawn.mode, t)}
        </span>
        <span className="text-muted-foreground">·</span>
        <span
          data-copyable-control-text="true"
          className="shrink-0 text-muted-foreground"
        >
          {terminalCount}/{runs.length}
        </span>
        <span className="min-w-0 flex-1" />
        {outerStatus === "running" &&
          spawn.taskId &&
          onStopSubagent &&
          toolBlock.toolUseId && (
            <button
              type="button"
              onClick={(event) => {
                event.stopPropagation();
                onStopSubagent(toolBlock.toolUseId!);
              }}
              aria-label={t("canonical.agentSpawn.stop")}
              title={t("canonical.agentSpawn.stop")}
              className="inline-flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive focus-visible:ring-2 focus-visible:ring-ring/50"
            >
              <Square className="size-2.5 fill-current" aria-hidden="true" />
            </button>
          )}
        {outerStatus === "completed" ? (
          outcomeSummary ? (
            <span
              data-testid="agent-spawn-outcome"
              data-copyable-control-text="true"
              className="shrink-0 text-meta text-subtle-foreground"
            >
              {outcomeSummary}
            </span>
          ) : null
        ) : (
          <TranscriptPill
            data-testid="agent-spawn-status-pill"
            className={statusPillClass(outerStatus)}
          >
            <StatusGlyph status={outerStatus} />
            {statusLabel(outerStatus, t)}
          </TranscriptPill>
        )}
      </TranscriptCardHeader>
      <div
        data-slot="agent-spawn-details"
        aria-hidden={!expanded}
        onTransitionEnd={onTransitionEnd}
        className="grid transition-[grid-template-rows] duration-200 ease-out motion-reduce:transition-none"
        style={{ gridTemplateRows: expanded ? "1fr" : "0fr" }}
      >
        <div className="min-h-0 overflow-hidden">
          <div className="flex flex-col gap-3 border-t border-border px-3 py-3">
            <AgentSpawnSection label={t("canonical.agentSpawn.sections.tasks")}>
              <div className="flex flex-col gap-2">
                {runs.map((run) => (
                  <AgentSpawnRunGroup
                    key={run.id || run.index}
                    run={run}
                    mode={spawn.mode}
                    blocks={childBlocks.byRun.get(run.id) ?? []}
                    cwd={cwd}
                    cardExpanded={mounted}
                    uiStateKey={`${keyBase}:run:${run.id || run.index}`}
                  />
                ))}
              </div>
            </AgentSpawnSection>
            {fallbackSteps.length > 0 && mounted ? (
              <div data-testid="agent-spawn-fallback-steps">
                <AgentSpawnSection
                  label={t("canonical.agentSpawn.sections.steps")}
                >
                  <AgentSpawnSteps
                    steps={fallbackSteps}
                    cwd={cwd}
                    keyPrefix={`${keyBase}:fallback`}
                    runStatus={outerStatus}
                  />
                </AgentSpawnSection>
              </div>
            ) : null}
            <AgentSpawnSection
              label={t("canonical.agentSpawn.sections.summary")}
            >
              {renderSummary(summaryAgentStatus, t, resultBlock)}
            </AgentSpawnSection>
          </div>
        </div>
      </div>
    </TranscriptCard>
  );
}

function AgentSpawnRunGroup({
  run,
  mode,
  blocks,
  cwd,
  cardExpanded,
  uiStateKey,
}: {
  run: AgentSpawnRunDTO;
  mode: AgentSpawnMode | undefined;
  blocks: ChatBlockData[];
  cwd?: string;
  /** 卡片内容挂载态(展开或收缩过渡中)—— 折叠落定后不 mount 任何 run 内的步骤(性能)。 */
  cardExpanded: boolean;
  uiStateKey: string;
}): React.ReactElement {
  const { t } = useTranslation();
  const status = runStatus(run, mode);
  const steps = pairChildBlocks(blocks);
  const hasActivity =
    steps.length > 0 || (status !== "waiting" && status !== "skipped");
  const defaultExpanded = status === "running" || status === "failed";
  const [expanded, setExpanded] = useTranscriptBooleanState(
    uiStateKey,
    defaultExpanded,
  );
  const userTouched = React.useRef(false);
  const previousActivity = React.useRef(hasActivity);
  React.useEffect(() => {
    if (!userTouched.current && hasActivity && !previousActivity.current) {
      setExpanded(true);
    }
    previousActivity.current = hasActivity;
  }, [hasActivity, setExpanded]);

  const name = run.agent || t("canonical.agentSpawn.defaultName");
  const source = sourceLabel(run.agentSource, t);
  const model = run.model || run.requestedModel || "";
  const output = run.summary || run.errorMessage || "";
  const terminal = isTerminalRunStatus(status);

  return (
    <div
      data-testid="agent-spawn-run-group"
      data-run-id={run.id}
      className={cn(
        "overflow-hidden rounded-lg border bg-background",
        status === "failed"
          ? "border-status-error/50 border-l-2 border-l-status-error"
          : status === "running"
            ? "border-status-running/50 border-l-2 border-l-status-running"
            : status === "waiting" || status === "partial"
              ? "border-status-waiting/50 border-l-2 border-l-status-waiting"
              : "border-border border-l-2 border-l-border",
      )}
    >
      <button
        type="button"
        onClick={() => {
          userTouched.current = true;
          setExpanded((value) => !value);
        }}
        aria-expanded={expanded}
        aria-label={t("canonical.agentSpawn.runToggle", {
          index: run.index + 1,
          name,
        })}
        className="flex w-full min-w-0 cursor-pointer items-center gap-2 px-2.5 py-2 text-left transition-colors hover:bg-muted/30 focus-visible:ring-2 focus-visible:ring-ring/50"
      >
        <ChevronRight
          className={cn(
            "size-2.5 shrink-0 text-muted-foreground transition-transform duration-150 ease-out motion-reduce:transition-none",
            expanded && "rotate-90",
          )}
          aria-hidden="true"
        />
        <span className="shrink-0 text-meta text-subtle-foreground">
          {run.index + 1}
        </span>
        <span
          data-copyable-control-text="true"
          className="shrink-0 font-semibold text-foreground"
        >
          {name}
        </span>
        {source ? <RunBadge>{source}</RunBadge> : null}
        {run.profile ? <RunBadge>{run.profile}</RunBadge> : null}
        {run.task ? (
          <>
            <span className="text-muted-foreground">·</span>
            <span
              data-copyable-control-text="true"
              className="min-w-0 truncate text-muted-foreground"
            >
              {run.task}
            </span>
          </>
        ) : null}
        {model ? (
          <RunBadge testId="agent-spawn-run-model">
            {shortenModelName(model)}
          </RunBadge>
        ) : null}
        <span className="min-w-0 flex-1" />
        <TranscriptPill className={statusPillClass(status)}>
          <StatusGlyph status={status} />
          {statusLabel(status, t)}
        </TranscriptPill>
      </button>
      <div
        aria-hidden={!expanded}
        className="grid transition-[grid-template-rows] duration-150 ease-out motion-reduce:transition-none"
        style={{ gridTemplateRows: expanded ? "1fr" : "0fr" }}
      >
        <div className="min-h-0 overflow-hidden">
          <div className="flex flex-col gap-2 border-t border-border px-2.5 py-2">
            <AgentSpawnSection label={t("canonical.agentSpawn.sections.steps")}>
              {steps.length > 0 ? (
                // 性能:run group 的步骤区按**卡片**展开态懒挂载 —— 折叠的卡片
                // 不 mount 任何步骤。run group 自身折叠/展开只控制视觉高度,不
                // 卸载步骤(测试契约:卡片展开后,折叠 run 的步骤仍可查询到)。
                cardExpanded ? (
                  <AgentSpawnSteps
                    steps={steps}
                    cwd={cwd}
                    keyPrefix={uiStateKey}
                    runStatus={terminal ? status : undefined}
                  />
                ) : null
              ) : (
                <div className="rounded-sm bg-muted/30 px-2.5 py-2 text-muted-foreground">
                  {status === "running"
                    ? t("canonical.agentSpawn.emptyStepsRunning")
                    : t("canonical.agentSpawn.emptySteps")}
                </div>
              )}
            </AgentSpawnSection>
            {terminal ? (
              <AgentSpawnSection
                label={t("canonical.agentSpawn.sections.output")}
              >
                <CollapsibleCode
                  value={output || t("canonical.agentSpawn.emptyResult")}
                  surface={status === "failed" ? "destructive" : "muted"}
                  bodyClassName="rounded-sm px-2.5 py-2"
                />
              </AgentSpawnSection>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  );
}

function RunBadge({
  children,
  testId,
}: {
  children: React.ReactNode;
  testId?: string;
}) {
  return (
    <span
      data-testid={testId}
      data-copyable-control-text="true"
      className="inline-flex min-w-0 shrink-0 items-center rounded-sm border border-border-strong px-1.5 py-0.5 text-meta text-muted-foreground"
    >
      <span className="truncate">{children}</span>
    </span>
  );
}

// AgentSpawnMetaItem 渲染展开区 meta 行的单个「标签 值」项(R8)。value 只放在
// 独立的 testId 节点上,不与标签共用一个 textContent——下沉数字与调用方(测试/
// 未来其它读者)需要精确取到裸值,不掺文案。
function AgentSpawnMetaItem({
  testId,
  label,
  value,
}: {
  testId: string;
  label: string;
  value: string;
}) {
  return (
    <span className="inline-flex items-center gap-1">
      <span className="text-subtle-foreground">{label}</span>
      <span data-testid={testId} className="text-foreground">
        {value}
      </span>
    </span>
  );
}

function AgentSpawnSection({
  children,
  label,
  meta,
}: {
  children: React.ReactNode;
  label: string;
  meta?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2">
        <span className="font-sans text-meta font-semibold uppercase tracking-[0.06em] text-muted-foreground">
          {label}
        </span>
        {meta ? (
          <span className="font-mono text-meta text-subtle-foreground">
            {meta}
          </span>
        ) : null}
        <span className="h-px min-w-0 flex-1 bg-border" />
      </div>
      {children}
    </div>
  );
}

function renderSummary(
  status: AgentStatus,
  t: TFunction,
  resultBlock?: ChatBlockData,
): React.ReactElement {
  if (!resultBlock || !resultBlock.text) {
    if (status === "running" || status === "waiting") {
      return (
        <div className="inline-flex items-center gap-2 rounded-sm bg-secondary px-2.5 py-2 text-muted-foreground">
          <Clock className="size-3" aria-hidden="true" />
          {t("canonical.agentSpawn.summaryRunning")}
        </div>
      );
    }
    return (
      <div className="rounded-sm bg-muted/40 px-2.5 py-2 text-muted-foreground">
        {t("canonical.agentSpawn.emptySummary")}
      </div>
    );
  }
  return (
    <CollapsibleCode
      value={resultBlock.text}
      surface={status === "error" ? "destructive" : "muted"}
      bodyClassName={cn(
        "rounded-sm border-l-2 px-2.5 py-2",
        status === "error" ? "border-status-error" : "border-primary",
      )}
    />
  );
}
