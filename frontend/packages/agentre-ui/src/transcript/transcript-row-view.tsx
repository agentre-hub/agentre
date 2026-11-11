// transcript-row-view: 行级虚拟化的渲染层 —— 一个 TranscriptRow 渲染成一个虚拟行。
// 消息 chrome 按分片标志挂载:首行走 ChatMessage(article + 头像 + 名字 + 时间戳,
// 纯文本消息恰好一行,DOM 与 message 级虚拟化完全一致);后续行用幽灵 gutter 对齐
// 头像列;末行追加 footer(meta/copy/edit)+ RetryNotice + TypingIndicator + ErrorCard。
// 静态依赖走 TranscriptRenderContext,行组件的 memo 比较只剩 row 引用 + 少量 live 标量。
import * as React from "react";
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  Pencil,
  RefreshCw,
  TriangleAlert,
  WifiOff,
} from "lucide-react";

import {
  makeMentionDecorator,
  prepareMentionText,
} from "../chat-input/mentions/transcript";
import { useUiTranslation } from "../i18n";
import {
  formatCompactTurnDuration,
  formatTokensPerSec,
  formatTurnDuration,
} from "../lib/format-duration";
import { cn } from "../lib/utils";
import { CommandCard } from "../onboarding/command-card";
import { Button } from "../ui/button";
import {
  DialogShell,
  DialogShellBody,
  DialogShellHeader,
} from "../ui/dialog-shell";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "../ui/tooltip";

import { ActivityBlock } from "./activity-block/block";
import { AutoTriggerBanner } from "./auto-trigger-banner";
import { PlanApproveCard } from "./canonical-tool/plan-approve-request/card";
import type { PlanActionStream } from "./canonical-tool/props";
import { CanonicalToolRouter } from "./canonical-tool/registry";
import { ShortcutHintsProvider } from "./canonical-tool/tool-permission/shortcut-hints";
import { isPendingToolPermission } from "./canonical-tool/tool-permission/use-tool-permission-decision";
import { CompactBoundaryDivider } from "./compact-boundary-divider";
import type {
  RetryNotice,
  TranscriptBlock,
  TranscriptLocalCommand,
} from "./dto";
import { LocalCommandCard } from "./local-command/card";
import { MarkdownText, StreamingMarkdown } from "./markdown-text";
import { MessageCopyButton, MessageRow } from "./message-row";
import {
  compactTokenCount,
  fitMetaLine,
  normalizeModelName,
  type MetaFit,
} from "./meta-fit";
import { OpenClawExecApprovalCard } from "./openclaw-exec-approval/card";
import { useOptionalPort } from "./ports-context";
import { ThinkingBlock } from "./thinking-block";
import { ToolApprovalCard } from "./tool-approval/card";
import type { TranscriptRow, TranscriptRowItem } from "./transcript-rows";
import {
  classifyTurnFailure,
  loginCommandFor,
  turnFailureFact,
} from "./turn-failure";
import { computeLiveTurnStats, type LiveTurnInput } from "./turn-stats";

// ─── 会话级静态渲染依赖 ───────────────────────────────────────────────────────

export type TranscriptRenderContextValue = {
  agentName: string;
  /**
   * assistant 行的头像节点，**必填**，由宿主给。
   *
   * 与 MessageRow 的 `avatar` 同一个理由：头像画什么是身份问题（桌面端的 16 色
   * 调色板 + icon-registry + 自定义头像图片，agentre-server 另有一套），不是布局
   * 问题。会话级常量，所以挂在这个上下文上而不是逐行传 —— 顺带保住行级 memo。
   * 尺寸对齐用包导出的 `MESSAGE_AVATAR_CLASS`。
   */
  agentAvatar: React.ReactNode;
  cwd?: string;
  sessionId: number;
  tabStateKey?: string;
  onPlanActionStarted?: (stream: PlanActionStream, userText: string) => void;
  /** 停掉 AgentSpawn 卡对应的正在运行子 agent(按 tool_use_id);只读/不支持时不传。 */
  onStopSubagent?: (toolUseId: string) => void | Promise<void>;
  /** 停掉本地命令；ChatPanel 是生命周期 owner，只读模式不传。 */
  onStopLocalCommand?: (terminalId: string) => void | Promise<void>;
  /** 只读模式下不传；有值时才渲染「重新生成」按钮。 */
  onRerun?: (messageId: number) => void;
  /** 只读模式下不传；有值时错误卡渲染「继续」按钮。 */
  onContinue?: (messageId: number) => void;
  /**
   * 原样重试这条失败消息对应的那一轮，**不经**「永久丢失」确认 —— 只挂在瞬态失败
   * （过载 / 限流 / 网络）的错误卡上。不传时这些卡退回「继续 / 重新生成」。
   */
  onRetryTurn?: (messageId: number) => void;
  /** 会话的后端类型（如 `claudecode`），错误卡据此给登录失效的命令卡；不传不给。 */
  backendType?: string;
  /** 只读模式下不传；有值时才渲染「编辑」按钮。 */
  onEdit?: (messageId: number) => void;
  /**
   * 统计行的密度。`"compact"`（手机宽度）：一行、数字缩写、放不下按 tok/s → 用时 →
   * 截模型名的顺序让、点行开「这一轮」明细、复制只剩 44px 图标。不传即桌面那一档。
   */
  metaDensity?: "default" | "compact";
  /** 用户消息的对齐；`"end"` 把用户消息排到尾侧。不传与桌面一致。 */
  userMessageAlign?: "start" | "end";
  /**
   * 待决工具权限请求画在哪。`"docked"`：宿主把它交给 `ToolPermissionDock` 停在
   * 输入框的位置，转录不再画待决的工具权限卡（已决的照常显示；提问卡、计划审批
   * 不受影响）。不传即桌面那一档 `"inline"`。
   */
  decisionPlacement?: "inline" | "docked";
  /**
   * 审批卡不画 ↵ / Esc 键帽（触屏）。转录之外直接用 `CanonicalToolRouter` 的宿主
   * 改用 `ShortcutHintsProvider`。不传与桌面一致。
   */
  hideShortcutHints?: boolean;
};

export const TranscriptRenderContext =
  React.createContext<TranscriptRenderContextValue | null>(null);

// ─── 消息 chrome 组件──────────────────────────────────────

