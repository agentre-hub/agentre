import * as React from "react";
import type { TFunction } from "i18next";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useShallow } from "zustand/react/shallow";
import {
  Check,
  LoaderCircle,
  Timer,
  TriangleAlert,
  Wrench,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  Button,
  ChatComposer as SharedChatComposer,
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
  TooltipProvider,
  autonomousTurnMessageIds,
  buildMentionSources,
  transcriptRowPadClass,
  type ChatComposerDropZone,
  type ChatComposerHandle,
  type ChatComposerProps as SharedChatComposerProps,
  type SlashCommand,
  type SlashExec,
  type UsageLevel,
  usageLevel,
} from "@agentre-hub/agentre-ui";
import { cn } from "@/lib/utils";

import {
  applyLiveTranscriptRows,
  buildSettledTranscriptRows,
  buildSourceByMessageId,
  CodeBlock,
  estimateRowSizeWithSpacing,
  indicatorHostMessageId,
  TranscriptCard,
  TranscriptUIStateProvider,
  type LiveRowContent,
  type LiveTurnInput,
  type PlanActionStream,
  type TranscriptRow,
} from "@agentre-hub/agentre-ui";
import { CompactHistoryFold } from "./compact-history-fold";
import { useTranscriptCallbacks } from "./use-transcript-callbacks";
import { EarlierMessagesLoader } from "./earlier-messages-loader";
import {
  ChatMessage,
  ErrorCard,
  MessageMeta,
  MESSAGE_AVATAR_CLASS,
  TranscriptRenderContext,
  TranscriptRowView,
  type TranscriptRenderContextValue,
} from "@agentre-hub/agentre-ui";
import { AgentAvatar } from "./primitives";
import type { AgentColor, AgentStatus } from "./types";
import { statusConfig } from "./types";
import type { RetryNotice } from "@/stores/chat-streams-store";
import { useLocalCommandsStore } from "@/stores/local-commands-store";
import { useChatAgents } from "@/hooks/use-chat-agents";
import { useProjectList } from "@/hooks/use-project-list";
import {
  ChatReadDroppedImages,
  RemoteDeviceFingerprint,
} from "../../../wailsjs/go/app/App";
import { chat_svc } from "../../../wailsjs/go/models";
import { registerDropZone } from "@/lib/file-drop";
import { listAvailable, useAgentSkillCommands } from "./slash-commands";

type ToolCallProps = React.ComponentProps<"div"> & {
  path?: string;
  status?: AgentStatus;
  statusLabel: string;
  toolName: string;
};

function ToolCall({
  className,
  path,
  status = "running",
  statusLabel,
  toolName,
  ...props
}: ToolCallProps) {
  const config = statusConfig[status];
  const StatusIcon = status === "waiting" ? LoaderCircle : Check;

  return (
    <TranscriptCard
      data-selectable-text="true"
      className={cn("flex flex-col gap-1.5 px-3 py-2.5", className)}
      {...props}
    >
      <div className="flex min-w-0 items-center gap-1.5 font-mono text-aux">
        <Wrench className="size-3.5 shrink-0 text-primary-text" />
        <span className="font-semibold text-primary-text">{toolName}</span>
        {path ? (
          <>
            <span className="text-muted-foreground">·</span>
            <span className="min-w-0 truncate text-muted-foreground">
              {path}
            </span>
          </>
        ) : null}
      </div>
      <div className="flex items-center gap-1.5 font-mono text-meta">
        <StatusIcon className={cn("size-3", config.textClassName)} />
        <span className={status === "running" ? config.textClassName : ""}>
          {statusLabel}
        </span>
      </div>
    </TranscriptCard>
  );
}

type ApprovalGateProps = React.ComponentProps<"section"> & {
  description: string;
  onApprove?: () => void;
  onReject?: () => void;
  title: string;
};

function ApprovalGate({
  className,
  description,
  onApprove,
  onReject,
  title,
  ...props
}: ApprovalGateProps) {
  const { t } = useTranslation();
  return (
    <TranscriptCard
      className={cn(
        "flex items-center gap-3 border-status-waiting bg-status-waiting-bg px-4 py-3",
        className,
      )}
      {...props}
    >
      <TriangleAlert
        className="size-5 shrink-0 text-status-waiting"
        aria-hidden="true"
      />
      <div className="min-w-0 flex-1">
        <div className="text-aux font-semibold text-status-waiting">
          {title}
        </div>
        <div className="mt-0.5 text-aux leading-snug">{description}</div>
      </div>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="h-8"
        onClick={onReject}
      >
        {t("common.reject")}
      </Button>
      <Button
        type="button"
        size="sm"
        className="h-8 bg-status-running text-status-running-foreground hover:bg-status-running/90"
        onClick={onApprove}
      >
        {t("chat.actions.approve")}
      </Button>
    </TranscriptCard>
  );
}

/**
 * 桌面端 composer 的**装配面**。渲染住在 `@agentre-hub/agentre-ui` 的 ChatComposer
 * 里，这里只负责把这一端独有的能力接上去：@ 提及的数据源、该 agent 生效的技能
 * 命令、以及 Wails 的原生拖入通道。
 *
 * 这三样都是宿主耦合，包不得知道：`useChatAgents` / `useProjectList` 读的是本机
 * 的清单，`ChatReadDroppedImages` 是 Wails 绑定。与 `agent-backends.tsx` /
 * `llm-providers.tsx` 是同一种装配根，不是第二份实现。
 */
type DesktopChatComposerProps = Omit<
  SharedChatComposerProps,
  "mentionSources" | "slashCommands" | "onSlashSelect" | "dropZone"
> & {
  /** 当前会话或新会话的 agent id，用于加载该 agent 最终生效的 skill 命令。 */
  agentId?: number;
  /** 当前会话 / 项目的工作目录，用于发现 project-scoped Skill。 */
  cwd?: string;
  /** slash menu 里 rpc 类命令的回调（literal_text 由包内部填回编辑器）。 */
  onSlashRpc?: (
    cmd: SlashCommand,
    exec: Extract<SlashExec, { kind: "rpc" }>,
  ) => void;
};

export type {
  ChatComposerHandle,
  ChatComposerSubmit,
  ChatImageAttachment,
} from "@agentre-hub/agentre-ui";

// 落盘路径 → 图片附件。这是 Wails 绑定，是包里 resolveDroppedPaths 的 readImages
// 端口在桌面端的实现；浏览器端拿不到绝对路径，所以那一端不注入这个通道。
const DESKTOP_DROP_ZONE: ChatComposerDropZone = {
  readImages: async (paths: string[]) => {
    const resp = await ChatReadDroppedImages(
      chat_svc.ReadDroppedImagesRequest.createFrom({ paths }),
    );
    return (resp.items ?? []).map((it) => ({
      dataUrl: it.dataUrl,
      kind: it.kind === "image" ? ("image" as const) : ("path" as const),
      mediaType: it.mediaType,
      name: it.name,
      path: it.path,
    }));
  },
  registerDropZone,
};

