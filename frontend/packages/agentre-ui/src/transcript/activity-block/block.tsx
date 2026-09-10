import * as React from "react";
import { ChevronRight, Ellipsis, Layers } from "lucide-react";
import { cn } from "../../lib/utils";
import { shouldIgnoreClickForSelection } from "../../lib/copyable-text";
import { useCollapsible } from "../../hooks/use-collapsible";
import { useTranscriptBooleanState } from "../transcript-ui-state";
import { useUiTranslation } from "../../i18n";

import {
  summarizeActivity,
  type ActivityStep,
  type ActivitySummary,
  type ActivitySummaryPart,
} from "../transcript-rows";

import { stepLabel, type PendingOutcome } from "./facts";
import { ActivityRow } from "./row";

// ActivityBlock —— 一段连续活动(思考 / 只读探查 / 中性 / 写 / 命令 / 失败)的
// 一行组头 + 可展开的活动行时间轴。一个块 = 一个 RenderItem = 一个虚拟行:
// 折叠态只 mount 组头,组内步骤(以及它们几十 KB 的结果文本)一个都不进 DOM。
//
// 组头是折叠态唯一的信息出口:展开指示 → 活动图标 → N 步 → 汇总 → 耗时。
// 汇总的类目顺序与截断由行模型层(transcript-rows 的 summarizeActivity)算好,
// 这里只负责渲染 —— 失败计数单独渲染在末尾,永不参与截断。
//
// 运行中的块自动展开,于是一轮跑到几十步时它就是一堵不断长高的墙,新的一步把
// 正文越推越远。超过阈值时只留最后几行,前面的收成一行可点开的省略行(带被省
// 略部分的汇总 + 红失败数)。只在运行态生效:落定后整块本来就会自动收起,用户
// 主动点开时要的是全貌。

// 运行中超过 RUNNING_ELIDE_OVER 步就省略,只保留最后 RUNNING_TAIL 行。
// 8 行约等于「还能一眼扫完」的上限;保留 6 行足以看出 agent 此刻在什么节奏上,
// 又不至于让在途的几十步全部挂进 DOM。
const RUNNING_ELIDE_OVER = 8;
const RUNNING_TAIL = 6;

export type ActivityBlockProps = {
  steps: ActivityStep[];
  summary: ActivitySummary;
  /** 折叠态持久化 key(行模型给的块身份,流式生长中不漂移)。 */
  uiStateKey: string;
  cwd?: string;
  /**
   * 组头耗时。**没有块级来源** —— 调用方给的是消息级耗时(一条消息里有多个
   * 活动块时它们共享同一个数字),所以只在轮次落定后显示,运行中留空。
   */
  durationMs?: number;
  /** 这一组此刻正在跑:自动展开 + 组头播报当前这一步,轮次结束后自动收起。 */
  running?: boolean;
  /** 用户没碰过时的默认展开态(子代理内部按步数阈值决定)。 */
  defaultExpanded?: boolean;
  /**
   * 组内没有结果的步骤按什么归属报(默认「运行中」)。子代理那层的 run 已经终结
   * 时传 failed / canceled / unknown —— 那些步永远等不到结果了。
   */
  pendingOutcome?: PendingOutcome;
};

