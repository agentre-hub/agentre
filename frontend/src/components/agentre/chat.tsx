import * as React from "react";
import type { TFunction } from "i18next";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useShallow } from "zustand/react/shallow";
import {
  Check,
  EyeOff,
  Gauge,
  ImagePlus,
  LoaderCircle,
  Pencil,
  SendHorizontal,
  SquareTerminal,
  Timer,
  TriangleAlert,
  Wrench,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@/components/ui/hover-card";
import { TooltipProvider } from "@/components/ui/tooltip";
import { isNoticeOnlyMessage } from "@/lib/notice-message";
import { cn } from "@/lib/utils";

import type { Editor } from "@tiptap/react";
import {
  AIChatInput,
  type AIChatInputHandle,
  type LocalCommandHistoryScope,
  type LocalCommandSubmitHandler,
} from "./chat-input";
import {
  applyLiveTranscriptRows,
  buildSettledTranscriptRows,
  buildSourceByMessageId,
  CodeBlock,
  estimateRowSizeWithSpacing,
  isLastRowOfMessage,
  TranscriptCard,
  TranscriptUIStateProvider,
  type LiveRowContent,
  type PlanActionStream,
  type TranscriptRow,
} from "@agentre-ai/agentre-ui";
import { CompactHistoryFold } from "./compact-history-fold";
import {
  ChatMessage,
  ErrorCard,
  MessageMeta,
  MESSAGE_AVATAR_CLASS,
  TranscriptRenderContext,
  TranscriptRowView,
  type TranscriptRenderContextValue,
} from "@agentre-ai/agentre-ui";
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
import { buildMentionSources } from "./chat-input/mentions/build-sources";
import { LOCAL_COMMAND_HISTORY_CLEAR_SELECTOR } from "./chat-input/local-command-history/history-popover";
import { resolveDroppedPaths } from "./chat-input/drop";
import { useFileDropZone } from "./chat-input/use-file-drop";
import { useAgentSkillCommands } from "./slash-commands";

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

type ChatComposerProps = Omit<React.ComponentProps<"form">, "onSubmit"> & {
  onSubmit?: (message: ChatComposerSubmit) => void;
  placeholder?: string;
  /** 历史消息文本，按时间倒序排列（最新在前），方向键 ↑↓ 浏览。 */
  userMessageHistory?: string[];
  /** 编辑模式：true 时输入框上方挂"正在编辑"提示条，发送按钮文案改为"保存"。 */
  editing?: boolean;
  /** 进入编辑模式时载入到输入框的初始草稿。每次切换编辑目标都会重新载入。 */
  editDraft?: string;
  /** 用户点击提示条上的"取消编辑"。父组件应清掉编辑状态并恢复正常输入。 */
  onCancelEdit?: () => void;
  /** 在输入框上方插入的内容（例：QueuedMessagesBar）。Composer 不关心内容，
   *  只负责把节点放进 card 顶部，跟 editing banner 同位。空节点正常渲染（组件自决可见性）。 */
  topSlot?: React.ReactNode;
  /** 发送 RPC 在途（await SendChatMessage 未返回）。true 时发送按钮禁用并转 spinner。 */
  sending?: boolean;
  /** 上下文用量。max <= 0 时整块不渲染（未知模型 / 后端未配置）。 */
  contextUsage?: { used: number; max: number };
  /** Claude Code OAuth 5h/7d 配额。undefined / reason='no_credentials' 时整块不渲染。
   *  由 chat-panel 通过 useCCUsage(deviceKey) 拉到后传入。 */
  quotaUsage?: import("../../../wailsjs/go/models").cc_usage_svc.UsageState;
  /** 配额对应的 device 友好名(local 或远端设备名),供 QuotaMeter HoverCard 文案使用。 */
  quotaDeviceLabel?: string;
  /** Permission mode 控件，仅在 claudecode 后端时由 chat-panel 注入。null 时整块不渲染。 */
  permissionModeSlot?: React.ReactNode;
  /** 模型切换控件，与 permissionModeSlot 并排（模型 pill 紧随其后）。null 时不渲染。 */
  modelSlot?: React.ReactNode;
  /** 焦点在 composer 内时按下 Shift+Tab 的钩子（用于循环切换 permission mode）。 */
  onShiftTab?: () => void;
  /** 挂载时自动 focus 输入框。新建会话场景下由 chat-panel 传 true，让用户一打开
   *  就能直接打字、不用再点输入框一次。 */
  autoFocusOnMount?: boolean;
  /** 输入框只读禁用。新建会话场景下由 chat-panel 对不可对话 Agent 传入。 */
  disabled?: boolean;
  /** 当前会话 backend 类型;让 AIChatInput 启用 slash menu 并按 backend 过滤候选命令。
   *  空串/省略 → 不启用 slash menu。 */
  backendType?: string;
  /** 当前会话或新会话的 agent id,用于加载该 agent 最终生效的 skill 命令。 */
  agentId?: number;
  /** 当前会话/项目的工作目录,用于发现 project-scoped Skill。 */
  cwd?: string;
  /** 当前 backend 是否支持图片输入。false 时不渲染图片附件入口。 */
  supportsImageInput?: boolean;
  /** slash menu rpc 类命令的回调(literal_text 类由 AIChatInput 内部直接填回编辑器,
   *  不自动发送,也不会冒泡到这里)。省略则 slash menu 不启用。 */
  onSlashRpc?: (
    cmd: import("./slash-commands").SlashCommand,
    exec: Extract<import("./slash-commands").SlashExec, { kind: "rpc" }>,
  ) => void;
  /** 本地命令回调:启动命令后返回后端解析出的稳定设备与 cwd，供历史落盘。 */
  onRunCommand?: LocalCommandSubmitHandler;
  /** 进入 ! 模式时通知上层重新解析当前执行作用域。 */
  onCommandModeChange?: (active: boolean) => void;
  /** 当前执行设备与 cwd 组成的 Shell 历史隔离作用域。 */
  localCommandHistoryScope?: LocalCommandHistoryScope;
  /** 透传给内层 AIChatInput 的编辑器 ref,供测试驱动编辑器内容。 */
  editorRef?: React.RefObject<Editor | null>;
};

export type ChatImageAttachment = {
  dataUrl: string;
  mediaType: string;
  name: string;
};

export type ChatComposerSubmit = {
  images?: ChatImageAttachment[];
  text: string;
};

// ChatComposer 的命令式句柄。restoreDraft 用于发送失败时把用户刚提交的文本 + 图片
// 原样放回输入框（草稿恢复）；clearDraft 清空输入框 + 图片附件（丢弃草稿）。
export type ChatComposerHandle = {
  restoreDraft: (text: string, images: ChatImageAttachment[]) => void;
  clearDraft: () => void;
};

const CHAT_IMAGE_ACCEPT = "image/png,image/jpeg,image/webp";
const MAX_CHAT_IMAGE_COUNT = 4;
const MAX_CHAT_IMAGE_BYTES = 5 * 1024 * 1024;

function readImageFile(file: File): Promise<ChatImageAttachment> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(reader.error ?? new Error("read failed"));
    reader.onload = () => {
      if (typeof reader.result !== "string") {
        reject(new Error("invalid image data"));
        return;
      }
      resolve({
        dataUrl: reader.result,
        mediaType: file.type,
        name: file.name,
      });
    };
    reader.readAsDataURL(file);
  });
}