// formatResetIn 把"距离 ISO 时间点还有多久"渲染成紧凑的 XdYh / Xh / Xm 形式
// (e.g. "4d21h", "3h", "40m"),用于 QuotaMeter tooltip。
//   - 空串 / 无法解析的输入 → 空串(调用方自己决定是否显示括号)
//   - 已过期(diff<=0)→ "0m"
//   - <1h → "Nm"(向上取整,避免 30s 显示 0m)
//   - <24h → "Nh"(向下取整)
//   - >=24h → "XdYh"(Yh=0 时省略,写 "Xd")
// nowMs 可选(测试注入固定 now);省略走 Date.now()。
export function formatResetIn(value: unknown, nowMs?: number): string {
  if (value == null || value === "") return "";
  const target =
    value instanceof Date ? value.getTime() : Date.parse(String(value));
  if (Number.isNaN(target)) return "";
  const diffMs = target - (nowMs ?? Date.now());
  if (diffMs <= 0) return "0m";
  if (diffMs < 3_600_000) {
    return `${Math.max(1, Math.ceil(diffMs / 60_000))}m`;
  }
  const totalHours = Math.floor(diffMs / 3_600_000);
  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  if (days <= 0) return `${hours}h`;
  if (hours === 0) return `${days}d`;
  return `${days}d${hours}h`;
}

// 配色表共用 quotaLevel 定级。文字色分表是因为"正常"态各处诉求不同(底栏配额要退到
// 背景里,面板与上下文里这个数字是主角);填充色三处一致,故只有一张表。
// 分表而不是拿 class 字符串去比较判断。
const QUOTA_METER_TONE: Record<UsageLevel, string> = {
  ok: "text-muted-foreground",
  warn: "text-status-waiting",
  danger: "text-status-error",
};
const QUOTA_PANEL_TONE: Record<UsageLevel, string> = {
  ok: "text-foreground",
  warn: "text-status-waiting",
  danger: "text-status-error",
};
const LEVEL_FILL_TONE: Record<UsageLevel, string> = {
  ok: "bg-primary",
  warn: "bg-status-waiting",
  danger: "bg-status-error",
};

const QUOTA_HOVER_OPEN_DELAY_MS = 200;
const QUOTA_HOVER_CLOSE_DELAY_MS = 100;

// QuotaMeter 展示 Claude Code 订阅的 5h / 7d 配额。数据由 chat-panel 通过 useCCUsage
// 拉取并传入(per-device, 不在这里订阅 store, 保证 Composer 可被纯 props 测试)。
//
// 渲染策略(与 cc_usage_svc.UsageState.reason 对齐):
//   - undefined / 空 reason / "no_credentials" → 整块不渲染(API key 用户、未首探)
//   - "ok" / "rate_limited"+stale / "network"+stale → 5h X% · 7d Y%(stale 不可见标记,只在面板脚注里提示)
//   - "auth_expired" / "device_offline" / "network"无stale → 灰态占位 "5h —%"
//
// 详情(重置倒计时 / Sonnet / Opus 拆分 / 异常态)在 HoverCard 面板里,不再用原生
// title —— 原生 title 不可键盘触达、不可着色、多行渲染跨平台不一致。
export function QuotaMeter({
  data,
  deviceLabel,
}: {
  data?: import("../../../wailsjs/go/models").cc_usage_svc.UsageState;
  deviceLabel?: string;
}) {
  const { t } = useTranslation();
  if (!data || !data.reason) return null;
  if (data.reason === "no_credentials") return null;

  const showNumbers = data.data && (data.reason === "ok" || !!data.stale);
  const fiveH = data.data ? Math.round(data.data.fiveHourPercent) : null;
  const sevenD = data.data ? Math.round(data.data.weeklyPercent) : null;

  const offline =
    data.reason === "auth_expired" || data.reason === "device_offline";
  // 灰态占位没有可信数值,整块压成 subtle;有数值时两个窗口各自取色。
  const fiveTone = offline
    ? "text-muted-foreground"
    : QUOTA_METER_TONE[usageLevel(fiveH)];
  const sevenTone = offline
    ? "text-muted-foreground"
    : QUOTA_METER_TONE[usageLevel(sevenD)];

  return (
    <HoverCard
      openDelay={QUOTA_HOVER_OPEN_DELAY_MS}
      closeDelay={QUOTA_HOVER_CLOSE_DELAY_MS}
    >
      <HoverCardTrigger asChild>
        <button
          type="button"
          className={cn(
            // min-w-0 + 截断:计量器是底栏唯一的让位者(规格决策 11)。断点估窄时
            // 多出来的宽度只吃掉这里,绝不把发送按钮顶出可视区。
            "flex min-w-0 cursor-default items-center gap-1.5 overflow-hidden rounded-sm border border-transparent px-1 py-0.5 whitespace-nowrap",
            "font-mono text-meta tabular-nums transition-colors motion-reduce:transition-none",
            "hover:border-border hover:bg-accent",
            "focus-visible:border-border focus-visible:bg-accent focus-visible:outline-none",
            offline ? "text-muted-foreground" : "text-muted-foreground",
          )}
          aria-label={t("chat.quota.aria", {
            device: deviceLabel || "local",
            five: fiveH ?? "—",
            seven: sevenD ?? "—",
          })}
        >
          <Timer className="size-2.5 shrink-0" aria-hidden="true" />
          <span className={fiveTone}>
            {/* 窄档隐藏 5h/7d 前缀,只留两个百分比;语义由 aria-label 与面板保留。 */}
            <span
              data-quota-prefix="5h"
              className="@max-[800px]/composer:hidden"
            >
              5h{" "}
            </span>
            {showNumbers && fiveH !== null ? `${fiveH}%` : "—%"}
          </span>
          <span className="text-decorative-foreground">·</span>
          <span className={sevenTone}>
            <span
              data-quota-prefix="7d"
              className="@max-[800px]/composer:hidden"
            >
              7d{" "}
            </span>
            {showNumbers && sevenD !== null ? `${sevenD}%` : "—%"}
          </span>
        </button>
      </HoverCardTrigger>
      <HoverCardContent align="end" className="w-[268px] p-0">
        <QuotaPanel data={data} deviceLabel={deviceLabel} t={t} />
      </HoverCardContent>
    </HoverCard>
  );
}