function formatHHmm(ms: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

type ChatMessageProps = React.ComponentProps<"article"> & {
  author: string;
  /**
   * assistant 行的头像节点。user 行不用它 —— 「我」是中性的、与身份无关，
   * 由本组件自己画（见下）。
   */
  avatar: React.ReactNode;
  children: React.ReactNode;
  meta?: React.ReactNode;
  /** R17:非本机发出的用户消息的来源设备显示名;undefined = 不渲染来源标识。 */
  source?: string;
  time: string;
  /** 透传给 MessageRow 的对齐。 */
  align?: "start" | "end";
  /** "assistant" (默认): 渲染 agent avatar + 名字。
   *  "user": 渲染中性的「我」头像 —— 与 agent 头像视觉对称，但走 muted
   *  色阶不抢焦点，把主视觉留给 agent。 */
  variant?: "user" | "assistant";
};

function ChatMessage({
  align,
  author,
  avatar,
  children,
  className,
  meta,
  source,
  time,
  variant = "assistant",
  ...props
}: ChatMessageProps) {
  const { t } = useUiTranslation();
  const isUser = variant === "user";
  return (
    <MessageRow
      align={align}
      className={className}
      avatar={
        isUser ? (
          <span
            aria-label={t("chat.message.me")}
            role="img"
            className="inline-flex size-7 shrink-0 items-center justify-center rounded-lg bg-muted text-meta font-semibold text-muted-foreground"
          >
            {t("chat.message.me")}
          </span>
        ) : (
          avatar
        )
      }
      name={isUser ? null : author}
      headerExtra={
        <>
          <span className="text-meta text-muted-foreground">{time}</span>
          {/* R17:来源标识跟随在角色标签之后,极简 inline pill,不占独立行、不用状态色
              (它是设备属性,不是状态)。只有非本机发出的用户消息才有 source。 */}
          {source ? (
            <span className="inline-flex items-center rounded-full bg-muted px-1.5 text-meta text-muted-foreground">
              {t("chat.message.fromDevice", { device: source })}
            </span>
          ) : null}
        </>
      }
      footer={meta}
      {...props}
    >
      {children}
    </MessageRow>
  );
}

type MetaDensity = NonNullable<TranscriptRenderContextValue["metaDensity"]>;

type MessageMetaProps = {
  cacheCreationTokens?: number;
  cachedTokens?: number;
  completionTokens: number;
  /** 不传即桌面那一档；见 TranscriptRenderContextValue.metaDensity。 */
  density?: MetaDensity;
  durationMs: number;
  firstTokenMs?: number;
  liveTurn?: LiveTurnInput | null;
  model: string;
  onRerun?: () => void;
  promptTokens: number;
  reasoningTokens?: number;
  tokensPerSec?: number;
};

function useNow(enabled: boolean): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    if (!enabled) return;
    setNow(Date.now());
    const id = window.setInterval(() => setNow(Date.now()), 200);
    return () => window.clearInterval(id);
  }, [enabled]);
  return now;
}