function imageFilesFromClipboard(data: DataTransfer): File[] {
  const itemFiles = Array.from(data.items ?? [])
    .filter((item) => item.kind === "file" && item.type.startsWith("image/"))
    .map((item) => item.getAsFile())
    .filter((file): file is File => !!file);
  if (itemFiles.length > 0) return itemFiles;
  return Array.from(data.files ?? []).filter((file) =>
    file.type.startsWith("image/"),
  );
}

// 把 token 数显示成 "42.3k / 1M" 这种紧凑形式，跟 inline 底栏的 10px 字号匹配。
// 三档，k 与 M 同构（商小于阈值保 1 位小数、否则取整）：
//   - < 1000        → 原样
//   - [1e3, 1e6)    → k：商 >= 100 取整，否则 1 位小数
//   - >= 1e6        → M：商 >= 10 取整，否则 1 位小数；整数时省掉 ".0"（1e6 → "1M"）
// 额外一条：k 档取整后要是凑够 1000（999_999 → "1000k"），改按 M 档渲染 —— "1000k"
// 这个字符串在任何输入下都不该出现，本来就是这次要消灭的东西。
export function formatTokens(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1_000_000) {
    const v = n / 1000;
    if (v < 100) return `${v.toFixed(1)}k`;
    const rounded = Math.round(v);
    if (rounded < 1000) return `${rounded}k`;
    // 落到这里说明四舍五入把它顶进了 M 档，交给下面统一渲染。
  }
  const v = n / 1_000_000;
  if (v >= 10) return `${Math.round(v)}M`;
  const s = v.toFixed(1);
  return `${s.endsWith(".0") ? s.slice(0, -2) : s}M`;
}

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