// quotaFootnote 给面板脚注挑文案。正常态说明"百分比是已用比例",异常态
// (429 退避 / 网络错误 / OAuth 过期 / 设备离线)换成对应说明并着 waiting 色。
function quotaFootnote(
  reason: string,
  device: string,
  t: TFunction,
): { text: string; warn: boolean } {
  switch (reason) {
    case "rate_limited":
      return {
        text: t("chat.quota.title.rateLimited", { device }),
        warn: true,
      };
    case "network":
      return { text: t("chat.quota.title.network", { device }), warn: true };
    case "auth_expired":
      return {
        text: t("chat.quota.title.authExpired", { device }),
        warn: true,
      };
    case "device_offline":
      return {
        text: t("chat.quota.title.deviceOffline", { device }),
        warn: true,
      };
    default:
      return { text: t("chat.quota.panel.usedNote"), warn: false };
  }
}

// QuotaRow 是面板里的一行窗口:名称 + 重置倒计时 + 百分比 + 进度条。
function QuotaRow({
  label,
  percent,
  resetsAt,
  t,
}: {
  label: string;
  percent: number;
  resetsAt?: unknown;
  t: TFunction;
}) {
  const pct = Math.round(percent);
  const level = usageLevel(pct);
  const remaining = formatResetIn(resetsAt);
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline gap-1.5 text-2xs">
        <span className="font-medium text-foreground">{label}</span>
        {remaining ? (
          <span className="font-mono text-muted-foreground">
            {t("chat.quota.resetRemaining", { time: remaining }).trim()}
          </span>
        ) : null}
        <span
          className={cn(
            "ml-auto font-mono tabular-nums",
            QUOTA_PANEL_TONE[level],
          )}
        >
          {pct}%
        </span>
      </div>
      <span className="h-1 overflow-hidden rounded-sm bg-border">
        <span
          className={cn("block h-1 rounded-sm", LEVEL_FILL_TONE[level])}
          style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
        />
      </span>
    </div>
  );
}

// QuotaPanel 是 HoverCard 的内容:标题 + 设备名 + 两个主窗口 + 可选的
// Sonnet / Opus 7 天分组 + 脚注。
function QuotaPanel({
  data,
  deviceLabel,
  t,
}: {
  data: import("../../../wailsjs/go/models").cc_usage_svc.UsageState;
  deviceLabel?: string;
  t: TFunction;
}) {
  const device = deviceLabel || "local";
  const d = data.data;
  const foot = quotaFootnote(data.reason, device, t);
  const sonnet = d?.sonnetWeeklyPercent;
  const opus = d?.opusWeeklyPercent;
  return (
    <div>
      <div className="flex items-center gap-1.5 border-b border-border px-3 py-2">
        <Timer
          className="size-3.5 shrink-0 text-foreground"
          aria-hidden="true"
        />
        <span className="text-xs font-semibold text-foreground">
          {t("chat.quota.panel.title")}
        </span>
        <span className="ml-auto truncate font-mono text-2xs text-muted-foreground">
          {device}
        </span>
      </div>
      {d ? (
        <div className="flex flex-col gap-2.5 px-3 py-2.5">
          <QuotaRow
            label={t("chat.quota.panel.fiveHour")}
            percent={d.fiveHourPercent}
            resetsAt={d.fiveHourResetsAt}
            t={t}
          />
          <QuotaRow
            label={t("chat.quota.panel.weekly")}
            percent={d.weeklyPercent}
            resetsAt={d.weeklyResetsAt}
            t={t}
          />
          {sonnet != null || opus != null ? (
            <div className="flex flex-col gap-2 border-l-2 border-border pl-2.5">
              {sonnet != null ? (
                <QuotaRow
                  label={t("chat.quota.panel.sonnetWeekly")}
                  percent={sonnet}
                  resetsAt={d.sonnetWeeklyResetsAt}
                  t={t}
                />
              ) : null}
              {opus != null ? (
                <QuotaRow
                  label={t("chat.quota.panel.opusWeekly")}
                  percent={opus}
                  resetsAt={d.opusWeeklyResetsAt}
                  t={t}
                />
              ) : null}
            </div>
          ) : null}
        </div>
      ) : null}
      <div
        className={cn(
          "border-t border-border px-3 py-1.5 text-2xs",
          foot.warn
            ? "bg-status-waiting-bg text-status-waiting"
            : "bg-muted text-muted-foreground",
        )}
      >
        {foot.text}
      </div>
    </div>
  );
}

const ChatComposer = React.forwardRef<
  ChatComposerHandle,
  DesktopChatComposerProps
>(function ChatComposer({ agentId = 0, cwd = "", onSlashRpc, ...rest }, ref) {
  const { agents } = useChatAgents();
  const { projects } = useProjectList();
  const mentionSources = React.useMemo(
    () => buildMentionSources(agents, projects),
    [agents, projects],
  );
  // 命令清单归宿主:静态注册表 + 该 agent 的技能命令,按 backend 过滤后交给包。
  // 包内只负责触发检测 / 排序 / 渲染(见包的 chat-input/slash/types.ts)。
  const skillCommands = useAgentSkillCommands(
    agentId,
    rest.backendType ?? "",
    cwd,
  );
  const slashCommands = React.useMemo(
    () => listAvailable(rest.backendType ?? "", skillCommands),
    [rest.backendType, skillCommands],
  );

  return (
    <SharedChatComposer
      {...rest}
      ref={ref}
      dropZone={DESKTOP_DROP_ZONE}
      mentionSources={mentionSources}
      slashCommands={slashCommands}
      onSlashSelect={(cmd, exec) => {
        // literal_text 由包内部直接填回编辑器(不自动发送),这里只接 rpc 类。
        if (exec.kind === "rpc") onSlashRpc?.(cmd, exec);
      }}
    />
  );
});

// Generic tool card extension point: canonical-tool/raw/card.tsx handles
// non-canonical tools; canonical-tool/<kind>/card.tsx handles canonical kinds.

// ─── ChatTranscript ──────────────────────────────────────────────────────────

// 距底 ≤32px 视为"贴底",与 chat-panel 的 TRANSCRIPT_BOTTOM_THRESHOLD 同义:
// 它是 anchorTo:"end" 在 live 行流式增长时"是否继续钉底"的容差;
// 用户上滑超过它就不再钉底,保住阅读历史的位置。
const STICK_TO_BOTTOM_THRESHOLD_PX = 32;
const TRANSCRIPT_VIRTUAL_OVERSCAN = 6;

/**
 * TranscriptLiveContent 是一条 assistant 消息此刻的流式内容。在 transcript-rows
 * 的 LiveRowContent 之上多带一个 liveRetry —— 后者只用于行视图的重试提示卡,
 * 不参与行构建。
 */
export type TranscriptLiveContent = LiveRowContent & {
  liveRetry?: RetryNotice | null;
  liveTurn?: LiveTurnInput | null;
};

