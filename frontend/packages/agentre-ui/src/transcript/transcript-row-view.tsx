// transcript-row-view: 行级虚拟化的渲染层 —— 一个 TranscriptRow 渲染成一个虚拟行。
// 消息 chrome 按分片标志挂载:首行走 ChatMessage(article + 头像 + 名字 + 时间戳,
// 纯文本消息恰好一行,DOM 与 message 级虚拟化完全一致);后续行用幽灵 gutter 对齐
// 头像列;末行追加 footer(meta/copy/edit)+ RetryNotice + TypingIndicator + ErrorCard。
// 静态依赖走 TranscriptRenderContext,行组件的 memo 比较只剩 row 引用 + 少量 live 标量。
import * as React from "react";
import {
  ArrowDown,
  ArrowUp,
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
import { formatTokensPerSec, formatTurnDuration } from "../lib/format-duration";
import { cn } from "../lib/utils";
import { Button } from "../ui/button";
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
import { CompactBoundaryDivider } from "./compact-boundary-divider";
import type {
  RetryNotice,
  TranscriptBlock,
  TranscriptLocalCommand,
} from "./dto";
import { LocalCommandCard } from "./local-command/card";
import { MarkdownText, StreamingMarkdown } from "./markdown-text";
import { MessageCopyButton, MessageRow } from "./message-row";
import { OpenClawExecApprovalCard } from "./openclaw-exec-approval/card";
import { useOptionalPort } from "./ports-context";
import { ThinkingBlock } from "./thinking-block";
import { ToolApprovalCard } from "./tool-approval/card";
import type { TranscriptRow, TranscriptRowItem } from "./transcript-rows";
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
  onStopSubagent?: (toolUseId: string) => void;
  /** 停掉本地命令；ChatPanel 是生命周期 owner，只读模式不传。 */
  onStopLocalCommand?: (terminalId: string) => void | Promise<void>;
  /** 只读模式下不传；有值时才渲染「重新生成」按钮。 */
  onRerun?: (messageId: number) => void;
  /** 只读模式下不传；有值时错误卡渲染「继续」按钮。 */
  onContinue?: (messageId: number) => void;
  /** 只读模式下不传；有值时才渲染「编辑」按钮。 */
  onEdit?: (messageId: number) => void;
};

export const TranscriptRenderContext =
  React.createContext<TranscriptRenderContextValue | null>(null);

// ─── 消息 chrome 组件(自 chat.tsx 平移)──────────────────────────────────────