export function ActivityBlock({
  steps,
  summary,
  uiStateKey,
  cwd,
  durationMs,
  running = false,
  defaultExpanded = false,
  pendingOutcome,
}: ActivityBlockProps) {
  const { t } = useUiTranslation();
  // 自动行为走 fallback、用户选择走 store:运行中 fallback=true(自动展开),
  // 轮次结束 fallback 变回默认值(自动收起);用户点过一次就写进 store,
  // 从此以用户的选择为准,不被自动收起覆盖。
  const [expanded, setExpanded] = useTranscriptBooleanState(
    uiStateKey,
    running || defaultExpanded,
  );
  // 用户点过省略行之后就以用户的选择为准(与展开态同一条约定),运行中新来的
  // 步骤不会把已经摊开的时间轴重新收回去。
  const [showAllSteps, setShowAllSteps] = useTranscriptBooleanState(
    `${uiStateKey}:all`,
    false,
  );
  const { mounted, onTransitionEnd } = useCollapsible(expanded);

  const handleToggle = (event: React.MouseEvent<HTMLButtonElement>) => {
    if (shouldIgnoreClickForSelection(event)) return;
    setExpanded((v) => !v);
  };

  // 单条不成组:一段活动只有一步时不套「1 步」的壳,直接渲染那一行活动行。
  // 组头是折叠态唯一的信息出口 —— 只有一步时它没有任何信息可出(汇总就是那一
  // 行自己),多一层壳只是多一次点击。运行态同理:这一行本来就看得见,「正在跑
  // 的活动块自动展开」在这里已经天然成立,不需要额外的自动展开。
  if (steps.length === 1) {
    return (
      <ActivityRow
        step={steps[0]}
        cwd={cwd}
        pendingOutcome={pendingOutcome}
        standalone
      />
    );
  }

  const elidedCount =
    running && !showAllSteps && steps.length > RUNNING_ELIDE_OVER
      ? steps.length - RUNNING_TAIL
      : 0;
  const shownSteps = elidedCount > 0 ? steps.slice(elidedCount) : steps;

  const durationLabel =
    !running && durationMs !== undefined && durationMs > 0
      ? t("activity.header.duration", {
          seconds: (durationMs / 1000).toFixed(1),
        })
      : "";

  return (
    <div data-testid="activity-block" className="w-full">
      <button
        type="button"
        data-testid="activity-header"
        aria-expanded={expanded}
        // 刻意不给 aria-label:组头的可见内容(N 步 / 汇总 / N 失败)本身就是
        // 折叠态唯一的信息出口,套一个 "展开活动" 的标签会把它盖掉。
        onClick={handleToggle}
        className="flex w-full items-center gap-2 rounded px-1.5 py-1 text-left text-meta text-muted-foreground transition-colors duration-150 ease-out hover:bg-muted/55 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 motion-reduce:transition-none"
      >
        <ChevronRight
          aria-hidden="true"
          className={cn(
            "size-3 shrink-0 text-muted-foreground transition-transform duration-150 ease-out motion-reduce:transition-none",
            expanded && "rotate-90",
          )}
        />
        <Layers
          aria-hidden="true"
          className="size-3 shrink-0 text-decorative-foreground"
        />
        {running ? (
          <span
            aria-hidden="true"
            className="size-1.5 shrink-0 rounded-full bg-status-running motion-safe:animate-pulse"
          />
        ) : null}
        <span className="shrink-0">
          {running
            ? t("activity.header.running", { count: summary.steps })
            : t("activity.header.steps", { count: summary.steps })}
        </span>
        {running ? (
          <span
            data-testid="activity-live-tail"
            aria-live="polite"
            className="min-w-0 flex-1 truncate font-mono text-muted-foreground"
          >
            {currentStepLabel(t, steps, cwd)}
          </span>
        ) : (
          <SummaryText summary={summary} />
        )}
        <FailureCount count={summary.failures} />
        {durationLabel ? (
          <span
            data-testid="activity-duration"
            // 这个数字是**整轮**耗时(块级没有来源),tooltip 把它说清楚,
            // 免得一条消息里的两个活动块看起来各自跑了这么久。
            title={t("activity.header.durationTitle")}
            className="shrink-0 font-mono text-muted-foreground"
          >
            {durationLabel}
          </span>
        ) : null}
      </button>
      <div
        aria-hidden={!expanded}
        onTransitionEnd={onTransitionEnd}
        className="grid transition-[grid-template-rows] duration-200 ease-out motion-reduce:transition-none"
        style={{ gridTemplateRows: expanded ? "1fr" : "0fr" }}
      >
        <div className="min-h-0 overflow-hidden">
          {
            // 折叠态一个步骤都不 mount(Hard invariant 9):一个 200 步的活动块
            // 折叠时把步骤与结果文本全挂进 DOM,整个 transcript 都要陪跑 layout。
            // 收缩动画期间 mounted 仍为 true(内容保持挂载供过渡),过渡结束才卸载。
            mounted ? (
              <div className="ml-1.5 flex flex-col border-l border-border pl-3">
                {elidedCount > 0 ? (
                  <ElidedSteps
                    steps={steps.slice(0, elidedCount)}
                    pendingOutcome={pendingOutcome}
                    onShowAll={() => setShowAllSteps(true)}
                  />
                ) : null}
                {shownSteps.map((step) => (
                  <ActivityRow
                    key={step.uiStateKey}
                    step={step}
                    cwd={cwd}
                    pendingOutcome={pendingOutcome}
                  />
                ))}
              </div>
            ) : null
          }
        </div>
      </div>
    </div>
  );
}