type ChatTranscriptProps = {
  agentName: string;
  agentColor: AgentColor;
  /** 会话的工作目录，用于工具卡片把 cwd 内路径展示为相对路径。 */
  cwd?: string;
  /** 当前 chat session id —— AskUserQuestionCard 提交答案时要带它去 Wails 绑定。 */
  sessionId?: number;
  /** Transcript 的滚动容器。传入时启用动态高度虚拟列表。 */
  scrollElement?: HTMLElement | null;
  /** scrollElement 挂上前也保持虚拟化路径，避免长对话首帧全量 mount。 */
  virtualize?: boolean;
  /** 当前 tab 是否可见；从隐藏切回时触发虚拟列表重新测量。 */
  active?: boolean;
  messages: chat_svc.ChatMessage[];
  /**
   * 各 assistant 消息各自的流式内容,按 messageId 索引;由 chat-streams-store
   * 跨路由维护(每条 LiveStream 一项)。表里有 key 的消息就在流式中:其 liveBlocks
   * 摆在 persisted blocks 之后、liveTail 之前,整体顺序与真实流入顺序一致。
   *
   * **必须传全表**:一个会话可同时有用户轮 / 自主续轮 / 后台 subagent 活动轮多条流
   * 并存,只传一条(旧的单 liveTargetId 契约)会让其余几条的消息瞬间掉回持久化态。
   * 复用 ChatTranscript 的调用方漏传整表 = 那些消息完全不流式(参见 sess-1950)。
   */
  liveByMessageId?: ReadonlyMap<number, TranscriptLiveContent>;
  /** 用户点某条 assistant 上的「重新生成」时回调，参数是目标 assistant 的消息 id。 */
  onRerun?: (messageId: number) => void;
  /** 错误卡点击「继续」时回调，参数是失败的 assistant 消息 id。 */
  onContinue?: (messageId: number) => void;
  /** 用户点某条 user 消息上的「编辑」时回调，参数是 user 消息 id。 */
  onEdit?: (messageId: number) => void;
  /** stream 是否进行中。true 时在末尾 assistant 内挂 typing 指示器，覆盖首 chunk 前 / 工具返回后的空窗期。 */
  streaming?: boolean;
  /** claudecode CLI 正在跑 /compact 时为 true;末尾 assistant 的 typing indicator 替换为
   *  "正在压缩上下文…" chip,让用户知道这段时间在做什么。compact_boundary 到达自动清空。*/
  liveCompacting?: boolean;
  /** 与执行该会话那台远端 daemon 的通道断了、正在退避重连时为 true;末尾 assistant 的
   *  typing indicator 替换为断连形态,让"网断了"与"agent 在想"一眼可分。连接恢复即换回。
   *  它是运行态之上的修饰,不改 agentStatus。*/
  reconnecting?: boolean;
  onPlanActionStarted?: (stream: PlanActionStream, userText: string) => void;
  /** 停掉某张 AgentSpawn 卡对应的正在运行的子 agent / 后台任务(按 tool_use_id 下发 stop_task)。
   *  仅 backend 支持时由 ChatPanel 传入;未传 = 卡片不显示停止按钮。 */
  onStopSubagent?: (toolUseId: string) => void;
  /** 停掉 ChatPanel 启动并持有生命周期的本地命令；只读调用方不传。 */
  onStopLocalCommand?: (terminalId: string) => void | Promise<void>;
  /** Stable mounted chat tab key for UI drafts that survive route/tab remounts. */
  tabStateKey?: string;
  /** 占位 assistant 的 model 为空时，脚注用会话当前模型。 */
  fallbackModel?: string;
  /**
   * 还有更早的消息只拿到了元数据(正文没随本次加载下发,spec 2026-08-27 决策 6)。
   * 为真时转录顶部给出「取回更早正文」的入口,并在用户滚回顶部时自动去取。
   */
  hasEarlierMessages?: boolean;
  /** 更早那一段的正文正在取回来。 */
  loadingEarlier?: boolean;
  /** 取回更早那一段正文;未传 = 不给入口(只读调用方)。 */
  onLoadEarlier?: () => void;
};

type ChatTranscriptHandle = {
  scrollToMessage: (messageId: number) => void;
  // 锚点恢复:把锚点行钉到距视口顶 offset px 处,并随虚拟器逐行复测收敛。
  // rowKey(data-row-key)命中时精确钉回该行 —— 行级虚拟化下长消息拆成多行,
  // 只按 messageId 会塌到消息首行;rowKey 失效(行已消失/旧快照)回退消息首行。
  // 返回 false 表示该消息当前不在 displayMessages(被折叠 / 尚未加载),
  // 调用方应回退到像素恢复。
  scrollToAnchor: (
    messageId: number,
    offset: number,
    rowKey?: string,
  ) => boolean;
};

// findLastCompactBoundary 顺序扫所有 messages.blocks 找最后一条 type=compact_boundary
// 的位置;没找到返回 null。返回 (messageIdx, blockIdx) 让 ChatTranscript 知道从哪里
// 起算"压缩后"显示段:messages[messageIdx].blocks[blockIdx] 即 boundary 块本身。
function findLastCompactBoundary(
  messages: chat_svc.ChatMessage[],
): { messageIdx: number; blockIdx: number } | null {
  let found: { messageIdx: number; blockIdx: number } | null = null;
  messages.forEach((m, i) => {
    (m.blocks ?? []).forEach((b, j) => {
      if (b.type === "compact_boundary") {
        found = { messageIdx: i, blockIdx: j };
      }
    });
  });
  return found;
}

// useLocalDeviceFingerprint 交出本机设备指纹(R17 本机判定)。指纹是 keychain 里
// 的稳定值,模块级缓存一次 Wails 调用,进程内不再重复请求;组件用它计算
// sourceByMessageId(本机发出的用户消息不进来源表)。
let localFingerprintPromise: Promise<string> | null = null;

function getLocalFingerprintPromise(): Promise<string> {
  if (!localFingerprintPromise) {
    localFingerprintPromise = RemoteDeviceFingerprint().catch(() => "");
  }
  return localFingerprintPromise;
}

function useLocalDeviceFingerprint(): string | undefined {
  const [fp, setFp] = React.useState<string | undefined>(undefined);
  React.useEffect(() => {
    let alive = true;
    void getLocalFingerprintPromise().then((v) => {
      if (alive) setFp(v || undefined);
    });
    return () => {
      alive = false;
    };
  }, []);
  return fp;
}

// NO_LOADED_MESSAGES 是「一条正文都还没取到」时的稳定空表 —— 每次渲染新建 [] 会让
// 下游按数组身份做的记忆化整片失效。
const NO_LOADED_MESSAGES: chat_svc.ChatMessage[] = [];