function formatHHmm(ms: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

function formatHHmmss(ms: number): string {
  if (!ms) return "";
  const d = new Date(ms);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}:${String(d.getSeconds()).padStart(2, "0")}`;
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
  /** "assistant" (默认): 渲染 agent avatar + 名字。
   *  "user": 渲染中性的「我」头像 —— 与 agent 头像视觉对称，但走 muted
   *  色阶不抢焦点，把主视觉留给 agent。 */
  variant?: "user" | "assistant";
};

function ChatMessage({
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

type MessageMetaProps = {
  cacheCreationTokens?: number;
  cachedTokens?: number;
  completionTokens: number;
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

  // tooltip 里需要拆分展示，所以这里给一个稳定的 row 渲染器避免重复。
  const rows: { label: string; value: string }[] = [
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

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {/* 宿主(SaaS 前端等)不保证树上有 TooltipProvider,radix-ui 1.4 的 Root
          没有祖先 Provider 会在 render 阶段直接抛错,所以这里自带一份。
          delayDuration 取 200ms,与桌面端 chat.tsx 外层 Provider 一致。 */}
      <TooltipProvider delayDuration={200}>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              className="inline-flex items-center gap-1.5 rounded px-1 py-0.5 hover:bg-muted/60"
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
      {onRerun ? (
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
      ) : null}
    </div>
  );
}

type AssistantMessageActionsProps = {
  cacheCreationTokens?: number;
  cachedTokens?: number;
  completionTokens: number;
  copyText: string;
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
  const hasUsage =
    promptTokens > 0 ||
    completionTokens > 0 ||
    (cachedTokens ?? 0) > 0 ||
    (cacheCreationTokens ?? 0) > 0 ||
    (reasoningTokens ?? 0) > 0;

  return (
    <>
      {durationMs > 0 || liveTurn != null || hasUsage || model ? (
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
        />
      ) : null}
      <MessageCopyButton
        text={copyText}
        label={t("common.copy")}
        ariaLabel={t("chat.actions.copyOutput")}
        successTitle={t("chat.actions.copyOutputDone")}
        errorTitle={t("chat.actions.copyOutputFailed")}
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

function ErrorCard({
  text,
  onContinue,
  onRerun,
}: {
  text: string;
  onContinue?: () => void;
  onRerun?: () => void;
}) {
  const { t } = useUiTranslation();
  return (
    <section
      data-selectable-text="true"
      className="flex w-full max-w-measure items-center gap-3 rounded-lg border border-status-error/40 bg-destructive-soft px-4 py-2.5"
    >
      <TriangleAlert
        className="size-4 shrink-0 text-status-error"
        aria-hidden="true"
      />
      <span className="min-w-0 flex-1 text-aux text-status-error">
        {t("chat.errorCard.message", { text })}
      </span>
      <div className="flex shrink-0 items-center gap-2">
        {onContinue ? (
          <Button
            type="button"
            size="xs"
            variant="outline"
            onClick={onContinue}
          >
            {t("chat.errorCard.continue")}
          </Button>
        ) : null}
        {onRerun ? (
          <Button type="button" size="xs" variant="outline" onClick={onRerun}>
            {t("chat.errorCard.regenerate")}
          </Button>
        ) : null}
      </div>
    </section>
  );
}

function RetryNoticeCard({ retry }: { retry: RetryNotice }) {
  const { t } = useUiTranslation();
  const hasCount = retry.attempt > 0 && retry.maxAttempts > 0;
  const title = hasCount
    ? t("chat.retry.titleWithMax", {
        attempt: retry.attempt,
        max: retry.maxAttempts,
      })
    : retry.attempt > 0
      ? t("chat.retry.titleWithAttempt", { attempt: retry.attempt })
      : t("chat.retry.title");
  const message = retry.message || t("chat.retry.defaultMessage");
  const at = formatHHmmss(retry.at);

  return (
    <section
      data-selectable-text="true"
      role="status"
      aria-label={t("chat.retry.aria")}
      className="flex w-full max-w-measure items-start gap-3 rounded-lg border border-status-warning/45 bg-status-warning/10 px-4 py-2.5"
    >
      <RefreshCw
        className="mt-0.5 size-4 shrink-0 animate-spin text-status-warning motion-reduce:animate-none"
        aria-hidden="true"
      />
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-aux font-semibold text-status-warning">
            {title}
          </span>
          {at ? (
            <span className="font-mono text-meta text-muted-foreground">
              {at}
            </span>
          ) : null}
        </div>
        <div className="mt-1 min-w-0 break-words font-mono text-meta text-foreground">
          {message}
        </div>
        {retry.details ? (
          <div className="mt-1 min-w-0 break-words text-meta leading-snug text-muted-foreground">
            {retry.details}
          </div>
        ) : null}
      </div>
    </section>
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
      className="block w-fit overflow-hidden rounded-lg border border-border bg-muted"
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

// ─── RenderItem → JSX ────────────────────────────────────────────────────────

// messageId 是本行所属的 assistant 消息 —— 审批 / 权限卡做乐观更新时要用它定位
// chat-streams-store 里对应的那条流(一个会话可同时有多条流,只给 sessionId 定位不到)。
function RenderItemView({
  item,
  messageId,
  live,
  durationMs,
}: {
  item: TranscriptRowItem;
  messageId: number;
  /** 本行是仍在流式的那条消息的末行 —— 尾部的活动块此刻正在跑。 */
  live: boolean;
  /** 消息级耗时(块级无来源),活动块组头的耗时槽用;运行中不显示。 */
  durationMs?: number;
}) {
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
      // running 认「仍在流式的消息的末行」:此刻没有别的东西排在它后面,说明
      // agent 正在这一组里干活。轮次落定 → live 变假 → 自动收起。
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
          running={live || item.growing === true}
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
      const providerKey = item.block.providerKey;
      const providerLabel = item.block.providerName || providerKey;
      const modelKey = item.block.modelKey;
      const modelLabel = item.block.modelName || modelKey;
      const noticeKind = item.block.noticeKind;
      const content =
        noticeKind === "switch"
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
          className="flex w-full max-w-measure items-start gap-2 rounded-lg border border-border bg-muted/40 px-3 py-2 text-aux text-muted-foreground"
        >
          <span className="min-w-0 flex-1 break-words">{content}</span>
        </section>
      );
    }
    case "unknown":
      return (
        <div className="rounded-lg border border-dashed border-border px-3 py-2 font-mono text-aux text-muted-foreground">
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
  const isAssistant = m.role === "assistant";
  // 每条 assistant 都允许重新生成；只读模式下 ctx.onRerun 为 undefined 时不渲染按钮。
  const rerunHandler =
    isAssistant && ctx?.onRerun ? () => ctx.onRerun!(m.id) : undefined;
  const continueHandler =
    isAssistant && ctx?.onContinue ? () => ctx.onContinue!(m.id) : undefined;
  const copyText =
    isAssistant && row.isLastOfMessage
      ? extractAssistantOutputText(m.blocks ?? [], liveBlocks ?? [], liveTail)
      : "";
  // live 行 = 仍在流式的那条消息的末行(chat.tsx 只给这一行喂 live* 内容)。
  // 尾部的活动块据此判「此刻正在跑」:自动展开 + 实时尾巴,落定后自动收起。
  const isLiveTailRow =
    row.isLastOfMessage && (liveBlocks !== undefined || liveTail.length > 0);

  const tailAttachments = row.isLastOfMessage ? (
    <>
      {liveRetry ? <RetryNoticeCard retry={liveRetry} /> : null}
      {showIndicator ? (
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
          onContinue={continueHandler}
          onRerun={rerunHandler}
        />
      ) : null}
    </>
  ) : null;

  const meta = !row.isLastOfMessage ? undefined : isAssistant &&
    (m.durationMs > 0 || copyText || liveTurn != null) ? (
    <AssistantMessageActions
      model={m.model || liveTurn?.model || fallbackModel}
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
    />
  ) : !isAssistant && ctx?.onEdit ? (
    <UserMessageActions onEdit={() => ctx.onEdit!(m.id)} />
  ) : undefined;

  if (row.isFirstOfMessage) {
    return (
      <>
        {row.autonomous ? <AutoTriggerBanner /> : null}
        <ChatMessage
          author={isAssistant ? (ctx?.agentName ?? "") : ""}
          avatar={ctx?.agentAvatar}
          variant={isAssistant ? "assistant" : "user"}
          time={formatHHmm(m.createtime)}
          source={isAssistant ? undefined : row.sourceDevice}
          meta={meta}
        >
          <RenderItemView
            item={row.item}
            messageId={row.messageId}
            live={isLiveTailRow}
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
    <div className="flex gap-3 text-sm">
      <div aria-hidden className="w-7 shrink-0" />
      <div className="flex min-w-0 max-w-measure flex-1 flex-col gap-1">
        <div data-selectable-text="true" className="flex flex-col gap-2">
          <RenderItemView
            item={row.item}
            messageId={row.messageId}
            live={isLiveTailRow}
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