function MessageMeta({
  cacheCreationTokens = 0,
  cachedTokens = 0,
  completionTokens,
  density = "default",
  durationMs,
  firstTokenMs = 0,
  liveTurn,
  model,
  onRerun,
  promptTokens,
  reasoningTokens = 0,
  tokensPerSec = 0,
}: MessageMetaProps) {
  const { t } = useUiTranslation();
  const now = useNow(liveTurn != null);
  const live = liveTurn ? computeLiveTurnStats({ ...liveTurn, now }) : null;
  const displayModel = live?.model || model;
  const displayDurationMs = live?.durationMs ?? durationMs;
  const displayPrompt = live?.promptTokens ?? promptTokens;
  const displayCompletion = live?.completionTokens ?? completionTokens;
  const displayCached = live?.cachedTokens ?? cachedTokens;
  const displayCacheCreation = live?.cacheCreationTokens ?? cacheCreationTokens;
  const displayReasoning = live?.reasoningTokens ?? reasoningTokens;
  const waitingFirstToken = live?.waitingFirstToken ?? false;
  const displayFirstTokenMs = waitingFirstToken
    ? (live?.firstTokenMs ?? 0)
    : (live?.firstTokenMs ?? firstTokenMs);
  const displayTokensPerSec = live?.tokensPerSec ?? tokensPerSec;
  const completionApprox = live?.completionApprox ?? false;
  const durationLabel = formatTurnDuration(displayDurationMs);
  // 后端一个 token 数都没上报时（如 OpenClaw 网关不发 usage 帧），渲染「↑0 ↓0」
  // 等于把「没上报」说成「用了 0 个 token」。这种情况整块隐藏计数，只留耗时。
  const hasUsage =
    displayPrompt > 0 ||
    displayCompletion > 0 ||
    displayCached > 0 ||
    displayCacheCreation > 0 ||
    displayReasoning > 0;

  const approxMark = completionApprox ? "~" : "";
  const showFirstToken =
    waitingFirstToken ||
    (!liveTurn && firstTokenMs > 0) ||
    (liveTurn != null && !waitingFirstToken);
  const showSpeed = displayTokensPerSec > 0;
  const firstTokenLabel = waitingFirstToken
    ? durationLabel
    : formatTurnDuration(displayFirstTokenMs);
  const speedLabel = `${approxMark}${t("chat.meta.tokensPerSec", {
    value: formatTokensPerSec(displayTokensPerSec),
  })}`;

  // 明细行：桌面的悬停 tooltip 与紧凑档的「这一轮」弹层共用这一份构造。
  const rows: MetaDetailRow[] = [
    { label: t("chat.meta.model"), value: displayModel || "—" },
  ];
  if (hasUsage) {
    rows.push(
      {
        label: t("chat.meta.prompt"),
        value: displayPrompt > 0 ? displayPrompt.toLocaleString() : "—",
      },
      {
        label: t("chat.meta.completion"),
        value: `${approxMark}${displayCompletion.toLocaleString()}`,
      },
    );
  } else if (!liveTurn) {
    rows.push({
      label: t("chat.meta.usage"),
      value: t("chat.meta.usageUnavailable"),
    });
  }
  if (displayCached > 0) {
    rows.push({
      label: t("chat.meta.cacheHit"),
      value: displayCached.toLocaleString(),
    });
  }
  if (displayCacheCreation > 0) {
    rows.push({
      label: t("chat.meta.cacheWrite"),
      value: displayCacheCreation.toLocaleString(),
    });
  }
  if (displayReasoning > 0) {
    rows.push({
      label: t("chat.meta.reasoning"),
      value: displayReasoning.toLocaleString(),
    });
  }
  if (showFirstToken) {
    rows.push({
      label: t("chat.meta.firstToken"),
      value: waitingFirstToken
        ? durationLabel
        : formatTurnDuration(displayFirstTokenMs),
    });
  }
  rows.push({ label: t("chat.meta.duration"), value: durationLabel });
  if (showSpeed) {
    rows.push({ label: t("chat.meta.outputSpeed"), value: speedLabel });
  }

  if (density === "compact") {
    return (
      <CompactMessageMeta
        model={normalizeModelName(displayModel)}
        counts={
          hasUsage
            ? {
                prompt:
                  displayPrompt > 0 ? compactTokenCount(displayPrompt) : null,
                completion: `${approxMark}${compactTokenCount(displayCompletion)}`,
              }
            : null
        }
        duration={formatCompactTurnDuration(displayDurationMs)}
        speed={showSpeed ? speedLabel : null}
        rows={rows}
        onRerun={onRerun}
      />
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {/* 宿主(SaaS 前端等)不保证树上有 TooltipProvider,radix-ui 1.4 的 Root
          没有祖先 Provider 会在 render 阶段直接抛错,所以这里自带一份。
          delayDuration 取 200ms,与桌面端转录区外层 Provider 一致。 */}
      <TooltipProvider delayDuration={200}>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              className="inline-flex items-center gap-1.5 rounded px-1 py-0.5 hover:bg-accent/60 hover:on-accent"
              aria-label={t("chat.meta.tokenDetails")}
            >
              {displayModel ? (
                <>
                  <span>{displayModel}</span>
                  <span className="text-border-strong">·</span>
                </>
              ) : null}
              {hasUsage ? (
                <span
                  data-testid="message-token-counts"
                  className="inline-flex items-center gap-1.5"
                >
                  {displayPrompt > 0 ? (
                    <span className="inline-flex items-center gap-0.5">
                      <ArrowUp className="size-2.5" aria-hidden="true" />
                      {displayPrompt.toLocaleString()}
                    </span>
                  ) : null}
                  <span className="inline-flex items-center gap-0.5">
                    <ArrowDown className="size-2.5" aria-hidden="true" />
                    {approxMark}
                    {displayCompletion.toLocaleString()}
                  </span>
                  <span className="text-border-strong">·</span>
                </span>
              ) : null}
              {waitingFirstToken ? null : <span>{durationLabel}</span>}
              {showFirstToken ? (
                <span data-testid="message-first-token">
                  {waitingFirstToken ? null : (
                    <span className="text-border-strong">· </span>
                  )}
                  {firstTokenLabel}
                </span>
              ) : null}
              {showSpeed ? (
                <span data-testid="message-tokens-per-sec">
                  <span className="text-border-strong">· </span>
                  {speedLabel}
                </span>
              ) : null}
            </button>
          </TooltipTrigger>
          <TooltipContent className="font-mono text-meta">
            <table className="border-separate border-spacing-x-3 border-spacing-y-0.5">
              <tbody>
                {rows.map((row) => (
                  <tr key={row.label}>
                    <td className="text-left text-muted-foreground">
                      {row.label}
                    </td>
                    <td className="text-right tabular-nums">{row.value}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TooltipContent>
        </Tooltip>
      </TooltipProvider>
      {onRerun ? <RerunButton onRerun={onRerun} /> : null}
    </div>
  );
}

type MetaDetailRow = { label: string; value: string };

function RerunButton({ onRerun }: { onRerun: () => void }) {
  const { t } = useUiTranslation();
  return (
    <Button
      type="button"
      variant="ghost"
      size="xs"
      className="ml-1 h-6 gap-1 px-1.5 text-meta text-muted-foreground"
      onClick={onRerun}
    >
      <RefreshCw data-icon="inline-start" aria-hidden="true" />
      {t("chat.actions.regenerate")}
    </Button>
  );
}

type CompactMetaSegments = {
  /** 规整后的模型名；空串 = 没有模型名这一段。 */
  model: string;
  /** ↑↓ 计数（已缩写）；null = 后端没上报用量，整段不画。 */
  counts: { prompt: string | null; completion: string } | null;
  duration: string;
  speed: string | null;
};

const META_SEPARATOR = <span className="text-border-strong">·</span>;

// 各段的内容。可见行与量宽度用的标尺画的是同一份，宽度才量得准。
function MetaCountsSegment({
  counts,
}: {
  counts: NonNullable<CompactMetaSegments["counts"]>;
}) {
  return (
    <>
      {counts.prompt !== null ? (
        <span className="inline-flex items-center gap-0.5">
          <ArrowUp className="size-2.5" aria-hidden="true" />
          {counts.prompt}
        </span>
      ) : null}
      <span className="inline-flex items-center gap-0.5">
        <ArrowDown className="size-2.5" aria-hidden="true" />
        {counts.completion}
      </span>
    </>
  );
}

const SEGMENT_CLASS = "inline-flex shrink-0 items-center gap-1";

// 紧凑档的统计行：一行。放不下时由 fitMetaLine 判定让到哪一档 —— 这里只负责量宽度
// （标尺里每段的实际渲染宽度 + 行的可用宽度）并按结果渲染。宽度变化（转屏、字体载入）
// 由 ResizeObserver 触发，运行中那一轮每次刷新内容都会变，由 layout effect 重判。
function CompactMessageMeta({
  model,
  counts,
  duration,
  speed,
  rows,
  onRerun,
}: CompactMetaSegments & {
  rows: MetaDetailRow[];
  onRerun?: () => void;
}) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);
  const [fit, setFit] = React.useState<MetaFit & { modelMin: number }>({
    level: 0,
    more: false,
    modelMin: 0,
  });
  const lineRef = React.useRef<HTMLButtonElement>(null);
  const segmentsRef = React.useRef<HTMLSpanElement>(null);
  const rulerRef = React.useRef<HTMLSpanElement>(null);

  const refit = React.useCallback(() => {
    const line = lineRef.current;
    const segments = segmentsRef.current;
    const ruler = rulerRef.current;
    if (!line || !segments || !ruler) return;
    const gap = Number.parseFloat(getComputedStyle(segments).columnGap) || 0;
    const width = (kind: string) =>
      ruler
        .querySelector(`[data-meta-ruler="${kind}"]`)
        ?.getBoundingClientRect().width ?? 0;
    // 每段都把它前面的间距算进去；第一段前面没有间距，可用宽度相应补回一份。
    const withGap = (kind: string) => {
      const w = width(kind);
      return w > 0 ? w + gap : 0;
    };
    const available = line.getBoundingClientRect().width;
    const next = fitMetaLine({
      available: available > 0 ? available + gap : 0,
      model: withGap("model"),
      modelMin: withGap("model-min"),
      fixed: withGap("counts"),
      duration: withGap("duration"),
      speed: withGap("speed"),
      more: withGap("more"),
    });
    const modelMin = width("model-min");
    setFit((prev) =>
      prev.level === next.level &&
      prev.more === next.more &&
      prev.modelMin === modelMin
        ? prev
        : { ...next, modelMin },
    );
  }, []);

  const countsKey = counts ? `${counts.prompt}|${counts.completion}` : "";
  React.useLayoutEffect(() => {
    refit();
  }, [refit, model, countsKey, duration, speed]);

  React.useEffect(() => {
    const line = lineRef.current;
    const ruler = rulerRef.current;
    if (!line || !ruler || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => refit());
    observer.observe(line);
    // 标尺的宽度随字体载入变化，行宽却不变 —— 两个都得看。
    observer.observe(ruler);
    return () => observer.disconnect();
  }, [refit]);

  const truncateModel = fit.level >= 3;
  const modelMinText = model.length > 6 ? `${model.slice(0, 6)}…` : model;
  const durationSep = model !== "" || counts !== null;

  return (
    <div
      data-meta-density="compact"
      className="flex min-w-0 flex-1 basis-0 items-center gap-1.5 font-sans tabular-nums"
    >
      <button
        ref={lineRef}
        type="button"
        data-testid="message-meta-line"
        aria-label={t("chat.meta.tokenDetails")}
        aria-haspopup="dialog"
        onClick={() => setOpen(true)}
        className="relative flex min-w-0 flex-1 basis-0 items-center overflow-hidden whitespace-nowrap rounded py-0.5 text-left"
      >
        <span
          ref={segmentsRef}
          data-testid="message-meta-visible"
          className="flex min-w-0 items-center gap-1"
        >
          {model ? (
            <span
              data-testid="message-meta-model"
              className={truncateModel ? "min-w-0 truncate" : "shrink-0"}
              style={truncateModel ? { minWidth: fit.modelMin } : undefined}
            >
              {model}
            </span>
          ) : null}
          {counts ? (
            <span className={SEGMENT_CLASS}>
              {model ? META_SEPARATOR : null}
              <MetaCountsSegment counts={counts} />
            </span>
          ) : null}
          {fit.level < 2 ? (
            <span className={SEGMENT_CLASS}>
              {durationSep ? META_SEPARATOR : null}
              {duration}
            </span>
          ) : null}
          {fit.level < 1 && speed !== null ? (
            <span className={SEGMENT_CLASS}>
              {META_SEPARATOR}
              {speed}
            </span>
          ) : null}
          {fit.more ? (
            <span
              data-testid="message-meta-more"
              aria-hidden="true"
              className="shrink-0 px-0.5"
            >
              ›
            </span>
          ) : null}
        </span>
        {/* 标尺：每段各画一份、不受档位影响，只用来量宽度。 */}
        <span
          ref={rulerRef}
          aria-hidden="true"
          className="pointer-events-none invisible absolute left-0 top-0 flex w-max items-center gap-1"
        >
          {model ? (
            <>
              <span data-meta-ruler="model" className="shrink-0">
                {model}
              </span>
              <span data-meta-ruler="model-min" className="shrink-0">
                {modelMinText}
              </span>
            </>
          ) : null}
          {counts ? (
            <span data-meta-ruler="counts" className={SEGMENT_CLASS}>
              {model ? META_SEPARATOR : null}
              <MetaCountsSegment counts={counts} />
            </span>
          ) : null}
          <span data-meta-ruler="duration" className={SEGMENT_CLASS}>
            {durationSep ? META_SEPARATOR : null}
            {duration}
          </span>
          {speed !== null ? (
            <span data-meta-ruler="speed" className={SEGMENT_CLASS}>
              {META_SEPARATOR}
              {speed}
            </span>
          ) : null}
          <span data-meta-ruler="more" className="shrink-0 px-0.5">
            ›
          </span>
        </span>
      </button>
      {onRerun ? <RerunButton onRerun={onRerun} /> : null}
      <DialogShell open={open} onOpenChange={setOpen} size="sm">
        <DialogShellHeader
          title={t("chat.meta.turnDetails")}
          onClose={() => setOpen(false)}
        />
        <DialogShellBody>
          <dl className="flex flex-col overflow-hidden rounded-lg bg-field-fill py-1">
            {rows.map((row) => (
              <div
                key={row.label}
                className="flex min-h-11 items-center gap-3 px-3.5 text-sm"
              >
                <dt className="shrink-0 text-foreground">{row.label}</dt>
                <dd className="ml-auto truncate font-mono text-xs tabular-nums text-muted-foreground">
                  {row.value}
                </dd>
              </div>
            ))}
          </dl>
        </DialogShellBody>
      </DialogShell>
    </div>
  );
}

// 页脚的 meta 段（模型 · token · 耗时 · 重新生成）出不出现。错误卡要据此判断页脚
// 是否已经有「重新生成」，所以与页脚共用这一个判据。
function showsMessageMeta({
  durationMs,
  liveTurn,
  model,
  promptTokens,
  completionTokens,
  cachedTokens,
  cacheCreationTokens,
  reasoningTokens,
}: {
  durationMs: number;
  liveTurn?: LiveTurnInput | null;
  model: string;
  promptTokens: number;
  completionTokens: number;
  cachedTokens?: number;
  cacheCreationTokens?: number;
  reasoningTokens?: number;
}): boolean {
  return (
    durationMs > 0 ||
    liveTurn != null ||
    promptTokens > 0 ||
    completionTokens > 0 ||
    (cachedTokens ?? 0) > 0 ||
    (cacheCreationTokens ?? 0) > 0 ||
    (reasoningTokens ?? 0) > 0 ||
    Boolean(model)
  );
}

type AssistantMessageActionsProps = {
  cacheCreationTokens?: number;
  cachedTokens?: number;
  completionTokens: number;
  copyText: string;
  density?: MetaDensity;
  durationMs: number;
  firstTokenMs?: number;
  liveTurn?: LiveTurnInput | null;
  model: string;
  onRerun?: () => void;
  promptTokens: number;
  reasoningTokens?: number;
  tokensPerSec?: number;
};

function AssistantMessageActions({
  cacheCreationTokens,
  cachedTokens,
  completionTokens,
  copyText,
  density,
  durationMs,
  firstTokenMs,
  liveTurn,
  model,
  onRerun,
  promptTokens,
  reasoningTokens,
  tokensPerSec,
}: AssistantMessageActionsProps) {
  const { t } = useUiTranslation();

  return (
    <>
      {showsMessageMeta({
        durationMs,
        liveTurn,
        model,
        promptTokens,
        completionTokens,
        cachedTokens,
        cacheCreationTokens,
        reasoningTokens,
      }) ? (
        <MessageMeta
          model={model}
          promptTokens={promptTokens}
          completionTokens={completionTokens}
          cachedTokens={cachedTokens}
          cacheCreationTokens={cacheCreationTokens}
          reasoningTokens={reasoningTokens}
          durationMs={durationMs}
          firstTokenMs={firstTokenMs}
          tokensPerSec={tokensPerSec}
          liveTurn={liveTurn}
          onRerun={onRerun}
          density={density}
        />
      ) : null}
      <MessageCopyButton
        text={copyText}
        label={t("common.copy")}
        ariaLabel={t("chat.actions.copyOutput")}
        successTitle={t("chat.actions.copyOutputDone")}
        errorTitle={t("chat.actions.copyOutputFailed")}
        iconOnly={density === "compact"}
      />
    </>
  );
}

// UserMessageActions 渲染 user 气泡的 action 行：目前只有「编辑」。
// 作为 `meta` prop 传入 ChatMessage，常驻显示在消息下方。
function UserMessageActions({ onEdit }: { onEdit: () => void }) {
  const { t } = useUiTranslation();
  return (
    <div className="flex items-center gap-1.5">
      <Button
        type="button"
        variant="ghost"
        size="xs"
        className="h-6 gap-1 px-1.5 text-meta text-muted-foreground"
        onClick={onEdit}
      >
        <Pencil data-icon="inline-start" aria-hidden="true" />
        {t("common.edit")}
      </Button>
    </div>
  );
}

// ErrorCard：一轮失败时挂在 assistant 消息末尾。标题是按类别写好的一句事实，原文
// （stopErr.Error()，常是英文 + JSON）只进默认折叠的「详情」；出口按类别给 ——
// 只给这一类走得通的那条路（见 turn-failure.ts）。
function ErrorCard({
  text,
  backendType,
  footerHasRerun = false,
  onContinue,
  onRerun,
  onRetryTurn,
}: {
  text: string;
  backendType?: string;
  /** 页脚已经有「重新生成」时卡内不再放一颗同样的。 */
  footerHasRerun?: boolean;
  onContinue?: () => void;
  onRerun?: () => void;
  onRetryTurn?: () => void;
}) {
  const { t } = useUiTranslation();
  const [detailsOpen, setDetailsOpen] = React.useState(false);
  const detailsId = React.useId();
  const kind = classifyTurnFailure(text);
  const transient =
    kind === "overloaded" || kind === "rateLimited" || kind === "network";
  const loginCommand =
    kind === "authExpired" ? loginCommandFor(backendType) : undefined;
  // 瞬态失败原样重试同一条消息；宿主没接这条路（agentre-server 的钉）时退回
  // 「继续 / 重新生成」。登录失效重试必然再失败，不给任何重来的按钮。
  const retryTurn = transient ? onRetryTurn : undefined;
  const resume = kind !== "authExpired" && !retryTurn;
  const continueAction = resume ? onContinue : undefined;
  const rerunAction = resume && !footerHasRerun ? onRerun : undefined;

  return (
    <section
      data-testid="turn-error-card"
      data-selectable-text="true"
      className="flex w-full max-w-measure flex-col gap-2 rounded-lg bg-destructive-soft px-4 py-2.5"
    >
      <div className="flex items-center gap-3">
        <TriangleAlert
          className="size-4 shrink-0 text-status-error"
          aria-hidden="true"
        />
        <span className="min-w-0 flex-1 text-aux text-destructive-text">
          {turnFailureFact(kind, t)}
        </span>
        <div className="flex shrink-0 items-center gap-2">
          {retryTurn ? (
            <Button
              type="button"
              size="xs"
              variant="outline"
              onClick={retryTurn}
            >
              {t("chat.errorCard.retryTurn")}
            </Button>
          ) : null}
          {continueAction ? (
            <Button
              type="button"
              size="xs"
              variant="outline"
              onClick={continueAction}
            >
              {t("chat.errorCard.continue")}
            </Button>
          ) : null}
          {rerunAction ? (
            <Button
              type="button"
              size="xs"
              variant="outline"
              onClick={rerunAction}
            >
              <RefreshCw data-icon="inline-start" aria-hidden="true" />
              {t("chat.actions.regenerate")}
            </Button>
          ) : null}
          <Button
            type="button"
            size="xs"
            variant="ghost"
            className="text-muted-foreground"
            aria-expanded={detailsOpen}
            aria-controls={detailsId}
            onClick={() => setDetailsOpen((open) => !open)}
          >
            <ChevronDown
              data-icon="inline-start"
              aria-hidden="true"
              className={cn(
                "transition-transform motion-reduce:transition-none",
                detailsOpen && "rotate-180",
              )}
            />
            {t("chat.errorCard.details")}
          </Button>
        </div>
      </div>
      {loginCommand ? (
        <CommandCard
          label={t("chat.errorCard.loginLabel")}
          command={loginCommand}
        />
      ) : null}
      {detailsOpen ? (
        <div id={detailsId} className="flex flex-col items-start gap-1">
          <pre className="max-h-48 w-full select-text overflow-auto whitespace-pre-wrap break-all rounded-lg bg-code-surface px-3 py-2 font-mono text-xs text-code-foreground">
            {text}
          </pre>
          <MessageCopyButton
            text={text}
            ariaLabel={t("chat.errorCard.copyDetails")}
          />
        </div>
      ) : null}
    </section>
  );
}

// RetryIndicator 是 Typing / Compacting / Disconnected 的第四个兄弟:同一个位置、
// 一行、互斥。上游在退避重试是"在等"而不是"出错",所以只用 status-waiting 的
// spinner + 一句中文事实 + 次数;retry.message / retry.details 是机器原话,只进 title,不上屏。
function RetryIndicator({ retry }: { retry: RetryNotice }) {
  const { t } = useUiTranslation();
  const label =
    retry.attempt > 0 && retry.maxAttempts > 0
      ? t("chat.retry.label", {
          attempt: retry.attempt,
          max: retry.maxAttempts,
        })
      : retry.attempt > 0
        ? t("chat.retry.labelWithAttempt", { attempt: retry.attempt })
        : t("chat.retry.labelPlain");

  return (
    <div
      data-testid="transcript-retry"
      role="status"
      aria-live="polite"
      title={
        [retry.message, retry.details].filter(Boolean).join("\n") || undefined
      }
      className="flex items-center gap-2 py-1 text-aux text-muted-foreground"
    >
      <RefreshCw
        className="size-3.5 shrink-0 animate-spin text-status-waiting motion-reduce:animate-none"
        aria-hidden="true"
      />
      <span>{label}</span>
    </div>
  );
}

function TypingIndicator() {
  const { t } = useUiTranslation();
  // keyframe 自己控制 opacity (0.2 ↔ 1)，dot 颜色不再叠 /60，避免叠加后整体太淡看不见。
  // 6px 三点 + 1.5 gap 是「克制但可感知」的尺寸；动画通过 @theme 的 --animate-typing-dot 注册，
  // class 名 animate-typing-dot 由 Tailwind v4 解析为 animation: typing-dot 1.2s ease-in-out infinite。
  const dotClass =
    "size-1.5 rounded-full bg-muted-foreground animate-typing-dot motion-reduce:animate-none";
  return (
    <div
      aria-label={t("chat.typing.aria")}
      role="status"
      aria-live="polite"
      className="flex items-center gap-1.5 py-1"
    >
      <span className={dotClass} />
      <span className={cn(dotClass, "[animation-delay:0.15s]")} />
      <span className={cn(dotClass, "[animation-delay:0.3s]")} />
    </div>
  );
}

// CompactingIndicator 在 claudecode CLI 跑 /compact 期间替代 TypingIndicator,
// 让用户知道这段时间不是普通回答,而是在压缩上下文 (manual 或 auto)。文案旁
// 复用 TypingIndicator 的 dot 动画做"还在跑"的视觉信号。
function CompactingIndicator() {
  const { t } = useUiTranslation();
  const dotClass =
    "size-1.5 rounded-full bg-muted-foreground animate-typing-dot motion-reduce:animate-none";
  return (
    <div
      aria-label={t("chat.compacting.aria")}
      role="status"
      aria-live="polite"
      className="flex items-center gap-2 py-1 text-aux text-muted-foreground"
    >
      <div className="flex items-center gap-1">
        <span className={dotClass} />
        <span className={cn(dotClass, "[animation-delay:0.15s]")} />
        <span className={cn(dotClass, "[animation-delay:0.3s]")} />
      </div>
      <span>{t("chat.compacting.label")}</span>
    </div>
  );
}

// DisconnectedIndicator 是 TypingIndicator / CompactingIndicator 的第三个兄弟:
// 本机与执行该会话那台远端 daemon 之间的通道断了、客户端正在退避重连时,它顶替
// 打字指示器出现在转录流里同一个位置(随内容滚动,不是横幅、不常驻)。
//
// 断连不改会话的运行态取值 —— 远端此刻可能正在全速跑,所以文案要说清「远端仍在
// 运行」。同一组点尺寸、同一 typing-dot keyframe,只把周期放慢一倍(--animate-
// typing-dot-slow),让"网络在等"与"agent 在想"一眼可分。降级(prefers-reduced-
// motion)时停动画、点降到低透明度,标签与图形都还在,信息不丢。
function DisconnectedIndicator() {
  const { t } = useUiTranslation();
  const dotClass =
    "size-1.5 rounded-full bg-muted-foreground animate-typing-dot-slow motion-reduce:animate-none motion-reduce:opacity-55";
  return (
    <div
      aria-label={t("chat.disconnected.aria")}
      role="status"
      aria-live="polite"
      className="flex items-center gap-2 py-1 text-aux text-muted-foreground"
    >
      {/* 相位延迟随周期一同翻倍(0.15/0.3 → 0.3/0.6):同一条曲线放慢一倍,
          三点之间的相位差也要按同一倍率拉开,否则三点会挤成几乎同相。 */}
      <div className="flex items-center gap-1.5">
        <span className={dotClass} />
        <span className={cn(dotClass, "[animation-delay:0.3s]")} />
        <span className={cn(dotClass, "[animation-delay:0.6s]")} />
      </div>
      <WifiOff aria-hidden className="size-3.5 shrink-0 opacity-75" />
      <span>{t("chat.disconnected.label")}</span>
    </div>
  );
}

function ImageBlockView({ block }: { block: TranscriptBlock }) {
  const { t } = useUiTranslation();
  const image = (
    block as TranscriptBlock & {
      image?: { dataUrl?: string; mediaType?: string; name?: string };
    }
  ).image;
  if (!image?.dataUrl) return null;
  return (
    <a
      href={image.dataUrl}
      target="_blank"
      rel="noreferrer"
      className="block w-fit overflow-hidden rounded-lg bg-muted"
    >
      <img
        src={image.dataUrl}
        alt={image.name || image.mediaType || t("chat.image.alt")}
        className="max-h-72 max-w-full object-contain"
      />
    </a>
  );
}

function extractAssistantOutputText(
  blocks: TranscriptBlock[] = [],
  liveBlocks: TranscriptBlock[] = [],
  liveTail: string = "",
): string {
  const allBlocks = [...blocks, ...liveBlocks];
  let lastSectionStart = -1;
  for (let index = allBlocks.length - 1; index >= 0; index -= 1) {
    if (allBlocks[index].type !== "text") {
      lastSectionStart = index;
      break;
    }
  }
  const text = allBlocks
    .slice(lastSectionStart + 1)
    .map((block) => block.text ?? "")
    .join("");
  return text + liveTail;
}

// MessageBody 渲染定稿(非流式)文本消息:先 prepareMentionText 把 @mention XML
// 换成哨兵,再按需构建 decorator。抽成独立组件是因为调用点在 RenderItemView 的
// switch 分支里 —— 直接在 case 里调 useMemo 会违反 hooks 规则(只在 item.type
// === "text" 时执行,不满足每次渲染都调用同一组 hooks)。
function MessageBody({
  item,
  cwd,
  sessionId,
}: {
  item: Extract<TranscriptRowItem, { type: "text" }>;
  cwd?: string;
  sessionId?: number;
}) {
  const { text: mentionText, refs: mentionRefs } = React.useMemo(
    () => prepareMentionText(item.text),
    [item.text],
  );
  const mentionDecorator = React.useMemo(
    () =>
      mentionRefs.length > 0 ? makeMentionDecorator(mentionRefs) : undefined,
    [mentionRefs],
  );
  return (
    <MarkdownText
      cwd={cwd}
      sessionId={sessionId}
      text={mentionText}
      decorator={mentionDecorator}
    />
  );
}

// agentruntime.UnsupportedRequestPurpose 的封闭词表(与 chat.notice.hermesUnsupportedRequest.purpose
// 下的文案一一对应)。发送方比本端新、或没带用途时落到 unknown,绝不拼出查不到的 key。
const UNSUPPORTED_REQUEST_PURPOSES = new Set([
  "sudo_password",
  "secret",
  "vault_unlock",
  "vault_save_login",
  "vault_code",
  "terminal_read",
  "preview_read",
  "window_read",
  "preview_act",
  "tour",
]);

function unsupportedRequestPurposeKey(purpose: string | undefined): string {
  return purpose && UNSUPPORTED_REQUEST_PURPOSES.has(purpose)
    ? purpose
    : "unknown";
}

// ─── RenderItem → JSX ────────────────────────────────────────────────────────

// 停靠档下待决的工具权限请求由宿主的 ToolPermissionDock 画，转录这一项留空。
function isDockedDecision(
  item: TranscriptRowItem,
  ctx: TranscriptRenderContextValue | null,
): boolean {
  return (
    ctx?.decisionPlacement === "docked" &&
    item.type === "tool_permission_request" &&
    isPendingToolPermission(item.block)
  );
}

// messageId 是本行所属的 assistant 消息 —— 审批 / 权限卡做乐观更新时要用它定位
// chat-streams-store 里对应的那条流(一个会话可同时有多条流,只给 sessionId 定位不到)。
type RenderItemViewProps = {
  item: TranscriptRowItem;
  messageId: number;
  /** 本行是「这一轮在跑」的末行 —— 尾部的活动块此刻正在跑。 */
  running: boolean;
  /** 消息级耗时(块级无来源),活动块组头的耗时槽用;运行中不显示。 */
  durationMs?: number;
};

function RenderItemView(props: RenderItemViewProps) {
  const ctx = React.useContext(TranscriptRenderContext);
  if (isDockedDecision(props.item, ctx)) return null;
  return (
    <ShortcutHintsProvider hidden={ctx?.hideShortcutHints}>
      <RenderItemContent {...props} />
    </ShortcutHintsProvider>
  );
}

function RenderItemContent({
  item,
  messageId,
  running,
  durationMs,
}: RenderItemViewProps) {
  const { t } = useUiTranslation();
  const ctx = React.useContext(TranscriptRenderContext);
  switch (item.type) {
    case "placeholder":
      return null;
    case "text":
      return item.streaming ? (
        <StreamingMarkdown
          cwd={ctx?.cwd}
          sessionId={ctx?.sessionId}
          text={item.text}
        />
      ) : (
        <MessageBody item={item} cwd={ctx?.cwd} sessionId={ctx?.sessionId} />
      );
    case "plan":
      return (
        <PlanApproveCard
          cwd={ctx?.cwd}
          sessionId={ctx?.sessionId ?? 0}
          toolBlock={item.block}
          onPlanActionStarted={ctx?.onPlanActionStarted}
          uiStateKey={item.uiStateKey}
          tabStateKey={ctx?.tabStateKey}
        />
      );
    case "thinking":
      return (
        <ThinkingBlock
          startedAt={item.startedAt}
          streaming={item.streaming}
          text={item.block.text ?? ""}
          uiStateKey={item.uiStateKey}
        />
      );
    case "activity":
      // 活动块:连续的思考 / 只读探查 / 中性 / 写 / 命令 / 失败折成一行组头。
      // 一个块仍然只占一个虚拟行 —— 折叠态不 mount 组内步骤(Hard invariant 9)。
      // running 认「这一轮在跑的那条消息的末行」:此刻没有别的东西排在它后面,
      // 说明 agent 正在这一组里干活。轮次落定 → running 变假 → 自动收起。
      // item.growing 是唯一的例外:后面那行是一段**还在流的思考**,它一落定就并
      // 回这个块 —— 这一组并没有结束,不能因为暂时不是末行就收起来(行模型的
      // markGrowingActivity 负责标)。
      return (
        <ActivityBlock
          steps={item.steps}
          summary={item.summary}
          uiStateKey={item.uiStateKey}
          cwd={ctx?.cwd}
          durationMs={durationMs}
          running={running || item.growing === true}
        />
      );
    case "image":
      return <ImageBlockView block={item.block} />;
    case "tool":
      // 工具卡统一走 CanonicalToolRouter:
      //   - block.canonical 非空且 kind 已注册 → 分发到 canonical-tool/<kind>/card.tsx
      //     (file.write / file.edit / agent.spawn ... )
      //   - tool_use 形态的 plan.update 刻意不注册,这里仍显示普通工具卡。
      //     type="plan" 且带 actions 的 plan.update 已在上方复用 PlanCard。
      //   - 否则 fallback 到 RawToolCard(Bash/Read/MCP 等通用工具)。
      // 审批信息不单独透传:RawToolCard 自己从 toolBlock.toolPermission 读。
      return (
        <CanonicalToolRouter
          cwd={ctx?.cwd}
          sessionId={ctx?.sessionId ?? 0}
          messageId={messageId}
          resultBlock={item.resultBlock}
          toolBlock={item.toolBlock ?? { type: "tool_use" }}
          childBlocks={item.childBlocks}
          onPlanActionStarted={ctx?.onPlanActionStarted}
          onStopSubagent={ctx?.onStopSubagent}
          uiStateKey={item.uiStateKey}
          tabStateKey={ctx?.tabStateKey}
        />
      );
    case "tool_permission_request":
      // 两种 canonical(plan.approve_request / tool.permission)由后端在
      // ExitPlanMode 与其它工具之间分流。这里统一走 CanonicalToolRouter。
      return (
        <CanonicalToolRouter
          cwd={ctx?.cwd}
          sessionId={ctx?.sessionId ?? 0}
          messageId={messageId}
          toolBlock={item.block}
          onPlanActionStarted={ctx?.onPlanActionStarted}
          uiStateKey={item.uiStateKey}
          tabStateKey={ctx?.tabStateKey}
        />
      );
    case "tool_approval":
      // 内置写工具审批卡:不走 CanonicalToolRouter,按 block.type 直接路由。
      // approval 从 block.toolApproval 取,sessionId 从渲染上下文取(同 tool 卡)。
      return item.block.toolApproval ? (
        <ToolApprovalCard
          approval={item.block.toolApproval}
          sessionId={ctx?.sessionId ?? 0}
        />
      ) : null;
    case "exec_approval":
      return item.block.execApproval ? (
        <OpenClawExecApprovalCard
          approval={item.block.execApproval}
          sessionId={ctx?.sessionId ?? 0}
        />
      ) : null;
    case "compact_boundary": {
      const trig = item.block.compact?.trigger;
      const trigger: "auto" | "manual" | undefined =
        trig === "auto" || trig === "manual" ? trig : undefined;
      return (
        <CompactBoundaryDivider
          preTokens={item.block.compact?.preTokens}
          trigger={trigger}
          at={item.block.compact?.at ?? 0}
        />
      );
    }
    case "notice": {
      // notice 块:供应商回退/切换 notice 由后端投影成结构化 providerKey + providerName
      // + noticeKind (providerNoticePayload),文案走 t();其它来源/旧数据的非结构化
      // notice 原样渲染 Text。noticeKind==="switch" 是用户主动切换（决策 9），其余
      // （含旧数据的空 noticeKind）沿用既有的供应商回退提示——两者共用 providerKey /
      // providerName 字段，必须靠 noticeKind 而不是「providerKey 是否非空」区分，
      // 因为切换回「跟随 agent 绑定」时 providerKey 本就是空串。
      // providerName 非空就优先显示它（2026-08-10 显示缺陷修复决策 1/2）,为空
      // （供应商已删,后端查不到实体）则回退到裸 providerKey,文案本身不变。
      // fixed-model 切换时负载还带 modelKey/modelName（spec 2026-08-11 决策 1），
      // transcript 要能读出「固定到哪个模型」而不是只剩供应商名。
      // noticeKind==="reasoning_effort" 是会话级思考力度切换（spec 2026-09-01 决策 7）：
      // 同一条通道、同一份负载，只是换字段——档位在 reasoningEffort 上，空串表示
      // 改回跟随后端配置。它同样没有 Text（后端对结构化 notice 一律置空），所以
      // 少了这一支就只剩一个空灰框。
      // noticeKind==="hermes_unsupported_request" 是 Hermes 反向请求提示（spec
      // 2026-09-17）：后端只给一个只读用途分类 noticePurpose（从不是协议方法名或
      // 原始参数），这里两段 t() 拼出「Hermes 请求了〈用途〉，Agentre 暂不支持，
      // 已跳过」——用途文案与句子模板分开翻译，purpose 不是自由文本。
      const providerKey = item.block.providerKey;
      const providerLabel = item.block.providerName || providerKey;
      const modelKey = item.block.modelKey;
      const modelLabel = item.block.modelName || modelKey;
      const noticeKind = item.block.noticeKind;
      const content =
        noticeKind === "cli_model"
          ? item.block.cliModelMode === "fixed"
            ? t("modelTargetPicker.cli.switchedFixed", {
                model: item.block.cliModel,
              })
            : t(
                item.block.cliModelMode === "cli-default"
                  ? "modelTargetPicker.cli.switchedDefault"
                  : "modelTargetPicker.cli.switchedAgent",
              )
          : noticeKind === "cli_model_deviation"
            ? t("modelTargetPicker.cli.deviation", {
                selected: item.block.cliModel,
                actual: item.block.actualModel,
              })
            : noticeKind === "hermes_unsupported_request"
              ? t("chat.notice.hermesUnsupportedRequest.sentence", {
                  purpose: t(
                    `chat.notice.hermesUnsupportedRequest.purpose.${unsupportedRequestPurposeKey(item.block.noticePurpose)}`,
                  ),
                })
              : noticeKind === "reasoning_effort"
                ? item.block.reasoningEffort
                  ? t("chat.notice.reasoningEffortSwitch.sentence", {
                      level: item.block.reasoningEffort,
                    })
                  : t("chat.notice.reasoningEffortSwitch.followBackend")
                : noticeKind === "switch"
                  ? providerKey
                    ? modelKey
                      ? t("chat.notice.providerSwitch.fixedModel", {
                          provider: providerLabel,
                          model: modelLabel,
                        })
                      : t("chat.notice.providerSwitch.sentence", {
                          provider: providerLabel,
                        })
                    : t("chat.notice.providerSwitch.followAgentBinding")
                  : providerKey
                    ? t("chat.notice.providerFallback.sentence", {
                        provider: providerLabel,
                      })
                    : (item.block.text ?? "");
      return (
        <section
          role="status"
          data-testid="transcript-notice"
          className="flex w-full max-w-measure items-start gap-2 rounded-lg bg-field-fill px-3 py-2 text-aux text-muted-foreground"
        >
          <span className="min-w-0 flex-1 break-words">{content}</span>
        </section>
      );
    }
    case "unknown":
      return (
        <div className="rounded-lg bg-field-fill px-3 py-2 font-mono text-aux text-muted-foreground">
          {t("chat.debug.unknownBlock", { type: item.block.type })}
        </div>
      );
    default:
      return null;
  }
}

// LocalCommandRowView 单独成组件,只为了让 attachTerminal 端口的 hook 落在
// 「真的渲染了一张本地命令卡」的那一次上 —— 端口取用口会在缺 Provider 时如实抛错,
// 放到 TranscriptRowView 顶部就等于让每一行都要求宿主接好这个纯桌面能力。
function LocalCommandRowView({
  entry,
  onStop,
}: {
  entry: TranscriptLocalCommand;
  onStop?: (terminalId: string) => void | Promise<void>;
}) {
  const attachTerminal = useOptionalPort("attachTerminal");
  return (
    <LocalCommandCard
      entryId={entry.id}
      onOpenInTerminal={(id) =>
        attachTerminal?.({ terminalId: id, command: entry.command })
      }
      onStop={onStop}
    />
  );
}

// ─── 行渲染 ──────────────────────────────────────────────────────────────────

export type TranscriptRowViewProps = {
  row: TranscriptRow;
  /** 仅 live 消息的末行收到非空值 —— footer copyText 需要拼上未持久化的输出。 */
  liveTail: string;
  liveBlocks: TranscriptBlock[] | undefined;
  /** 仅 live 消息的末行非空。 */
  liveRetry: RetryNotice | null;
  /** 末行 && 该消息是 streaming 指示器宿主(最后一条 assistant)。 */
  showIndicator: boolean;
  /** showIndicator && compacting → 渲染 CompactingIndicator 替代 TypingIndicator。*/
  compacting: boolean;
  /** showIndicator && reconnecting → 渲染 DisconnectedIndicator。通道断了就先说通道:
   *  压缩与否此刻都观察不到,断连形态优先于压缩形态。*/
  reconnecting: boolean;
  /** 流式中的本轮统计。非 live 行传 null，避免 memo 失配。 */
  liveTurn?: LiveTurnInput | null;
  /**
   * 本行所属的这一轮**此刻在不在跑** —— 由宿主如实告知,活动块的运行态(自动展开 +
   * 超阈值省略中段)据此判定。
   *
   * 与上面那族 live* 入参不同:它们说的是「还没落库的那一段内容是什么」,这一条说
   * 的是「这一轮的生命周期状态」。两者过去被当成一回事(从 `liveBlocks !== undefined`
   * 反推运行中),那是**泄漏的判据** —— 只有「有未落库内容」这条分界的宿主才恒真。
   * agentre-server 控制台是中继事件流,到一条画一条,没有这条分界,于是运行态在
   * 它上面一次都没生效过;它手上真正有的是 daemon 的会话生命周期。
   *
   * 缺省(不传)= 宿主还没接这个信号,退回旧判据,消费方 pin 新版本前行为不变。
   * memo 纪律同 live* 一族:它是标量,非运行中的行一律传 `false`(或不传),
   * 不要让它随每个 chunk 变化。
   */
  turnRunning?: boolean;
  /** 占位消息 model 为空时，用会话当前模型。 */
  fallbackModel?: string;
};

// 行级 memo 是流式期间的重渲边界:persisted 消息的行对象来自 WeakMap 缓存(引用
// 稳定),live 标量 props 对非 live 行恒为空值 → 每个 chunk 只有 live 消息的行重渲。
export const TranscriptRowView = React.memo(function TranscriptRowView({
  row,
  liveTail,
  liveBlocks,
  liveRetry,
  showIndicator,
  compacting,
  reconnecting,
  liveTurn = null,
  turnRunning,
  fallbackModel = "",
}: TranscriptRowViewProps) {
  const ctx = React.useContext(TranscriptRenderContext);
  // local_command 行无 message 引用,独立成卡(无头像/footer chrome)在此提前返回。
  if (row.item.type === "local_command") {
    return (
      <LocalCommandRowView
        entry={row.item.entry}
        onStop={ctx?.onStopLocalCommand}
      />
    );
  }
  const m = row.message;
  // 类型收窄:只有 local_command 行缺 message(已在上方返回),其余行恒有 message。
  if (!m) return null;
  // 停靠走的待决审批若是消息中间的分片行,这一行没有任何别的内容 —— 整行不画。
  if (
    !row.isFirstOfMessage &&
    !row.isLastOfMessage &&
    isDockedDecision(row.item, ctx)
  ) {
    return null;
  }
  const isAssistant = m.role === "assistant";
  // 每条 assistant 都允许重新生成；只读模式下 ctx.onRerun 为 undefined 时不渲染按钮。
  const rerunHandler =
    isAssistant && ctx?.onRerun ? () => ctx.onRerun!(m.id) : undefined;
  const continueHandler =
    isAssistant && ctx?.onContinue ? () => ctx.onContinue!(m.id) : undefined;
  const retryTurnHandler =
    isAssistant && ctx?.onRetryTurn ? () => ctx.onRetryTurn!(m.id) : undefined;
  const copyText =
    isAssistant && row.isLastOfMessage
      ? extractAssistantOutputText(m.blocks ?? [], liveBlocks ?? [], liveTail)
      : "";
  // 「这一轮此刻在跑」的末行 —— 尾部的活动块据此自动展开,落定后自动收起。
  //
  // 「在不在跑」归宿主(turnRunning),「是不是这一轮的末行」归行模型 ——
  // 只有这两件事都成立,尾部那个活动块才是 agent 此刻正在干活的那一组。
  //
  // 宿主没接 turnRunning 时退回旧判据「宿主给这一行喂了 live* 内容」。它只是
  // 运行中的一个代理量,对「有未落库内容」这条分界的宿主(桌面端)恒等价,
  // 对没有这条分界的宿主(agentre-server 控制台)恒为假 —— 留着只为兼容。
  const isTurnRunningRow =
    row.isLastOfMessage &&
    (turnRunning ?? (liveBlocks !== undefined || liveTail.length > 0));

  const footerModel = m.model || liveTurn?.model || fallbackModel;
  const showsAssistantFooter =
    row.isLastOfMessage &&
    isAssistant &&
    (m.durationMs > 0 || copyText !== "" || liveTurn != null);
  const footerHasRerun =
    showsAssistantFooter &&
    rerunHandler !== undefined &&
    showsMessageMeta({ ...m, liveTurn, model: footerModel });

  const tailAttachments = row.isLastOfMessage ? (
    <>
      {liveRetry ? (
        <RetryIndicator retry={liveRetry} />
      ) : showIndicator ? (
        reconnecting ? (
          <DisconnectedIndicator />
        ) : compacting ? (
          <CompactingIndicator />
        ) : (
          <TypingIndicator />
        )
      ) : null}
      {isAssistant && m.errorText ? (
        <ErrorCard
          text={m.errorText}
          backendType={ctx?.backendType}
          footerHasRerun={footerHasRerun}
          onContinue={continueHandler}
          onRerun={rerunHandler}
          onRetryTurn={retryTurnHandler}
        />
      ) : null}
    </>
  ) : null;

  const meta = !row.isLastOfMessage ? undefined : showsAssistantFooter ? (
    <AssistantMessageActions
      model={footerModel}
      promptTokens={m.promptTokens}
      completionTokens={m.completionTokens}
      cachedTokens={m.cachedTokens}
      cacheCreationTokens={m.cacheCreationTokens}
      reasoningTokens={m.reasoningTokens}
      durationMs={m.durationMs}
      firstTokenMs={m.firstTokenMs}
      tokensPerSec={m.tokensPerSec}
      liveTurn={liveTurn}
      onRerun={rerunHandler}
      copyText={copyText}
      density={ctx?.metaDensity}
    />
  ) : !isAssistant && ctx?.onEdit ? (
    <UserMessageActions onEdit={() => ctx.onEdit!(m.id)} />
  ) : undefined;

  const alignEnd = !isAssistant && ctx?.userMessageAlign === "end";

  if (row.isFirstOfMessage) {
    return (
      <>
        {row.autonomous ? (
          <AutoTriggerBanner trigger={row.message?.turnTrigger} />
        ) : null}
        <ChatMessage
          author={isAssistant ? (ctx?.agentName ?? "") : ""}
          avatar={ctx?.agentAvatar}
          variant={isAssistant ? "assistant" : "user"}
          align={alignEnd ? "end" : undefined}
          time={formatHHmm(m.createtime)}
          source={isAssistant ? undefined : row.sourceDevice}
          meta={meta}
        >
          <RenderItemView
            item={row.item}
            messageId={row.messageId}
            running={isTurnRunningRow}
            durationMs={m.durationMs}
          />
          {tailAttachments}
        </ChatMessage>
      </>
    );
  }

  // 消息的后续分片行:左侧 w-7 幽灵 gutter 对齐头像列,内容列与 MessageRow
  // 完全同构(max-w-measure / data-selectable-text / footer 槽样式)。
  return (
    <div
      data-align={alignEnd ? "end" : undefined}
      className={cn("flex gap-3 text-sm", alignEnd && "flex-row-reverse")}
    >
      <div aria-hidden className="w-7 shrink-0" />
      <div
        className={cn(
          "flex min-w-0 max-w-measure flex-1 flex-col gap-1",
          alignEnd && "items-end",
        )}
      >
        <div data-selectable-text="true" className="flex flex-col gap-2">
          <RenderItemView
            item={row.item}
            messageId={row.messageId}
            running={isTurnRunningRow}
            durationMs={m.durationMs}
          />
          {tailAttachments}
        </div>
        {meta ? (
          <div className="mt-1 flex flex-wrap items-center gap-2 font-mono text-meta text-muted-foreground">
            {meta}
          </div>
        ) : null}
      </div>
    </div>
  );
});

export { ChatMessage, ErrorCard, MessageMeta };