// EARLIER_MESSAGES_SCROLL_THRESHOLD_PX:滚到距顶多近就去取更早的正文。留一段余量,
// 让用户在真正撞到顶之前就开始取,而不是先看见一段空白。
const EARLIER_MESSAGES_SCROLL_THRESHOLD_PX = 240;

const ChatTranscript = React.forwardRef<
  ChatTranscriptHandle,
  ChatTranscriptProps
>(function ChatTranscript(
  {
    agentName,
    agentColor,
    cwd,
    sessionId,
    scrollElement,
    virtualize = false,
    active = true,
    messages: allMessages,
    liveByMessageId,
    onContinue,
    onRerun,
    onEdit,
    onPlanActionStarted,
    onStopSubagent,
    onStopLocalCommand,
    tabStateKey,
    streaming = false,
    liveCompacting = false,
    reconnecting = false,
    fallbackModel = "",
    hasEarlierMessages = false,
    loadingEarlier = false,
    onLoadEarlier,
  },
  ref,
) {
  // 转录只渲染正文已经取全的消息。窗口外的消息手上只有元数据加派生视图点名的那几类
  // 块(见 use-chat-session 的 DERIVED_VIEW_BLOCK_TYPES),把它们当整条渲染,用户看到
  // 的是一份缺了工具结果 / 思考 / 嵌套卡的假转录 —— 宁可先不渲染,由顶部的入口取回来
  // 之后自然接上。未取正文的消息永远是列表**前缀**(窗口取的是末尾一段),所以这里
  // 切一刀就够;整表都已就绪时连数组引用一起原样传下去,下游的记忆化不被击穿。
  const messages = React.useMemo(() => {
    const first = allMessages.findIndex((m) => m.blocksLoaded !== false);
    if (first === 0) return allMessages;
    return first < 0 ? NO_LOADED_MESSAGES : allMessages.slice(first);
  }, [allMessages]);

  // onLoadEarlierRef 转发「取回更早正文」的回调,免得 chat-panel 每次重渲的 inline
  // lambda 把滚动监听器反复摘挂;重复触发由调用方的在飞守卫吸收。
  const onLoadEarlierRef = React.useRef(onLoadEarlier);
  React.useEffect(() => {
    onLoadEarlierRef.current = onLoadEarlier;
  }, [onLoadEarlier]);
  // lastAssistantId:生成指示器(三个点)的宿主。规则归共享包所有 ——
  // agentre-server 的转录按同一条规则挂,两份实现必然漂移(它那边就漂过:往回找
  // 最后一条 assistant,于是三点跳到上一轮的回复上)。判据见包里的注释。
  const lastAssistantId = React.useMemo(
    () => indicatorHostMessageId(messages),
    [messages],
  );

  // 折叠"压缩前"的旧消息:扫所有 messages.blocks,找最后一条 compact_boundary 所在的
  // (messageIdx, blockIdx);该位置之前的所有消息默认隐藏,该消息自己的更早 blocks 也
  // 一并裁掉。expanded=true 时退化为原始 messages 渲染。
  const [expanded, setExpanded] = React.useState(false);
  const fold = React.useMemo(
    () => findLastCompactBoundary(messages),
    [messages],
  );
  const folding = !expanded && fold !== null;
  const foldedCount = folding ? fold.messageIdx : 0;
  const displayMessages = React.useMemo<chat_svc.ChatMessage[]>(() => {
    if (!folding) return messages;
    // 保留 boundary 所在消息及之后;boundary 消息的更早 blocks 裁掉。spread 出来的对象
    // 失去了 wails class 的 convertValues 方法,但下游 MessageItem 只读字段,as 强转
    // 即可,不需要走 Object.assign(Object.create(proto)) 这种重活。
    const out: chat_svc.ChatMessage[] = [];
    for (let i = fold.messageIdx; i < messages.length; i++) {
      if (i === fold.messageIdx && fold.blockIdx > 0) {
        const m = messages[i];
        out.push({
          ...m,
          blocks: (m.blocks ?? []).slice(fold.blockIdx),
        } as chat_svc.ChatMessage);
      } else {
        out.push(messages[i]);
      }
    }
    return out;
  }, [folding, messages, fold]);

  // autonomousIds:自主续轮(CLI 后台任务完成后自主跑的一轮)的消息 id 集合。
  // 判定与它的全部理由归共享包的 transcript-turns 所有 —— 「什么算一轮」在界面上
  // 有两个用处(这里给自主轮挂 banner、「回到底部」药丸报「下面还有 N 轮」),两处
  // 各拼各的必然在自主续轮或旁白行这类边角上分家。
  //
  // 用完整消息表(而非 displayMessages,也不是切掉未取正文那段之后的 messages)算:
  // 判据是「这一轮前面有没有用户消息」,而 compact 折叠与正文窗口都会切掉前缀 ——
  // 切完之后首条 assistant 失去它的"前一条",会被误判成自主轮挂上 banner。
  // 角色序列是元数据,窗口外的消息照样带着。
  const autonomousIds = React.useMemo(
    () => autonomousTurnMessageIds(allMessages),
    [allMessages],
  );

  // 六个回调的稳定代理(useEvent 模式)住在 useTranscriptCallbacks 里。
  const {
    stableOnRerun,
    stableOnContinue,
    stableOnEdit,
    stableOnPlanActionStarted,
    stableOnStopSubagent,
    stableOnStopLocalCommand,
    hasStopLocalCommand,
  } = useTranscriptCallbacks({
    onRerun,
    onContinue,
    onEdit,
    onPlanActionStarted,
    onStopSubagent,
    onStopLocalCommand,
  });

  // displayMessages → 虚拟行。persisted 消息的行缓存在实例级 WeakMap(引用稳定
  // → 行组件 memo 恒命中);live 消息每 chunk 现场重建,重渲上限 = 可见窗口行数。
  const rowsCacheRef = React.useRef(
    new WeakMap<chat_svc.ChatMessage, TranscriptRow[]>(),
  );
  // 本会话的临时本地命令条目(!command),useShallow 浅比身份集合 —— output 流式
  // 追加重建数组但条目身份不变时不触发归并重算。
  const localCommands = useLocalCommandsStore(
    useShallow((s) => s.listForSession(sessionId ?? 0)),
  );
  // R17:非本机发出的用户消息的来源标识。本机指纹与本机消息的 sourceDevice 相等,
  // 全部被 buildSourceByMessageId 跳过 → 单客户端恒为空表,界面零变化。
  const localFingerprint = useLocalDeviceFingerprint();
  const sourceByMessageId = React.useMemo(
    () => buildSourceByMessageId(displayMessages, localFingerprint),
    [displayMessages, localFingerprint],
  );
  // settled:只依赖 messages(流式中引用稳定),整体 memoize —— 每 chunk 不再全量
  // 重建 rows + 两张索引图。live 内容变化时只走 applyLiveTranscriptRows 的 O(live)
  // 叠加(非 live 行与 settled 共享引用,行组件 memo 恒命中)。
  const settled = React.useMemo(
    () =>
      buildSettledTranscriptRows({
        autonomousIds,
        cache: rowsCacheRef.current,
        displayMessages,
        localCommands,
        sourceByMessageId,
      }),
    [autonomousIds, displayMessages, localCommands, sourceByMessageId],
  );
  const { rows, firstRowIndexByMessageId, rowIndexByKey } = React.useMemo(
    () =>
      applyLiveTranscriptRows(settled, {
        autonomousIds,
        cache: rowsCacheRef.current,
        displayMessages,
        liveByMessageId,
        localCommands,
        sourceByMessageId,
      }),
    [
      autonomousIds,
      displayMessages,
      liveByMessageId,
      localCommands,
      settled,
      sourceByMessageId,
    ],
  );

  const renderCtx = React.useMemo<TranscriptRenderContextValue>(
    () => ({
      agentName,
      // 头像节点由宿主给：包里的 MessageRow / ChatMessage 不认识桌面端的 16 色
      // agent 调色板与 icon-registry，只借包的 MESSAGE_AVATAR_CLASS 对齐头像列尺寸。
      agentAvatar: (
        <AgentAvatar
          name={agentName}
          initials={agentName.charAt(0)}
          color={agentColor}
          size="md"
          className={MESSAGE_AVATAR_CLASS}
        />
      ),
      cwd,
      // 只读调用方不传 onEdit/onRerun 时，上游 ref 为 undefined；
      // 此处有条件地传入稳定代理，让行视图能用 ctx?.onEdit 作存在性门控。
      onEdit: onEdit ? stableOnEdit : undefined,
      onContinue: onContinue ? stableOnContinue : undefined,
      onPlanActionStarted: stableOnPlanActionStarted,
      onStopLocalCommand: hasStopLocalCommand
        ? stableOnStopLocalCommand
        : undefined,
      onStopSubagent: onStopSubagent ? stableOnStopSubagent : undefined,
      onRerun: onRerun ? stableOnRerun : undefined,
      sessionId: sessionId ?? 0,
      tabStateKey,
    }),
    [
      agentColor,
      agentName,
      cwd,
      hasStopLocalCommand,
      onEdit,
      onContinue,
      onRerun,
      onStopSubagent,
      sessionId,
      stableOnEdit,
      stableOnContinue,
      stableOnPlanActionStarted,
      stableOnStopLocalCommand,
      stableOnStopSubagent,
      stableOnRerun,
      tabStateKey,
    ],
  );

  const shouldVirtualize = virtualize || scrollElement != null;
  const lastVirtualTotalSizeRef = React.useRef(0);
  const lastScrollRectRef = React.useRef({ height: 0, width: 0 });
  const lastScrollOffsetRef = React.useRef(0);
  const restoreScrollOffsetRef = React.useRef(false);
  const [, forceRestoreRender] = React.useState(0);

  const observeScrollRect = React.useCallback(
    (
      el: HTMLElement | null,
      cb: (rect: { height: number; width: number }) => void,
    ) => {
      const next = {
        height: el?.clientHeight ?? 0,
        width: el?.clientWidth ?? 0,
      };
      if (next.height > 0 || next.width > 0) {
        lastScrollRectRef.current = next;
        cb(next);
        return;
      }
      cb(active ? next : lastScrollRectRef.current);
    },
    [active],
  );

  // renderRowView:单个虚拟行的内容。非 live 行的 live* prop 全部收敛到稳定空值,
  // 让 TranscriptRowView 的 React.memo shallow 比较恒命中 —— 每个流式 chunk 只有
  // live 消息(和指示器宿主末行)重渲。
  const renderRowView = React.useCallback(
    (row: TranscriptRow) => {
      // 本行所属消息此刻的流式内容(没有 = 该消息不在流式中)。多条流并存时各查各的。
      const live = liveByMessageId?.get(row.messageId);
      const isLiveTail = row.isLastOfMessage && live != null;
      const showIndicator =
        row.isLastOfMessage &&
        streaming &&
        lastAssistantId != null &&
        row.messageId === lastAssistantId;
      return (
        <TranscriptRowView
          row={row}
          liveTail={isLiveTail ? (live?.liveTail ?? "") : ""}
          liveBlocks={isLiveTail ? live?.liveBlocks : undefined}
          liveRetry={isLiveTail ? (live?.liveRetry ?? null) : null}
          showIndicator={showIndicator}
          compacting={showIndicator && isLiveTail && liveCompacting}
          reconnecting={showIndicator && reconnecting}
          liveTurn={isLiveTail ? (live?.liveTurn ?? null) : null}
          fallbackModel={fallbackModel}
        />
      );
    },
    [
      fallbackModel,
      lastAssistantId,
      liveByMessageId,
      liveCompacting,
      reconnecting,
      streaming,
    ],
  );

  // eslint-disable-next-line react-hooks/incompatible-library -- TanStack Virtual intentionally owns mutable measurement callbacks.
  const virtualizer = useVirtualizer({
    count: rows.length,
    estimateSize: (index) =>
      // estimateRowSize(内容高度,按 132→148 等同源校准比例缩放)之上,再按
      // isLastRowOfMessage 补上 transcriptRowPadClass 的间距增量(消息末行 pb-7=28px /
      // 块内行 pb-2.5=10px,与纯乘法缩放旧 padding 得到的 ≈22.4px/≈8.97px 有
      // ≈5.6px/≈1px 缺口——两处 padding 打在同一个 measureElement div 上,详见
      // transcript-rows.ts:estimateRowSizeWithSpacing 的注释)。
      estimateRowSizeWithSpacing(rows, index),
    getItemKey: (index) => rows[index]?.key ?? index,
    getScrollElement: () => scrollElement ?? null,
    initialRect: {
      height: scrollElement?.clientHeight ?? 0,
      width: scrollElement?.clientWidth ?? 0,
    },
    observeElementOffset: (_instance, cb) => {
      const el = scrollElement;
      const readOffset = () => {
        const offset = el?.scrollTop ?? 0;
        if (active && !restoreScrollOffsetRef.current) {
          lastScrollOffsetRef.current = offset;
          return offset;
        }
        if (offset > 0) {
          lastScrollOffsetRef.current = offset;
          return offset;
        }
        return lastScrollOffsetRef.current;
      };
      cb(readOffset(), false);
      if (!el) return;
      let scrollEndTimer: number | null = null;
      const handler = () => {
        const offset = readOffset();
        cb(offset, true);
        if (scrollEndTimer != null) window.clearTimeout(scrollEndTimer);
        scrollEndTimer = window.setTimeout(() => {
          cb(readOffset(), false);
          scrollEndTimer = null;
        }, 150);
      };
      el.addEventListener("scroll", handler, { passive: true });
      return () => {
        if (scrollEndTimer != null) window.clearTimeout(scrollEndTimer);
        el.removeEventListener("scroll", handler);
      };
    },
    observeElementRect: (_instance, cb) => {
      const el = scrollElement ?? null;
      observeScrollRect(el, cb);
      if (!el || typeof ResizeObserver === "undefined") return;
      const observer = new ResizeObserver(() => {
        observeScrollRect(el, cb);
      });
      observer.observe(el);
      return () => observer.disconnect();
    },
    overscan: TRANSCRIPT_VIRTUAL_OVERSCAN,
    // 流式贴底交给虚拟器自己的测量回路,而不是 chat-panel 在每个 chunk 用
    // scrollTop=maxScrollTop 手动追(那条路读的是异步复测前的旧 getTotalSize,
    // 永远慢一帧→最新输出被压到折叠线以下)。anchorTo:"end" 在 live 行被
    // ResizeObserver 复测变高、且当前距底 ≤ 阈值时,于 resizeItem 测量回路内
    // 同步把滚动钉回底部,天然消除"慢一帧";上滑超过阈值则不钉,保住阅读位置。
    //
    // 刻意不开 followOnAppend:追"新追加整条消息"已由 chat-panel 的结构性 follow
    //(atBottom 时随 messages 变化滚到底)覆盖;而 followOnAppend 会在会话打开、
    // messages 从 0→N 时把空列表判定为"在末尾"抢先 scrollToEnd,覆盖掉
    //「恢复到上次上滑位置」的还原(正是要修的 wrong-restore),故不启用。
    anchorTo: "end",
    scrollEndThreshold: STICK_TO_BOTTOM_THRESHOLD_PX,
  });
  React.useLayoutEffect(() => {
    if (!scrollElement) return;
    virtualizer.measure();
  }, [scrollElement, virtualizer]);
  React.useLayoutEffect(() => {
    if (!active) {
      restoreScrollOffsetRef.current = true;
      return;
    }
    if (!restoreScrollOffsetRef.current) return;
    restoreScrollOffsetRef.current = false;
    const el = scrollElement;
    if (!el) return;
    const offset = lastScrollOffsetRef.current;
    if (el.scrollTop !== offset) el.scrollTop = offset;
    el.dispatchEvent(new Event("scroll"));
    forceRestoreRender((version) => version + 1);
  }, [active, scrollElement, virtualizer]);
  // 注意:这里不能在 active 翻成 true 时再调 virtualizer.measure()。
  // measure() 会 itemSizeCache.clear() 把所有行的真实测量值丢弃、整列瞬间塌回
  // estimateSize(132px),切回 tab 时引发可见的塌缩 / 闪烁 reflow。隐藏期间行
  // 根本不在 DOM 里(renderVirtualRows 在 !active 时为 false,故无从复测),重新
  // 可见时 measureElement 的 ResizeObserver 会自然对可见窗口逐行复测,无需整列清缓存。

  // 行级贴底跟随:anchorTo:"end" 只在「行 resize」时钉底(流式文本生长走那条路),
  // 而行模型下新 tool 卡 / indicator 是「行追加」—— followOnAppend 因 wrong-restore
  // (见 virtualizer 配置注释)刻意不开,这里自己补:仅当 ①tab 可见且不在恢复期
  // ②非首载(0→N 是打开会话回放,要让位给滚动恢复)③确实是尾部追加 ④追加前
  // 用户贴底(按追加前的 totalSize 判定)时,把滚动钉到新的末尾。
  const followTailRef = React.useRef({
    count: 0,
    tailKey: null as string | null,
    totalSize: 0,
  });
  React.useLayoutEffect(() => {
    const el = scrollElement;
    const prev = followTailRef.current;
    const tailKey = rows.at(-1)?.key ?? null;
    followTailRef.current = {
      count: rows.length,
      tailKey,
      totalSize: virtualizer.getTotalSize(),
    };
    if (!el || !active || restoreScrollOffsetRef.current) return;
    if (prev.count === 0) return;
    if (rows.length <= prev.count || tailKey === prev.tailKey) return;
    const wasAtEnd =
      prev.totalSize <= el.clientHeight ||
      el.scrollTop + el.clientHeight >=
        prev.totalSize - STICK_TO_BOTTOM_THRESHOLD_PX;
    if (!wasAtEnd) return;
    virtualizer.scrollToOffset(virtualizer.getTotalSize(), { align: "end" });
  }, [rows, active, scrollElement, virtualizer]);

  const [pendingScrollMessageId, setPendingScrollMessageId] = React.useState<
    number | null
  >(null);
  const scrollToMessage = React.useCallback(
    (messageId: number) => {
      // 消息首行 = 消息顶,align:"start" 视觉等价于旧 message 级行为。
      const index = firstRowIndexByMessageId.get(messageId);
      if (index != null) {
        virtualizer.scrollToIndex(index, { align: "start" });
        return;
      }
      if (folding && messages.some((m) => m.id === messageId)) {
        setExpanded(true);
        setPendingScrollMessageId(messageId);
        return;
      }
      // 目标是一条正文还没取回来的旧消息(大纲列的是**整条会话**的轮次,点它跳转是
      // 常规动作)。先记下意图再去取,由下面那个 effect 在行真正出现后落位 ——
      // 直接滚是滚不动的:此刻它还不在渲染的行里。
      if (allMessages.some((m) => m.id === messageId)) {
        setPendingScrollMessageId(messageId);
        onLoadEarlierRef.current?.();
      }
    },
    [allMessages, firstRowIndexByMessageId, folding, messages, virtualizer],
  );

  React.useEffect(() => {
    if (pendingScrollMessageId == null) return;
    const index = firstRowIndexByMessageId.get(pendingScrollMessageId);
    if (index == null) {
      const target = allMessages.find((m) => m.id === pendingScrollMessageId);
      // 目标已经不在表里(被编辑/重跑截断了):这次跳转没有落点,收手。
      if (!target) {
        setPendingScrollMessageId(null);
        return;
      }
      // 正文还没取到就继续往前取;取到头(没有更早的)时留着意图不动 ——
      // 折叠展开那条路正是在这个窗口里等下一次重渲的。
      if (target.blocksLoaded === false && hasEarlierMessages) {
        onLoadEarlierRef.current?.();
      }
      return;
    }
    virtualizer.scrollToIndex(index, { align: "start" });
    setPendingScrollMessageId(null);
  }, [
    allMessages,
    firstRowIndexByMessageId,
    hasEarlierMessages,
    pendingScrollMessageId,
    virtualizer,
  ]);

  const anchorRestoreFrameRef = React.useRef<number | null>(null);
  const cancelAnchorRestore = React.useCallback(() => {
    if (anchorRestoreFrameRef.current != null) {
      window.cancelAnimationFrame(anchorRestoreFrameRef.current);
      anchorRestoreFrameRef.current = null;
    }
  }, []);
  // scrollToAnchor:把"保存时位于视口顶部的那条消息"(messageId)重新钉到距视口顶
  // offset px 处。路由重挂时虚拟器整列只有 estimate 高度,getOffsetForIndex 会随可见
  // 窗口逐行复测而变化;这里用 rAF 循环重算 target 直到稳定(连续 2 帧不变)或封顶,
  // 从而消除"仅凭像素 scrollTop 会落到错消息"的冷启动漂移——锚点钉的是消息身份,
  // 不是像素值。返回 false=该消息不在 displayMessages(被折叠/未加载),交回调用方
  // 回退像素恢复。
  const scrollToAnchor = React.useCallback(
    (messageId: number, offset: number, rowKey?: string): boolean => {
      const index =
        (rowKey != null ? rowIndexByKey.get(rowKey) : undefined) ??
        firstRowIndexByMessageId.get(messageId) ??
        -1;
      const el = scrollElement;
      if (index < 0 || !el) return false;
      cancelAnchorRestore();
      let prevTarget = -1;
      let stableFrames = 0;
      let frames = 0;
      const settle = () => {
        anchorRestoreFrameRef.current = null;
        const info = virtualizer.getOffsetForIndex(index, "start");
        if (!info) return;
        const target = Math.max(0, info[0] + offset);
        if (Math.abs(el.scrollTop - target) > 1) el.scrollTop = target;
        stableFrames =
          Math.abs(target - prevTarget) <= 1 ? stableFrames + 1 : 0;
        prevTarget = target;
        frames += 1;
        if (stableFrames < 2 && frames < 30) {
          anchorRestoreFrameRef.current = window.requestAnimationFrame(settle);
        }
      };
      // 同步先钉一帧(调用点是 chat-panel 的 useLayoutEffect,paint 前生效),
      // 避免路由重挂首帧闪在顶部;后续逐帧由 settle 自己挂 rAF 收敛。
      settle();
      return true;
    },
    [
      cancelAnchorRestore,
      firstRowIndexByMessageId,
      rowIndexByKey,
      scrollElement,
      virtualizer,
    ],
  );
  React.useEffect(() => () => cancelAnchorRestore(), [cancelAnchorRestore]);

  React.useEffect(() => {
    const el = scrollElement;
    if (!el || !hasEarlierMessages || !onLoadEarlier) return;
    const handler = () => {
      if (el.scrollTop > EARLIER_MESSAGES_SCROLL_THRESHOLD_PX) return;
      onLoadEarlierRef.current?.();
    };
    el.addEventListener("scroll", handler, { passive: true });
    return () => el.removeEventListener("scroll", handler);
  }, [hasEarlierMessages, onLoadEarlier, scrollElement]);

  React.useImperativeHandle(
    ref,
    () => ({
      scrollToMessage,
      scrollToAnchor,
    }),
    [scrollToAnchor, scrollToMessage],
  );

  const renderVirtualRows =
    shouldVirtualize && active && !restoreScrollOffsetRef.current;
  const virtualTotalSize = virtualizer.getTotalSize();
  if (virtualTotalSize > 0) {
    lastVirtualTotalSizeRef.current = virtualTotalSize;
  }
  const virtualSpacerSize =
    virtualTotalSize > 0
      ? virtualTotalSize
      : // 48→54:同上,per-row 兜底估值随字号/间距校准同步调整,避免首帧 spacer
        // 高度系统性偏矮。
        lastVirtualTotalSizeRef.current || rows.length * 54;

  // 行间距:消息末行 pb-7(消息间距),消息内分片行 pb-2.5(block 间距)。padding
  // 打在行 wrapper 上,跟随 measureElement 一起计入行高 —— isLastRowOfMessage 与
  // estimateSize 里 estimateRowSizeWithSpacing 补间距增量共用同一份边界判断,避免
  // 两处"是否消息末行"各算各的而漂移。

  return (
    <TooltipProvider delayDuration={200}>
      <TranscriptUIStateProvider>
        <TranscriptRenderContext.Provider value={renderCtx}>
          {/* 不再加 max-w-4xl —— 内部 ChatMessage 已经 cap 在 max-w-measure,
          这里再叠一层外层 max-w 没有任何收紧效果,只会留出 phantom 空白。 */}
          <div className={shouldVirtualize ? "min-h-full" : "flex flex-col"}>
            {hasEarlierMessages && onLoadEarlier ? (
              <EarlierMessagesLoader
                loading={loadingEarlier}
                onLoad={onLoadEarlier}
              />
            ) : null}
            {folding && foldedCount > 0 ? (
              <CompactHistoryFold
                count={foldedCount}
                onExpand={() => setExpanded(true)}
              />
            ) : null}
            {shouldVirtualize ? (
              <div
                className="relative w-full"
                style={{ height: `${virtualSpacerSize}px` }}
              >
                {renderVirtualRows
                  ? virtualizer.getVirtualItems().map((virtualItem) => {
                      const row = rows[virtualItem.index];
                      if (!row) return null;
                      return (
                        <div
                          key={virtualItem.key}
                          ref={virtualizer.measureElement}
                          data-index={virtualItem.index}
                          data-message-id={row.messageId}
                          data-row-key={row.key}
                          className={cn(
                            "absolute left-0 top-0 w-full",
                            transcriptRowPadClass(rows, virtualItem.index),
                          )}
                          style={{
                            transform: `translateY(${virtualItem.start}px)`,
                          }}
                        >
                          {renderRowView(row)}
                        </div>
                      );
                    })
                  : null}
              </div>
            ) : (
              rows.map((row, index) => (
                <div
                  key={row.key}
                  data-message-id={row.messageId}
                  data-row-key={row.key}
                  className={transcriptRowPadClass(rows, index)}
                >
                  {renderRowView(row)}
                </div>
              ))
            )}
          </div>
        </TranscriptRenderContext.Provider>
      </TranscriptUIStateProvider>
    </TooltipProvider>
  );
});

export {
  ApprovalGate,
  ChatComposer,
  ChatMessage,
  ChatTranscript,
  CodeBlock,
  ErrorCard,
  MessageMeta,
  ToolCall,
};
export type { ChatTranscriptHandle };