// quotaLevel 是配额告警阈值的唯一来源。每个窗口各自定级 —— 5h 告急不该把 7d
// 一起染红,否则用户看不出该等 3 小时还是等 4 天。
type QuotaLevel = "ok" | "warn" | "danger";

function quotaLevel(percent: number | null): QuotaLevel {
  if (percent === null) return "ok";
  if (percent >= 90) return "danger";
  if (percent >= 75) return "warn";
  return "ok";
}

// 配色表共用 quotaLevel 定级。文字色分表是因为"正常"态各处诉求不同(底栏配额要退到
// 背景里,面板与上下文里这个数字是主角);填充色三处一致,故只有一张表。
// 分表而不是拿 class 字符串去比较判断。
const QUOTA_METER_TONE: Record<QuotaLevel, string> = {
  ok: "text-muted-foreground",
  warn: "text-status-waiting",
  danger: "text-status-error",
};
const QUOTA_PANEL_TONE: Record<QuotaLevel, string> = {
  ok: "text-foreground",
  warn: "text-status-waiting",
  danger: "text-status-error",
};
const LEVEL_FILL_TONE: Record<QuotaLevel, string> = {
  ok: "bg-primary",
  warn: "bg-status-waiting",
  danger: "bg-status-error",
};
// 上下文计量器的文字色:正常态用 primary-text(它是底栏里唯一常驻的定量信息,
// 该被看见),告警两档与配额一致。
const CONTEXT_METER_TONE: Record<QuotaLevel, string> = {
  ok: "text-primary-text",
  warn: "text-status-waiting",
  danger: "text-status-error",
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
function QuotaMeter({
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
    ? "text-subtle-foreground"
    : QUOTA_METER_TONE[quotaLevel(fiveH)];
  const sevenTone = offline
    ? "text-subtle-foreground"
    : QUOTA_METER_TONE[quotaLevel(sevenD)];

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
            offline ? "text-subtle-foreground" : "text-muted-foreground",
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
          <span className="text-subtle-foreground">·</span>
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
  const level = quotaLevel(pct);
  const remaining = formatResetIn(resetsAt);
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline gap-1.5 text-2xs">
        <span className="font-medium text-foreground">{label}</span>
        {remaining ? (
          <span className="font-mono text-subtle-foreground">
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
        <span className="ml-auto truncate font-mono text-2xs text-subtle-foreground">
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

function ContextMeter({ used, max }: { used: number; max: number }) {
  const { t } = useTranslation();
  const safeUsed = Math.max(0, used);
  const ratio = max > 0 ? Math.min(1, safeUsed / max) : 0;
  const pct = Math.round(ratio * 100);
  // 阈值与配额共用 quotaLevel(≥90 危险 / ≥75 警告),别在这里再写一份 —— 同一个文件
  // 里两套 90/75 常量迟早会改漏一处。传 ratio*100 而不是取整后的 pct,保持既有边界
  // 行为不变(ratio 0.895 仍算 warning,不因四舍五入跳成 danger)。
  const level = quotaLevel(ratio * 100);
  // 调色板仍是上下文自己的:它的"正常"态是 primary 着色(这个数字是主角),
  // 而底栏配额的"正常"态要退到背景里 —— 与 QUOTA_METER_TONE / QUOTA_PANEL_TONE
  // 同源不同表。
  const tone = CONTEXT_METER_TONE[level];
  const fill = LEVEL_FILL_TONE[level];
  return (
    <div
      className="flex min-w-0 items-center gap-2 overflow-hidden font-mono text-meta whitespace-nowrap text-muted-foreground"
      aria-label={t("chat.context.aria", { max, used: safeUsed })}
    >
      <Gauge className="size-2.5 shrink-0" aria-hidden="true" />
      {/* 中档起隐藏文字标签:图标 + 数字已足够辨识, 标签是最先该让位的冗余。 */}
      <span className="font-sans @max-[1000px]/composer:hidden">
        {t("chat.context.label")}
      </span>
      <span className="inline-flex items-center gap-0.5 tabular-nums">
        <span className="font-medium text-foreground">
          {formatTokens(safeUsed)}
        </span>
        <span className="text-subtle-foreground"> / </span>
        <span>{formatTokens(max)}</span>
      </span>
      <span
        // 窄档整条隐藏:它与紧邻的百分比表达同一个量,是行内最贵的冗余装饰。
        className="h-1 w-24 shrink-0 overflow-hidden rounded-sm bg-border @max-[800px]/composer:hidden"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={max}
        aria-valuenow={Math.min(safeUsed, max)}
      >
        <span
          className={cn("block h-1 rounded-sm transition-[width]", fill)}
          style={{ width: `${pct}%` }}
        />
      </span>
      <span className={cn("font-medium tabular-nums", tone)}>{pct}%</span>
    </div>
  );
}

const ChatComposer = React.forwardRef<ChatComposerHandle, ChatComposerProps>(
  function ChatComposer(
    {
      className,
      onSubmit,
      placeholder,
      userMessageHistory,
      editing = false,
      editDraft,
      onCancelEdit,
      topSlot,
      contextUsage,
      quotaUsage,
      quotaDeviceLabel,
      permissionModeSlot,
      modelSlot,
      onShiftTab,
      autoFocusOnMount = false,
      backendType,
      agentId = 0,
      cwd = "",
      supportsImageInput = true,
      onSlashRpc,
      onRunCommand,
      onCommandModeChange,
      localCommandHistoryScope,
      editorRef,
      onPasteCapture,
      disabled = false,
      sending = false,
      ...props
    }: ChatComposerProps,
    ref,
  ) {
    const { t } = useTranslation();
    const { agents } = useChatAgents();
    const { projects } = useProjectList();
    const mentionSources = React.useMemo(
      () => buildMentionSources(agents, projects),
      [agents, projects],
    );
    const inputRef = React.useRef<AIChatInputHandle>(null);
    const fileInputRef = React.useRef<HTMLInputElement>(null);
    const [isEmpty, setIsEmpty] = React.useState(true);
    const [images, setImages] = React.useState<ChatImageAttachment[]>([]);
    const [imageError, setImageError] = React.useState("");
    const [commandMode, setCommandMode] = React.useState(false);
    const skillCommands = useAgentSkillCommands(
      agentId,
      backendType ?? "",
      cwd,
    );
    const resolvedPlaceholder =
      placeholder ??
      t(
        backendType === "codex"
          ? "chat.composer.placeholderCodex"
          : backendType === "claudecode"
            ? "chat.composer.placeholderClaude"
            : backendType === "piagent"
              ? "chat.composer.placeholderPi"
              : "chat.composer.placeholder",
      );

    // 发送失败草稿恢复:把用户刚提交的文本 + 图片原样放回输入框（父组件在 doSend
    // 的 catch 里调用）。loadDraft 走与编辑模式载入草稿同一路径,行为一致。
    React.useImperativeHandle(
      ref,
      () => ({
        restoreDraft: (text: string, restoreImages: ChatImageAttachment[]) => {
          inputRef.current?.loadDraft(text);
          setImages(restoreImages);
          setImageError("");
        },
        clearDraft: () => {
          inputRef.current?.clear();
          setImages([]);
          setImageError("");
        },
      }),
      [],
    );

    // 切换到编辑模式（或换了编辑目标）时把目标文本载进 TipTap，并把光标抓回输入框；
    // 退出编辑模式时清空输入，免得上一次的编辑残留干扰下一条新消息。
    const wasEditingRef = React.useRef(false);
    React.useEffect(() => {
      if (editing) {
        if (editDraft !== undefined) {
          inputRef.current?.loadDraft(editDraft);
        }
        inputRef.current?.focus();
      } else if (wasEditingRef.current) {
        inputRef.current?.clear();
      }
      if (editing) {
        setImages([]);
        setImageError("");
      }
      wasEditingRef.current = editing;
    }, [editing, editDraft]);

    const wasAutoFocusOnMountRef = React.useRef(autoFocusOnMount);
    React.useEffect(() => {
      const wasAutoFocusOnMount = wasAutoFocusOnMountRef.current;
      wasAutoFocusOnMountRef.current = autoFocusOnMount;
      if (!autoFocusOnMount || wasAutoFocusOnMount) return;
      inputRef.current?.focus();
    }, [autoFocusOnMount]);

    React.useEffect(() => {
      if (supportsImageInput) return;
      setImages([]);
      setImageError("");
      if (fileInputRef.current) fileInputRef.current.value = "";
    }, [supportsImageInput]);

    const dropRef = React.useRef<HTMLFormElement>(null);

    const handleDroppedPaths = React.useCallback(
      (paths: string[]) => {
        if (disabled) return;
        void (async () => {
          const { attachments, text } = await resolveDroppedPaths(paths, {
            allowImages: !editing && supportsImageInput,
            remainingImageSlots: MAX_CHAT_IMAGE_COUNT - images.length,
            readImages: async (imagePaths) => {
              const resp = await ChatReadDroppedImages(
                chat_svc.ReadDroppedImagesRequest.createFrom({
                  paths: imagePaths,
                }),
              );
              return (resp.items ?? []).map((it) => ({
                path: it.path,
                kind:
                  it.kind === "image" ? ("image" as const) : ("path" as const),
                name: it.name,
                mediaType: it.mediaType,
                dataUrl: it.dataUrl,
              }));
            },
          });
          if (attachments.length > 0) {
            setImages((prev) => [...prev, ...attachments]);
            setImageError("");
          }
          if (text) inputRef.current?.insertText(text);
        })();
      },
      [disabled, editing, supportsImageInput, images.length],
    );

    const { isDragOver } = useFileDropZone({
      ref: dropRef,
      enabled: !disabled,
      onPaths: handleDroppedPaths,
    });

    function handleSend(text: string) {
      if (disabled) return;
      const trimmed = text.trim();
      if (!trimmed && images.length === 0) return;
      onSubmit?.(
        images.length > 0 ? { images, text: trimmed } : { text: trimmed },
      );
      setImages([]);
      setImageError("");
    }

    function handleFormSubmit(event: React.FormEvent<HTMLFormElement>) {
      event.preventDefault();
      if (disabled) return;
      if (isEmpty && images.length > 0) {
        handleSend("");
        return;
      }
      inputRef.current?.submit();
    }

    async function handleImageFiles(files: FileList | readonly File[] | null) {
      try {
        if (disabled || !files || files.length === 0) return;
        const nextFiles = Array.from(files);
        if (images.length + nextFiles.length > MAX_CHAT_IMAGE_COUNT) {
          setImageError(
            t("chat.composer.imageErrors.tooMany", {
              count: MAX_CHAT_IMAGE_COUNT,
            }),
          );
          return;
        }
        const bad = nextFiles.find(
          (file) =>
            !CHAT_IMAGE_ACCEPT.split(",").includes(file.type) ||
            file.size > MAX_CHAT_IMAGE_BYTES,
        );
        if (bad) {
          setImageError(t("chat.composer.imageErrors.unsupported"));
          return;
        }
        const attachments = await Promise.all(nextFiles.map(readImageFile));
        setImages((prev) => [...prev, ...attachments]);
        setImageError("");
      } catch {
        setImageError(t("chat.composer.imageErrors.readFailed"));
      } finally {
        if (fileInputRef.current) fileInputRef.current.value = "";
      }
    }

    function handlePasteCapture(event: React.ClipboardEvent<HTMLFormElement>) {
      onPasteCapture?.(event);
      if (event.defaultPrevented || disabled || editing || !supportsImageInput)
        return;
      const pastedImages = imageFilesFromClipboard(event.clipboardData);
      if (pastedImages.length === 0) return;
      event.preventDefault();
      void handleImageFiles(pastedImages);
    }

    // Esc 取消编辑。TipTap 的 handleKeyDown 不处理 Esc，所以这里在 form 层捕获；
    // 非编辑态下不消费，让默认行为走。
    //
    // Shift+Tab 循环切换 permission mode —— 对齐 Claude TUI；focus 在 composer 内
    // （TipTap editor / 普通按钮）时都会冒泡到 form，preventDefault 拦掉默认 tab 切换。
    // 历史 Clear footer 保留原生反向焦点；编辑模式也不消费 Shift+Tab。
    function handleFormKeyDown(event: React.KeyboardEvent<HTMLFormElement>) {
      if (
        !editing &&
        isEmpty &&
        images.length > 0 &&
        event.key === "Enter" &&
        !event.shiftKey &&
        !event.metaKey &&
        !event.ctrlKey &&
        !event.altKey &&
        !event.nativeEvent.isComposing
      ) {
        event.preventDefault();
        handleSend("");
        return;
      }
      if (editing && event.key === "Escape") {
        event.preventDefault();
        onCancelEdit?.();
        return;
      }
      if (
        !event.defaultPrevented &&
        !editing &&
        onShiftTab &&
        event.key === "Tab" &&
        event.shiftKey &&
        !event.metaKey &&
        !event.ctrlKey &&
        !event.altKey
      ) {
        if (
          event.target instanceof Element &&
          event.target.closest(LOCAL_COMMAND_HISTORY_CLEAR_SELECTOR)
        ) {
          return;
        }
        event.preventDefault();
        onShiftTab();
      }
    }

    return (
      <form
        ref={dropRef}
        className={cn(
          // @container/composer:底栏按 composer 自身宽度分档降级 —— chat panel 的实际
          // 宽度取决于侧栏 / 右侧面板开合,视口宽度读不到它。
          "@container/composer relative w-full border-t border-border bg-background px-7 py-3.5",
          className,
        )}
        onSubmit={handleFormSubmit}
        onKeyDown={handleFormKeyDown}
        onPasteCapture={handlePasteCapture}
        {...props}
      >
        {isDragOver ? (
          <div
            className="pointer-events-none absolute inset-2 z-10 flex items-center justify-center rounded-md border-2 border-dashed border-ring bg-background/85 text-sm font-medium text-foreground"
            aria-hidden="true"
          >
            {t("chat.composer.dropHint")}
          </div>
        ) : null}
        <div
          className={cn(
            "flex w-full flex-col overflow-hidden rounded-md border bg-card shadow-xs transition-colors",
            "focus-within:ring-[3px] focus-within:ring-ring/50",
            editing
              ? "border-primary-text/45 focus-within:border-primary-text/70"
              : "border-border focus-within:border-ring",
          )}
        >
          {topSlot}
          {editing ? (
            <div
              role="status"
              aria-label={t("chat.composer.editing.aria")}
              className="flex items-center gap-2 border-b border-primary-text/20 bg-primary-soft px-3 py-1.5 text-meta"
            >
              <Pencil
                className="size-3 shrink-0 text-primary-text"
                aria-hidden="true"
              />
              <span className="font-semibold text-primary-text">
                {t("chat.composer.editing.title")}
              </span>
              <span className="text-muted-foreground">·</span>
              <span className="min-w-0 flex-1 truncate text-muted-foreground">
                {t("chat.composer.editing.description")}
              </span>
              <button
                type="button"
                aria-label={t("chat.composer.editing.cancel")}
                title={t("chat.composer.editing.cancelTitle")}
                className="inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                onClick={() => onCancelEdit?.()}
              >
                <X className="size-3" aria-hidden="true" />
              </button>
            </div>
          ) : null}
          {!editing && commandMode ? (
            <div
              role="status"
              className="flex items-center gap-2 border-b border-primary-text/20 bg-primary-soft px-3 py-1.5 text-meta"
            >
              <SquareTerminal
                className="size-3 shrink-0 text-primary-text"
                aria-hidden="true"
              />
              <span className="min-w-0 flex-1 truncate font-medium text-primary-text">
                {t("chat.composer.command.banner")}
              </span>
              <span className="inline-flex items-center gap-1 text-muted-foreground">
                <EyeOff className="size-3" aria-hidden="true" />
                {t("localCommand.notSharedWithAI")}
              </span>
            </div>
          ) : null}
          <div className="flex flex-col gap-1 px-3.5 pt-2.5 pb-1">
            {!editing && images.length > 0 ? (
              <div className="flex flex-wrap gap-2 pb-1">
                {images.map((img, idx) => (
                  <div
                    key={`${img.name}-${idx}`}
                    className="group relative h-16 w-20 overflow-hidden rounded-md border border-border bg-muted"
                  >
                    <img
                      src={img.dataUrl}
                      alt={img.name || t("chat.image.attachmentAlt")}
                      className="h-full w-full object-cover"
                    />
                    <button
                      type="button"
                      aria-label={t("chat.composer.removeImage", {
                        name: img.name || idx + 1,
                      })}
                      className="absolute top-1 right-1 inline-flex size-5 items-center justify-center rounded-sm bg-background/90 text-muted-foreground opacity-0 shadow-sm transition-opacity group-hover:opacity-100 focus-visible:opacity-100"
                      onClick={() => {
                        setImages((prev) => prev.filter((_, i) => i !== idx));
                        setImageError("");
                      }}
                    >
                      <X className="size-3" aria-hidden="true" />
                    </button>
                  </div>
                ))}
              </div>
            ) : null}
            <AIChatInput
              ref={inputRef}
              editorRef={editorRef}
              onSubmit={handleSend}
              onEmptyChange={setIsEmpty}
              onCommandModeChange={(active) => {
                setCommandMode(active);
                onCommandModeChange?.(active);
              }}
              onCommandSubmit={onRunCommand}
              localCommandHistoryScope={localCommandHistoryScope}
              sendOnEnter
              userMessageHistory={userMessageHistory}
              placeholder={resolvedPlaceholder}
              autoFocus={autoFocusOnMount}
              disabled={disabled}
              backendType={backendType}
              mentionSources={mentionSources}
              skillCommands={skillCommands}
              onSlashSelect={(cmd, exec) => {
                // literal_text 由 AIChatInput 内部直接填回编辑器(不自动发送),
                // 这里只接 rpc 类命令转交给父组件。
                if (exec.kind === "rpc") onSlashRpc?.(cmd, exec);
              }}
            />
            {imageError ? (
              <div className="text-meta text-status-error" role="alert">
                {imageError}
              </div>
            ) : null}
            {/* 底栏恒为单行:靠 @container 分档隐藏装饰来适配窄宽,绝不允许内部文字
              折行把行高顶高(Hard invariant 1),也绝不允许横向溢出把发送按钮裁出
              可视区(Hard invariant 2)。
              溢出优先级:按钮与两个 Pill 保持 shrink-0 不参与收缩,两个计量器是唯一
              带 min-w-0 的让位者 —— 断点万一估窄,多出的宽度只吃掉计量器。 */}
            <div className="flex flex-nowrap items-center gap-2">
              {!editing && supportsImageInput ? (
                <>
                  <input
                    ref={fileInputRef}
                    type="file"
                    accept={CHAT_IMAGE_ACCEPT}
                    multiple
                    className="hidden"
                    onChange={(event) =>
                      void handleImageFiles(event.target.files)
                    }
                  />
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("chat.composer.addImage")}
                    title={t("chat.composer.addImage")}
                    disabled={disabled || images.length >= MAX_CHAT_IMAGE_COUNT}
                    onClick={() => fileInputRef.current?.click()}
                  >
                    <ImagePlus data-icon="only" aria-hidden="true" />
                  </Button>
                </>
              ) : null}
              {/* 快捷键提示是一次性教学文案,空间不足时第一个让位。 */}
              <span className="shrink-0 font-mono text-meta leading-none whitespace-nowrap text-subtle-foreground @max-[1000px]/composer:hidden">
                {editing
                  ? t("chat.composer.shortcuts.edit")
                  : t("chat.composer.shortcuts.send")}
              </span>
              {!editing && (permissionModeSlot || modelSlot) ? (
                <div className="flex shrink-0 items-center gap-1">
                  {permissionModeSlot}
                  {modelSlot}
                </div>
              ) : null}
              <div className="min-w-0 flex-1" />
              {!editing && !commandMode ? (
                <QuotaMeter data={quotaUsage} deviceLabel={quotaDeviceLabel} />
              ) : null}
              {contextUsage &&
              contextUsage.max > 0 &&
              !editing &&
              !commandMode ? (
                <ContextMeter used={contextUsage.used} max={contextUsage.max} />
              ) : null}
              {!editing && commandMode ? (
                <Button
                  type="submit"
                  size="icon-sm"
                  aria-label={t("chat.composer.command.run")}
                  title={t("chat.composer.command.run")}
                >
                  <SquareTerminal data-icon="only" aria-hidden="true" />
                </Button>
              ) : editing ? (
                <Button
                  type="submit"
                  disabled={isEmpty}
                  size="xs"
                  aria-label={t("chat.composer.saveEdit")}
                >
                  <Check data-icon="inline-start" aria-hidden="true" />
                  {t("common.save")}
                </Button>
              ) : (
                <Button
                  type="submit"
                  disabled={
                    disabled || sending || (isEmpty && images.length === 0)
                  }
                  size="icon-sm"
                  aria-label={
                    sending ? t("chatPanel.sending") : t("chat.composer.send")
                  }
                  title={t("chat.composer.sendTitle")}
                >
                  {sending ? (
                    <LoaderCircle
                      data-icon="only"
                      className="animate-spin"
                      aria-hidden="true"
                    />
                  ) : (
                    <SendHorizontal data-icon="only" aria-hidden="true" />
                  )}
                </Button>
              )}
            </div>
          </div>
        </div>
      </form>
    );
  },
);

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
    messages,
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
  },
  ref,
) {
  // lastAssistantId:生成指示器(三个点)的宿主。只有当整个序列的「最后一条」就是
  // assistant 时才挂 —— 正常 stream 流程下 doSend 同时插入 user + assistant 占位,
  // 所以末尾一定是 assistant;末尾是 user(异常态或还没插占位)时不该在更早的
  // assistant 上挂指示器误导用户,所以扫到的第一条不是 assistant 就返回 undefined。
  // 只承载 notice 的旁白行在这里透明:轮中切换供应商会把一条 notice 消息追加在在跑的
  // assistant 之后(pill 切完立刻 reloadSession),它若被当成末条 assistant,三个点就
  // 跳到旁白行上、在跑的那条看着像已经停了。
  const lastAssistantId = React.useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i];
      if (isNoticeOnlyMessage(m)) continue;
      return m.role === "assistant" ? m.id : undefined;
    }
    return undefined;
  }, [messages]);

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
  // 判定:assistant 轮且其在**完整 messages**里紧邻的前一条不是 user —— 正常轮是
  // user→assistant、auto-continue / steer 也是 user→assistant,只有自主续轮是
  // assistant→assistant(无 user 行)。用完整 messages(而非 displayMessages)算,
  // 避免 compact 折叠把首条 assistant 误判成自主轮。会话首条永不算(见下方 prevRole
  // 的 undefined 初值)。
  //
  // 只含 notice 块的消息(供应商切换/回退提示,规格决策 3)在这条判定里透明:它自己
  // 永远不进 ids(单独 continue,连角色都不看),且在寻找「紧邻前一条」时被跳过(不
  // 推进 prevRole)—— 否则它会插进两条真实 assistant 消息之间把回退提示误判成自主
  // 续轮,也可能反过来垫在 assistant 与后续真实自主续轮之间把该有的判定拆断。
  const autonomousIds = React.useMemo(() => {
    const ids = new Set<number>();
    let prevRole: string | undefined;
    for (const m of messages) {
      if (isNoticeOnlyMessage(m)) continue;
      if (
        prevRole !== undefined &&
        m.role === "assistant" &&
        prevRole !== "user"
      ) {
        ids.add(m.id);
      }
      prevRole = m.role;
    }
    return ids;
  }, [messages]);

  // useEvent 模式：把 onRerun/onEdit/onPlanActionStarted 包成稳定引用,让行组件的
  // React.memo / TranscriptRenderContext 不会被 ChatPanel 传入的 inline lambda 击穿。
  // 父侧每次重渲都换新函数,但 ref 内部更新后稳定代理捕获最新值,语义不变。
  const onRerunRef = React.useRef(onRerun);
  const onContinueRef = React.useRef(onContinue);
  const onEditRef = React.useRef(onEdit);
  const onPlanActionStartedRef = React.useRef(onPlanActionStarted);
  const onStopSubagentRef = React.useRef(onStopSubagent);
  const onStopLocalCommandRef = React.useRef(onStopLocalCommand);
  React.useEffect(() => {
    onRerunRef.current = onRerun;
    onContinueRef.current = onContinue;
    onEditRef.current = onEdit;
    onPlanActionStartedRef.current = onPlanActionStarted;
    onStopSubagentRef.current = onStopSubagent;
    onStopLocalCommandRef.current = onStopLocalCommand;
  });
  const stableOnRerun = React.useCallback((id: number) => {
    onRerunRef.current?.(id);
  }, []);
  const stableOnContinue = React.useCallback((id: number) => {
    onContinueRef.current?.(id);
  }, []);
  const stableOnEdit = React.useCallback((id: number) => {
    onEditRef.current?.(id);
  }, []);
  const stableOnPlanActionStarted = React.useCallback(
    (stream: PlanActionStream, userText: string) => {
      onPlanActionStartedRef.current?.(stream, userText);
    },
    [],
  );
  const stableOnStopSubagent = React.useCallback((toolUseId: string) => {
    onStopSubagentRef.current?.(toolUseId);
  }, []);
  const stableOnStopLocalCommand = React.useCallback((terminalId: string) => {
    return onStopLocalCommandRef.current?.(terminalId);
  }, []);
  const hasStopLocalCommand = onStopLocalCommand !== undefined;

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
        />
      );
    },
    [lastAssistantId, liveByMessageId, liveCompacting, reconnecting, streaming],
  );

  // eslint-disable-next-line react-hooks/incompatible-library -- TanStack Virtual intentionally owns mutable measurement callbacks.
  const virtualizer = useVirtualizer({
    count: rows.length,
    estimateSize: (index) =>
      // estimateRowSize(内容高度,按 132→148 等同源校准比例缩放)之上,再按
      // isLastRowOfMessage 补上 rowWrapperPad 的间距增量(消息末行 pb-7=28px /
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
      }
    },
    [firstRowIndexByMessageId, folding, messages, virtualizer],
  );

  React.useEffect(() => {
    if (pendingScrollMessageId == null) return;
    const index = firstRowIndexByMessageId.get(pendingScrollMessageId);
    if (index == null) return;
    virtualizer.scrollToIndex(index, { align: "start" });
    setPendingScrollMessageId(null);
  }, [firstRowIndexByMessageId, pendingScrollMessageId, virtualizer]);

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
  const rowWrapperPad = (index: number): string =>
    isLastRowOfMessage(rows, index) ? "pb-7" : "pb-2.5";

  return (
    <TooltipProvider delayDuration={200}>
      <TranscriptUIStateProvider>
        <TranscriptRenderContext.Provider value={renderCtx}>
          {/* 不再加 max-w-4xl —— 内部 ChatMessage 已经 cap 在 max-w-measure,
          这里再叠一层外层 max-w 没有任何收紧效果,只会留出 phantom 空白。 */}
          <div className={shouldVirtualize ? "min-h-full" : "flex flex-col"}>
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
                            rowWrapperPad(virtualItem.index),
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
                  className={rowWrapperPad(index)}
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