// ElidedSteps —— 运行中被省略掉的前 N 步收成的一行。形态与活动行完全一致
// (同样的行高 / 内边距 / 图标位),只是把工具名换成「前 N 步」、摘要换成这 N 步
// 的汇总 —— 折叠掉的东西必须还报得出账。箭头常显:它是唯一说明「这里还有东西」
// 的提示。汇总用与组头同一个 summarizeActivity,两处不可能分叉。
function ElidedSteps({
  onShowAll,
  pendingOutcome,
  steps,
}: {
  onShowAll: () => void;
  pendingOutcome?: PendingOutcome;
  steps: ActivityStep[];
}) {
  const { t } = useUiTranslation();
  const summary = React.useMemo(
    () => summarizeActivity(steps, pendingOutcome === "failed"),
    [pendingOutcome, steps],
  );

  const handleClick = (event: React.MouseEvent<HTMLButtonElement>) => {
    if (shouldIgnoreClickForSelection(event)) return;
    onShowAll();
  };

  return (
    <button
      type="button"
      data-testid="activity-elided"
      aria-expanded={false}
      onClick={handleClick}
      className="flex w-full items-center gap-2 rounded px-1.5 py-0.5 text-left font-mono text-meta text-muted-foreground transition-colors duration-150 ease-out hover:bg-muted/55 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 motion-reduce:transition-none"
    >
      <ChevronRight
        aria-hidden="true"
        className="size-3 shrink-0 text-decorative-foreground"
      />
      <Ellipsis
        aria-hidden="true"
        className="size-3 shrink-0 text-decorative-foreground"
      />
      <span className="shrink-0">
        {t("activity.elided.steps", { count: steps.length })}
      </span>
      <SummaryText summary={summary} />
      <FailureCount count={summary.failures} />
    </button>
  );
}

// FailureCount —— 组头与省略行共用:红色计数永远在可伸缩的汇总**之外**,
// 挤不下时先裁汇总(折叠是收起,不是让发生过的事消失)。
function FailureCount({ count }: { count: number }) {
  const { t } = useUiTranslation();
  if (count <= 0) return null;
  return (
    <span
      data-testid="activity-failures"
      className="shrink-0 font-mono font-semibold text-status-error"
    >
      {t("activity.summary.failures", { count })}
    </span>
  );
}

// SummaryText 渲染组头汇总:类目按行模型给定的顺序原样输出(不重排、不重算),
// 被截掉的类目只补一个省略号。失败计数不在这里 —— 它在组头里单独渲染,
// 不跟着这段一起被裁。
function SummaryText({ summary }: { summary: ActivitySummary }) {
  const parts = summary.parts;
  if (parts.length === 0 && !summary.truncated) {
    return <span className="min-w-0 flex-1" />;
  }
  return (
    <span
      data-testid="activity-summary"
      className="flex min-w-0 flex-1 items-center gap-1 overflow-hidden whitespace-nowrap font-mono text-muted-foreground"
    >
      {parts.map((part, idx) => (
        <React.Fragment key={part.category}>
          {idx > 0 ? <Separator /> : null}
          <SummaryPart part={part} />
        </React.Fragment>
      ))}
      {summary.truncated ? (
        <>
          {parts.length > 0 ? <Separator /> : null}
          <span>{"…"}</span>
        </>
      ) : null}
    </span>
  );
}

function Separator() {
  return (
    <span aria-hidden="true" className="text-border-strong">
      {"·"}
    </span>
  );
}

function SummaryPart({ part }: { part: ActivitySummaryPart }) {
  const { t } = useUiTranslation();
  if (part.category === "edit") {
    return (
      <span className="flex shrink-0 items-center gap-1">
        <span>
          {t("activity.summary.edit", { count: part.files ?? part.count })}
        </span>
        {part.plus ? (
          <span className="font-semibold text-status-running">
            +{part.plus}
          </span>
        ) : null}
        {part.minus ? (
          <span className="font-semibold text-destructive">−{part.minus}</span>
        ) : null}
      </span>
    );
  }
  return <span className="shrink-0">{categoryLabel(t, part)}</span>;
}

// categoryLabel 逐类目静态 t(...) —— 拼接 key 会让 i18n 守卫(静态 key 扫描)
// 看不见这些文案,双语齐全就没人守了。
function categoryLabel(
  t: ReturnType<typeof useUiTranslation>["t"],
  part: ActivitySummaryPart,
): string {
  const count = part.count;
  switch (part.category) {
    case "thinking":
      return t("activity.summary.thinking", { count });
    case "read":
      return t("activity.summary.read", { count });
    case "write":
      return t("activity.summary.write", { count });
    case "command":
      return t("activity.summary.command", { count });
    default:
      return t("activity.summary.other", { count });
  }
}

// currentStepLabel:运行中的组头播报当前这一步(名字 + 摘要),取组内最后一步。
// 名字与摘要走与活动行同一个 stepLabel —— 同一步在组头与展开后必须叫同一个名字。
function currentStepLabel(
  t: ReturnType<typeof useUiTranslation>["t"],
  steps: ActivityStep[],
  cwd?: string,
): string {
  const last = steps.at(-1);
  if (!last) return "";
  if (last.type === "thinking") return "";
  const { fileCount, name, summary } = stepLabel(last.toolBlock, cwd);
  const tail = fileCount
    ? t("canonical.fileEdit.fileCount", { count: fileCount })
    : summary;
  return tail ? `${name} ${tail}` : name;
}
