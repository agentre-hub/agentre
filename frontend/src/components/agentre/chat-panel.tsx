import * as React from "react";
import {
  ArrowDown,
  ArrowRight,
  Folder,
  MoreHorizontal,
  PanelRight,
  PanelRightClose,
  Square,
  TriangleAlert,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  buildTranscriptRows,
  loadTranscriptScrollState,
  nextAutoFollow,
  saveTranscriptScrollState,
  copyTextWithToast,
  useTerminalTransport,
  type PlanActionStream,
  type TerminalSubscriber,
  type TerminalUnsubscribe,
} from "@agentre-ai/agentre-ui";
import { Badge } from "@/components/ui/badge";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { useCCUsage } from "@/hooks/use-cc-usage";
import { useChatSession } from "@/hooks/use-chat-session";
import { useChatStream, type ChatStreamEvent } from "@/hooks/use-chat-stream";
import { useProjectTree } from "@/hooks/use-project-tree";
import { useVisibleMessageId } from "@/hooks/use-visible-message-id";
import i18n from "@/i18n";
import { reasonToDisplayStatus } from "@/lib/attention-display";
import { splitErrorDetail } from "@/lib/error-detail";
import { isNoticeOnlyMessage } from "@/lib/notice-message";
import {
  findProjectColorToken,
  findProjectPath,
  projectChain,
} from "@/lib/project-chain";
import { relativeTime } from "@/lib/relative-time";
import { cn } from "@/lib/utils";
import { useSessionAttention } from "@/stores/attention-store";
import { useClearedBackgroundTasksStore } from "@/stores/cleared-background-tasks-store";
import {
  sessionStreamMap,
  streamForMessage,
  useChatStreamsStore,
  type ChatBlockData,
  type LiveStream,
} from "@/stores/chat-streams-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useQueuedMessagesStore } from "@/stores/queued-messages-store";
import { useSessionConnectionState } from "@/stores/session-conn-store";
import { useSessionReadStore } from "@/stores/session-read-store";
import { useSessionStatusStore } from "@/stores/session-status-store";
import {
  localCommandRuntimeStore,
  type LocalCommandRuntimeController,
} from "@/stores/local-command-runtime-store";

import { useBackendCapabilities } from "./capability/use-backend-capabilities";
import { useSessionCapabilities } from "./capability/use-session-capabilities";
import {
  ChatComposer,
  ChatTranscript,
  type ChatComposerHandle,
  type ChatComposerSubmit,
  type ChatTranscriptHandle,
  type ChatImageAttachment,
  type TranscriptLiveContent,
} from "./chat";
import type { LocalCommandHistoryScope } from "./chat-input";
import { ChatContextSidebar } from "./chat-context-sidebar";
import { FilePreviewPanel } from "./file-preview/file-preview-panel";
import {
  clearCatchUp,
  registerTranscriptRowCounter,
  useCatchUpSummary,
  type CatchUpSummary,
} from "./chat-panel-catchup-state";
import { computeComposerContextUsage } from "./chat-panel-context-usage";
import { blockReasonToCta, navigateToTarget } from "./not-chattable";
import { PermissionModePill, usePermissionMode } from "./permission-mode";
import { ProviderPill, useProviderPill } from "./model-pill";
import {
  NewSessionExecTargetLine,
  SessionOfflineBanner,
} from "./session-exec-target";
import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import { AgentAvatar, DeviceTag, StatusDot } from "./primitives";
import { QueuedMessagesBar } from "./queued-messages-bar";
import { BackgroundTasksChip } from "./background-tasks/background-tasks-chip";
import { deriveBackgroundTasks } from "./background-tasks/derive";
import {
  flipSubagentStatusInMessages,
  mergeSubagentMetaInMessages,
} from "./background-tasks/flip-subagent-status";
import { deriveTaskProgress } from "./task-progress/derive";
import { TaskProgressBar } from "./task-progress/task-progress-bar";
import type { AgentColor, AgentStatus } from "./types";
import { agentTextColorClassName, statusConfig } from "./types";

import {
  CancelQueuedChatMessage,
  ClearChatGoal,
  CompactChatSession,
  DeleteChatSession,
  EditChatMessage,
  EnqueueChatMessage,
  GetChatGoal,
  GetChatLaunchCommand,
  MarkChatSessionRead,
  PeerRunFresh,
  EnsureChatSession,
  RegenerateChatMessage,
  RenameChatSession,
  ResolveLocalCommandScope,
  SendChatMessage,
  SetChatGoal,
  StartChatGoal,
  StopBackgroundTask,
  StopChatMessage,
  TerminalClose,
  TerminalRunCommand,
} from "../../../wailsjs/go/app/App";
import { chat_svc } from "../../../wailsjs/go/models";
import { useLocalCommandsStore } from "@/stores/local-commands-store";
import { makeStreamDecoder } from "./local-command/decode";

type SvcChatMessage = chat_svc.ChatMessage;
type ChatAgentItem = chat_svc.ChatAgentItem;

type TranscriptScrollSnapshot = {
  atBottom: boolean;
  scrollTop: number;
  // 非贴底时记的锚点:视口顶部那一行的消息 id + 行顶边在视口顶上方的 px +
  // 行身份(行级虚拟化下长消息拆多行,rowKey 才能钉回同一行)。
  // 见 computeTopVisibleAnchor / ChatTranscriptHandle.scrollToAnchor。
  anchorId?: number;
  anchorOffset?: number;
  anchorRowKey?: string;
};

type ScrollMetrics = {
  clientHeight: number;
  maxScrollTop: number;
  scrollHeight: number;
  scrollTop: number;
};

type CollapsedScrollRestoreGuard = TranscriptScrollSnapshot & {
  key: string;
  minMaxScrollTop: number;
  until: number;
};

const TRANSCRIPT_BOTTOM_THRESHOLD = 32;
export const COLLAPSED_RESTORE_GUARD_MS = 3_000;

function readScrollMetrics(el: HTMLElement): ScrollMetrics {
  const { clientHeight, scrollHeight, scrollTop } = el;
  return {
    clientHeight,
    maxScrollTop: Math.max(0, scrollHeight - clientHeight),
    scrollHeight,
    scrollTop,
  };
}

function isTranscriptAtBottom(metrics: ScrollMetrics): boolean {
  return (
    metrics.scrollHeight - metrics.scrollTop - metrics.clientHeight <=
    TRANSCRIPT_BOTTOM_THRESHOLD
  );
}

function isCollapsedBelowGuard(
  metrics: ScrollMetrics,
  guard: CollapsedScrollRestoreGuard,
): boolean {
  return (
    Date.now() <= guard.until && metrics.maxScrollTop < guard.minMaxScrollTop
  );
}

// computeTopVisibleAnchor 找滚动容器内"视口顶部那条消息"——即第一条底边已越过视口顶
// 的 [data-message-id] 行(虚拟列表里 DOM 顺序≈消息顺序,故首条命中即视口顶那条),
// 返回其 id 与顶边在视口顶上方的 px。非贴底保存时记下它,路由重挂后据此 scrollToAnchor
// 钉回该消息,避免仅凭像素 scrollTop 在"整列还是 estimate 高度"时落到错消息的漂移。
// 找不到(无消息行 / 容器未布局)返回 null,调用方退回纯像素快照。
export function computeTopVisibleAnchor(el: HTMLElement): {
  anchorId: number;
  anchorOffset: number;
  anchorRowKey?: string;
} | null {
  const containerTop = el.getBoundingClientRect().top;
  const rows = el.querySelectorAll<HTMLElement>("[data-message-id]");
  for (const row of rows) {
    const rect = row.getBoundingClientRect();
    if (rect.bottom <= containerTop) continue; // 完全在视口上方,跳过
    const id = Number(row.getAttribute("data-message-id"));
    if (!Number.isFinite(id)) continue;
    // 行级虚拟化下一条消息拆成多行,offset 相对的是「这一行」的顶边 —— 把行身份
    // (data-row-key)一并记下,恢复端才能钉回同一行而不是塌到消息首行。
    const rowKey = row.getAttribute("data-row-key");
    return {
      anchorId: id,
      anchorOffset: Math.max(0, containerTop - rect.top),
      ...(rowKey ? { anchorRowKey: rowKey } : {}),
    };
  }
  return null;
}

// ─── Optimistic message helpers ─────────────────────────────────────────────

function textOfChatMessage(m: SvcChatMessage): string {
  for (const b of m.blocks ?? []) {
    if ((b as { type?: string }).type === "text") {
      return (b as { text?: string }).text ?? "";
    }
  }
  return "";
}

// isChatSteerNoActiveError 用 i18n 文案前缀匹配。Wails 把 service 端的
// i18n.NewError 透传成普通 Error，没有结构化 code，只能按字串识别。
function isChatSteerNoActiveError(msg: string): boolean {
  return (
    msg.includes(
      i18n.t("chatPanel.errors.noActiveConversation", { lng: "zh-CN" }),
    ) ||
    msg.includes(i18n.t("chatPanel.errors.noActiveConversation", { lng: "en" }))
  );
}

// isChatStopNoActiveError 同上：后端 ChatStopNoActive 错误码的中英文文案。
// Stop 与 turn 自然完成发生 race 时（用户点击之后、后端已自清 activeCancels）
// 会返这条；属于无害的「太晚了」，UI 不弹错。
function isChatStopNoActiveError(msg: string): boolean {
  return (
    msg.includes(
      i18n.t("chatPanel.errors.noActiveTurnToStop", { lng: "zh-CN" }),
    ) ||
    msg.includes(i18n.t("chatPanel.errors.noActiveTurnToStop", { lng: "en" }))
  );
}

const TERMINAL_NOT_OPEN_ERROR = "terminal not open";

function isTerminalNotOpenError(error: unknown): boolean {
  return (
    error === TERMINAL_NOT_OPEN_ERROR ||
    (error instanceof Error && error.message === TERMINAL_NOT_OPEN_ERROR)
  );
}

function isExactCompactCommand(text: string): boolean {
  return text.trim() === "/compact";
}

function isExactNewCommand(text: string): boolean {
  return text.trim() === "/new";
}

type GoalCommand =
  | { kind: "get" }
  | { kind: "clear" }
  | { kind: "set"; objective: string }
  | { kind: "status"; status: "active" | "paused" | "complete" };

function parseGoalCommand(text: string): GoalCommand | null {
  const trimmed = text.trim();
  if (trimmed === "/goal") return { kind: "get" };
  if (!trimmed.startsWith("/goal ")) return null;
  const arg = trimmed.slice("/goal ".length).trim();
  if (!arg) return { kind: "get" };
  switch (arg) {
    case "clear":
      return { kind: "clear" };
    case "pause":
      return { kind: "status", status: "paused" };
    case "resume":
      return { kind: "status", status: "active" };
    case "complete":
      return { kind: "status", status: "complete" };
    default:
      return { kind: "set", objective: arg };
  }
}

const EMPTY_CLEARED: string[] = [];

function optimisticUser(
  id: number,
  sid: number,
  text: string,
  images: ChatImageAttachment[] = [],
): SvcChatMessage {
  const blocks: Array<Record<string, unknown>> = [];
  if (text) blocks.push({ type: "text", text });
  for (const image of images) {
    blocks.push({
      type: "image",
      image: {
        dataUrl: image.dataUrl,
        mediaType: image.mediaType,
        name: image.name,
      },
    });
  }
  return {
    id,
    sessionId: sid,
    role: "user",
    blocks,
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: 0,
    createtime: Date.now(),
  } as unknown as SvcChatMessage;
}

// markSessionStatus 乐观补一刀 session 状态 —— 后端在这两条路上都没有
// session_status 事件可跟, 不补的话 tab / toolbar / sidebar 读
// session-status-store 会一直停在上一状态:
//   - "running": Send / Regenerate / Edit 成功返回后。后端落库已经是 running,
//     但 turn 起手不 emit session_status, 不补就停在 idle。
//   - "error": 自主续轮落库失败时后端只把 error 落了库、经会话级流推一条 error
//     事件(那条轮压根没有 per-turn 流), 不补就停在 running 空转。
// permissionMode 取 store 当前值, 避免覆盖刚 set 的 plan/default 等。
function markSessionStatus(sessionId: number, agentStatus: AgentStatus): void {
  if (!sessionId) return;
  const prev = useSessionStatusStore.getState().statuses.get(sessionId);
  useSessionStatusStore.getState().upsert(sessionId, {
    agentStatus,
    needsAttention: false,
    permissionMode: prev?.permissionMode,
  });
}

function markSessionRunning(sessionId: number): void {
  markSessionStatus(sessionId, "running");
}

function optimisticAssistantPlaceholder(
  id: number,
  sid: number,
): SvcChatMessage {
  return {
    id,
    sessionId: sid,
    role: "assistant",
    blocks: [],
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: 0,
    createtime: Date.now(),
  } as unknown as SvcChatMessage;
}

function upsertMessage(
  messages: SvcChatMessage[],
  next: SvcChatMessage,
): SvcChatMessage[] {
  const updated = [...messages];
  const idx = updated.findIndex((m) => m.id === next.id);
  if (idx >= 0) updated[idx] = next;
  else updated.push(next);
  return updated;
}

function applySteerConsumed(
  messages: SvcChatMessage[],
  event: ChatStreamEvent,
): SvcChatMessage[] {
  const additions = [
    ...(event.userMessages ?? []),
    ...(event.assistantMessage ? [event.assistantMessage] : []),
  ];
  const additionIDs = new Set(additions.map((m) => m.id));
  const next = messages.filter((m) => !additionIDs.has(m.id));

  let anchorIdx = -1;
  if (event.previousAssistantMessage) {
    anchorIdx = next.findIndex(
      (m) => m.id === event.previousAssistantMessage!.id,
    );
    if (anchorIdx >= 0) {
      next[anchorIdx] = event.previousAssistantMessage;
    } else {
      next.push(event.previousAssistantMessage);
      anchorIdx = next.length - 1;
    }
  }

  const insertAt = anchorIdx >= 0 ? anchorIdx + 1 : next.length;
  next.splice(insertAt, 0, ...additions);
  return next;
}

function applyStreamError(
  messages: SvcChatMessage[],
  message?: SvcChatMessage,
  error?: string,
): SvcChatMessage[] {
  if (message) return upsertMessage(messages, message);
  if (!error) return messages;

  // 末条**真实** assistant:供应商切换 notice 的旁白行跳过(与 use-chat-session /
  // ChatTranscript / 后端 lastTurnAssistantIndex 同一口径)。轮中切换供应商会把它排在
  // 在跑的那条之后,errorText 落到旁白行 = 出错的那一轮看着没出错、旁白行反而红了。
  let idx = -1;
  for (let i = messages.length - 1; i >= 0; i--) {
    if (isNoticeOnlyMessage(messages[i])) continue;
    if (messages[i].role === "assistant") {
      idx = i;
      break;
    }
  }
  if (idx < 0) return messages;

  const updated = [...messages];
  updated[idx] = { ...updated[idx], errorText: error } as SvcChatMessage;
  return updated;
}

// liveContentByMessageId 把该会话此刻在流的内容摊成「assistant 消息 id → 流式内容」。
// 渲染路径与补齐行数快照路径共用一份 —— 后者拿的是 store 的即时快照(见
// registerTranscriptRowCounter 处的注释),两处若各拼各的,数出来的行与画出来的行
// 就会在某个细节上分家。
function liveContentByMessageId(
  sessionStreams: ReadonlyMap<number, LiveStream> | null,
): Map<number, TranscriptLiveContent> {
  const out = new Map<number, TranscriptLiveContent>();
  if (!sessionStreams) return out;
  for (const s of sessionStreams.values()) {
    out.set(s.assistantMessageId, {
      liveTail: s.liveDelta,
      liveThinking: s.liveThinking,
      liveThinkingStartedAt: s.streamStartedAt,
      liveBlocks: s.liveBlocks,
      liveRetry: s.liveRetry,
    });
  }
  return out;
}

// EMPTY_AUTONOMOUS_IDS:行数快照不关心「哪条消息是自主续轮」——那只影响首行要不要
// 挂 banner,不改行数。渲染路径自己会算真值。
const EMPTY_AUTONOMOUS_IDS: ReadonlySet<number> = new Set<number>();

// TranscriptJumpControl 是转录区底部浮出的那枚控件。
//
// 没有未看的补齐时,它就是原来的「回到底部」圆钮;补齐把内容悄悄堆高之后,它长成
// 一枚带文字的药丸,写明新增了多少条 —— 用户的滚动位置没被夺走,但他得看得见
// 下面多了东西。补齐里还有没回答的待决策时再补一段「N 项待处理」:待决策一旦
// 埋进上百条补齐内容中间,不写在这里就等于没人知道。
// 那段必须是文字:状态色点只是修饰,颜色不能是信息的唯一载体(docs/design.md 无障碍)。
//
// newRows 为 0 的那一支同样浮出控件:补齐把上千条 delta 全追加进了还在流的那一行,
// 行数没变但内容确实多了(R14 只要求「补齐产生了新内容」)。此时报不出条数,就只说
// 有新内容 —— 与其编一个数字,不如少说一句。
function TranscriptJumpControl({
  catchUp,
  onJump,
}: {
  catchUp: CatchUpSummary | null;
  onJump: () => void;
}): React.ReactElement {
  const { t } = useTranslation();
  const floating =
    "sticky bottom-4 z-20 ml-auto flex rounded-full bg-background shadow-md hover:shadow-lg dark:bg-background animate-in fade-in slide-in-from-bottom-1 duration-200 ease-out motion-reduce:animate-none";

  if (!catchUp) {
    return (
      <Button
        type="button"
        data-testid="back-to-bottom-button"
        variant="outline"
        size="icon-sm"
        aria-label={t("chatPanel.scroll.backToBottom")}
        title={t("chatPanel.scroll.backToBottom")}
        onClick={onJump}
        className={floating}
      >
        <ArrowDown data-icon="only" aria-hidden="true" />
      </Button>
    );
  }

  // 不加 aria-label:按钮的可访问名就是这些文字本身,条数与待处理项数因此一并被读出。
  return (
    <Button
      type="button"
      data-testid="jump-to-latest-button"
      variant="outline"
      size="sm"
      title={t("chatPanel.scroll.jumpToLatest")}
      onClick={onJump}
      className={cn(floating, "w-fit")}
    >
      <ArrowDown aria-hidden="true" />
      <span>
        {catchUp.newRows > 0
          ? t("chatPanel.scroll.caughtUpCount", { count: catchUp.newRows })
          : t("chatPanel.scroll.caughtUpSome")}
      </span>
      {catchUp.pendingDecisions > 0 ? (
        <span
          data-testid="jump-to-latest-pending"
          className="flex items-center gap-1 text-status-waiting"
        >
          <span
            aria-hidden="true"
            className="size-1.5 rounded-full bg-status-waiting"
          />
          {t("chatPanel.scroll.pendingCount", {
            count: catchUp.pendingDecisions,
          })}
        </span>
      ) : null}
    </Button>
  );
}

// ─── ChatPanel ───────────────────────────────────────────────────────────────

type NewSessionContext = {
  /** 新建会话挂到此项目。0 = 自由会话。*/
  projectId?: number;
};

type ChatPanelProps = {
  /** 当前要渲染的会话；0 = 新建会话模式（需要配合 newSessionAgent）或空态。*/
  sessionId: number;
  /** sessionId=0 时若提供，则渲染新会话占位 + Composer，首发 RPC 后建立新会话。*/
  newSessionAgent?: ChatAgentItem | null;
  /** 新建会话时附加的项目上下文。仅 sessionId=0 路径生效。*/
  newSessionContext?: NewSessionContext;
  /** 新会话首发成功后回调，父级用来同步 selectedSessionId/agentId。*/
  onSessionCreated?: (sessionId: number, agentId: number) => void;
  /** 新建会话派到一台远端桌面端（R18）成功后回调，父级关掉新建 Tab 并打开 Peer Tab。*/
  onPeerSessionCreated?: (peer: {
    fingerprint: string;
    sessionId: number;
    title: string;
    deviceName: string;
  }) => void;
  /** 会话被删除或加载失败时回调，父级清掉选中状态。*/
  onSessionDeleted?: () => void;
  /** 任何会让父级列表（Agent / 项目）需要刷新的 RPC 成功后调一次。*/
  onSidebarShouldReload?: () => void;
  /** 标题上方的小字 meta，比如 "📂 Agentre / backend / sess-142"。*/
  headerTopline?: React.ReactNode;
  /** sessionId=0 且未提供 newSessionAgent 时的空态。*/
  emptyState?: React.ReactNode;
  /** 该 ChatPanel 当前是否是可见的 tab；用于在切回时补一次"跟随到底"。默认 true。*/
  active?: boolean;
  /** 当前 mounted tab 的稳定 id。用于跨路由 remount 恢复滚动；关闭 tab 后新 id 不复用。*/
  scrollStateKey?: string;
};

/** 不可对话 Agent 新会话 tab 的内联引导块（组 4B）：复用任务 2 的徽标 / 原因 / CTA
 *  文案与跳转语义，渲染在输入框上方；按钮走 navigateToTarget 与引导弹窗一致。 */
function NewSessionChatGuard({ agent }: { agent: ChatAgentItem }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const cta = blockReasonToCta(agent.blockReason ?? "");
  const reasonKey = cta.copyKey;
  return (
    <div
      className="mx-5 mb-2 flex flex-col items-start gap-2 rounded-lg border border-status-waiting/40 bg-status-waiting-bg px-4 py-3"
      data-testid="new-session-guard"
      role="alert"
      aria-live="polite"
    >
      <div className="flex flex-wrap items-center gap-2 text-sm font-semibold">
        <Badge
          variant="outline"
          className="border-status-waiting/40 bg-background px-1.5 py-0 text-2xs text-foreground"
        >
          {agent.blockReason === "no-backend"
            ? t("agentList.backendNotConfigured")
            : t("agentList.notConfigured")}
        </Badge>
        {t("chatPanel.newSession.guard.title", { name: agent.name })}
      </div>
      <div className="text-xs leading-relaxed text-muted-foreground">
        {t(`${reasonKey}.description`, { name: agent.name })}
      </div>
      <div className="flex items-center gap-4">
        <Button
          type="button"
          size="sm"
          onClick={() =>
            navigateToTarget(navigate, cta.primaryTarget, agent.id)
          }
        >
          {t(cta.primaryLabel)}
        </Button>
        <button
          type="button"
          className="inline-flex items-center gap-1 text-xs font-medium text-primary-text hover:underline"
          onClick={() =>
            navigateToTarget(navigate, "settings:agent-backend", agent.id)
          }
        >
          {t(cta.secondaryLabel)}
          <ArrowRight className="size-3" aria-hidden="true" />
        </button>
      </div>
    </div>
  );
}

function ChatPanel({
  sessionId,
  newSessionAgent,
  newSessionContext,
  onSessionCreated,
  onPeerSessionCreated,
  onSessionDeleted,
  onSidebarShouldReload,
  headerTopline,
  emptyState,
  active = true,
  scrollStateKey,
}: ChatPanelProps) {
  const { t } = useTranslation();
  // 本地命令卡片跑的是一条真 PTY,订阅走终端传输端口(宿主在 App 根挂 Provider)。
  // 它自持订阅表并扇出,所以同一条 PTY 可以同时被终端标签页盯着,谁退订都不连坐。
  const transport = useTerminalTransport();
  // 流式状态(streams / queuedBySession / liveBlocks ...)全部托管在跨路由长存的
  // zustand store 里。ChatPanel 只做「读 + 派发」,不再持有状态副本,这样切到 /projects
  // 等其它路由再切回来时,store 里累积的 liveDelta / liveBlocks / queued 都能直接还原。
  const openStream = useChatStreamsStore((s) => s.openStream);
  // 本会话的全部在流(用户轮 / 自主续轮 / 后台 subagent 活动轮),按 assistantMessageId
  // 索引。引用只在本会话有流增删 / 内容变动时才变,别的会话在流不会触发本 panel 重渲染。
  const sessionStreams = useChatStreamsStore((s) =>
    sessionId ? (s.streams.get(sessionId) ?? null) : null,
  );
  // 主流 = 最近开的那条,供会话级读数(进度条 / typing indicator / 停止按钮)。
  const currentStream = React.useMemo(() => {
    if (!sessionStreams || sessionStreams.size === 0) return null;
    let best: LiveStream | null = null;
    for (const s of sessionStreams.values()) {
      if (!best || s.streamStartedAt >= best.streamStartedAt) best = s;
    }
    return best;
  }, [sessionStreams]);
  const currentQueued = useQueuedMessagesStore(
    (s) => s.queuedBySession.get(sessionId) ?? null,
  );
  // 回合收尾未消费被暂存的排队条目(最多一条)。只有它与当前 session 匹配时才传给
  // QueuedMessagesBar —— 别 tab 的丢弃横幅不该贴在本 tab 的 composer 上。
  const droppedQueue = useQueuedMessagesStore((s) => s.dropped);
  // doneTick / lastDoneEvent 从 session-status-store 读取。
  // 每次 turn 结束（done/error/aborted/closed/steer_consumed）bumpDone 自增 doneTick，
  // ChatPanel 的 lastSeenDoneTickRef effect 据此触发 reload + 副作用。
  const liveStatus = useSessionStatusStore((s) =>
    sessionId ? (s.statuses.get(sessionId) ?? null) : null,
  );
  const doneTick = liveStatus?.doneTick ?? 0;
  const lastDoneEvent = liveStatus?.lastDoneEvent ?? null;

  const [pendingRegenId, setPendingRegenId] = React.useState<number | null>(
    null,
  );
  const [pendingDeleteId, setPendingDeleteId] = React.useState<number | null>(
    null,
  );
  // pendingRename 取代旧 window.prompt：null = 未弹窗；非空 = 显示 Dialog + Input。
  // draft 跟随用户在 Input 里的输入；提交时调 RenameChatSession，关闭时清空。
  const [pendingRename, setPendingRename] = React.useState<{
    id: number;
    draft: string;
  } | null>(null);
  // notice 取代旧 window.alert：所有 RPC 失败 / 重要提示统一渲染成 composer 上方的内联 Alert。
  // kind=info 用于成功后的提醒（带 token 复制等），error 用于失败；用户点 × 关闭即可。
  const [notice, setNotice] = React.useState<{
    kind: "error" | "info";
    text: string;
    detail?: string;
    // 发送失败草稿保留后的补救动作:Retry 重发同一条消息,Discard 清掉恢复的草稿。
    actions?: {
      retry: () => void;
      discard: () => void;
    };
  } | null>(null);
  // 「编辑用户消息」：点编辑后把目标消息文本直接载入 Composer。带 sessionId 在切换会话
  // 时自动失效，免得弄个 useEffect 在会话切换时手动 setState 一遍。
  const [editingMessage, setEditingMessage] = React.useState<{
    sessionId: number;
    messageId: number;
    text: string;
  } | null>(null);
  const activeEditing =
    editingMessage && editingMessage.sessionId === sessionId
      ? editingMessage
      : null;
  // R15a 手动指定执行目标：只在空会话态（showNewSessionPrompt）生效的瞬态选择，
  // 随首发 Send 透传给后端（SendRequest.ExecTargetOverride，与 ModelOverride 同一
  // 条规则）。每个 tab 独立的 ChatPanel 实例天然随 tab 切换重新挂载，不需要额外
  // 按 agent/session 手动重置。
  const [execTargetOverride, setExecTargetOverride] = React.useState<
    number | null
  >(null);
  // 改选后生效档的 backend type（由 NewSessionExecTargetLine 报上来）。新建会话且
  // 手动指定了执行目标时，permission mode 的 allowed 集合/默认值是 runtime 维度的，
  // 必须跟随实际后端而不是 agent 主后端 —— 否则从 claudecode 主后端改到 codex 后端
  // 后，pill 与 Send payload 还会带上 claudecode 才合法的 mode。
  const [overrideBackendType, setOverrideBackendType] = React.useState<
    string | null
  >(null);
  // 改选后实际生效档的目标种类与设备身份（R18）：空会话态首发时据此决定走本地 Send
  // 还是 peer RunFresh（桌面派发）。由 NewSessionExecTargetLine 报上来。
  const [effectiveTarget, setEffectiveTarget] = React.useState<{
    kind: "local" | "desktop" | "daemon";
    deviceId: string;
    deviceName: string;
    backendType: string;
    llmProviderKey: string;
    llmModelKey: string;
  } | null>(null);

  const {
    session,
    messages,
    setMessages,
    loading: sessionLoading,
    error: sessionError,
    reload: reloadSession,
  } = useChatSession(sessionId);
  // ChatComposer 命令式句柄:doSend 失败时用它恢复草稿（restoreDraft）/ 丢弃草稿
  // （clearDraft）。ChatComposer 内部已清空输入框,不恢复用户刚打的内容会永久丢失。
  const composerRef = React.useRef<ChatComposerHandle>(null);
  // 发送 RPC 在途标记:传给 ChatComposer 让发送按钮禁用 + spinner（乐观反馈的
  // 轻量实现;真正的乐观气泡需要临时行回收,在本文件消息模型下风险过高,见任务说明）。
  const [sendInFlight, setSendInFlight] = React.useState(false);
  const ensuredLocalCommandSessionRef = React.useRef<{
    agentId: number;
    projectId: number;
    promise: Promise<number>;
    requestId: number;
  } | null>(null);
  const ensuredLocalCommandSessionRequestRef = React.useRef(0);
  const handleStopLocalCommand = React.useCallback(
    async (terminalId: string) => {
      const delegated = await localCommandRuntimeStore.stop(terminalId);
      if (
        !delegated &&
        useLocalCommandsStore.getState().get(terminalId)?.status === "running"
      ) {
        console.error(
          "[chat] stop local command failed: runtime controller missing",
          { terminalId },
        );
      }
    },
    [],
  );

  const { reason: attentionReason } = useSessionAttention(sessionId);

  // ── 自主续轮(后台任务完成,CLI 自主跑的一轮)的会话级旁路订阅 ──
  // per-turn 流名只有用户 Send 才会从后端响应里拿到;自主轮没有这个入口,所以后端
  // 经会话级事件 "chat:autonomous:<sessionId>"(后端 AutonomousEventPrefix)推一帧
  // StreamAutonomousStarted。收到后:乐观翻 running + 插入新 assistant 行 + openStream
  // 订阅该自主轮的 per-turn 流,让它像普通 turn 一样实时渲染。后续 chunk/done 与
  // StreamDone→reloadSession 都复用既有路径,自主轮无需任何特殊渲染分支。
  // completedTask: 若事件携带后台任务身份,立即把对应 live tool_use block 的
  // subagent.status 翻成终态,刷新后台任务面板胶囊。
  // subagent_activity_started: 后台 subagent 开始产生内部活动(本地 claudecode 专有)。
  // 重开发起消息已有的 per-turn 流把嵌套块渲染回 AgentSpawnCard;不新建消息行,
  // session 保持 idle 态。remote claudecode 目前不发此事件。
  const onAutonomousEvent = React.useCallback(
    (ev: ChatStreamEvent) => {
      // subagent_activity_started: 后台 subagent 开始产生内部活动。
      // 只重开发起消息的流（openStream 绑到已存在的 launchMessageId）；
      // 不插入新消息行，不把 session 翻成 running（后台工作保持 idle 态）。
      if (ev.kind === "subagent_activity_started") {
        if (!ev.stream || !ev.launchMessageId) return;
        openStream({
          name: ev.stream,
          sessionId,
          assistantMessageId: ev.launchMessageId,
          streamStartedAt: Date.now(),
        });
        return;
      }
      // subagent_started/progress/done:后端在 per-turn 流之外镜像到会话级流的那一份。
      // 空闲态后台 subagent 的派遣卡早已落进 messages，ChatStreamsHost 那条只翻
      // liveBlocks 的路径必然落空 —— 卡片上的工具数 / token 会一直停在派遣那一刻
      // (sess-2275)。这里就地合并进 messages，与 completedTask 翻状态同一套做法。
      if (
        ev.kind === "subagent_started" ||
        ev.kind === "subagent_progress" ||
        ev.kind === "subagent_done"
      ) {
        if (!ev.toolUseId || !ev.subagent) return;
        const { toolUseId, subagent } = ev;
        setMessages((prev) =>
          mergeSubagentMetaInMessages(prev, toolUseId, subagent),
        );
        return;
      }
      // subagent_model:同上,会话级流镜像的模型事件(R2)只带 toolUseId + model 两个
      // 字段(不复用整份 Subagent 快照)——避免浅合并把已累计的 toolUses/totalTokens/
      // status 覆盖成空值(R4)。
      if (ev.kind === "subagent_model") {
        if (!ev.toolUseId || !ev.model) return;
        const { toolUseId, model } = ev;
        setMessages((prev) =>
          mergeSubagentMetaInMessages(prev, toolUseId, {
            model,
          } as chat_svc.ChatBlockSubagent),
        );
        return;
      }
      // autonomous_finished:自主轮 / 后台 subagent 活动轮收尾时会话级流补发的终态兜底。
      // per-turn 流的 openStream(ChatPanel)与 EventsOn 订阅(ChatStreamsHost)跨 render 解耦,
      // 短轮的 per-turn done/closed 可能赶在订阅注册前发完被漏掉 → LiveStream 永远留在 store
      // → streaming 卡死(发不出消息 / 空 assistant 行不回填)。会话级流常驻订阅、无此 race,
      // 据 launchMessageId 兜底 finishStream。幂等:per-turn done 已被收到时该流已不在,
      // streamForMessage 命中空直接跳过,不重复 bumpDone。
      if (ev.kind === "autonomous_finished") {
        const mid = ev.launchMessageId;
        if (!mid) return;
        const streamsState = useChatStreamsStore.getState();
        if (streamForMessage(streamsState, sessionId, mid)) {
          streamsState.finishStream(sessionId, mid, { kind: "done" });
        }
        return;
      }
      // error:自主续轮的 assistant 消息落库最终失败,后端已把会话翻 error、丢弃这一轮
      // 并中断 CLI 那一轮(见 docs/specs/2026-08-07-autonomous-turn-resilience.md
      // 「自主续轮落库失败时的可观察结果」)。失败的正是**建 assistant 行**那次写,所以
      // 这一轮既没有消息行也没有 per-turn 流 —— ChatStreamsHost 按 assistantMessageId
      // 收口的 error 路径接不住,只能就地渲染成 composer 上方的 notice。文案是后端
      // mapTurnError 给的动态文本(与用户发起的轮次同一套),不进 i18n。
      if (ev.kind === "error") {
        if (!ev.error) return;
        const { msg, detail } = splitErrorDetail(ev.error);
        setNotice({ kind: "error", text: msg, detail });
        markSessionStatus(sessionId, "error");
        return;
      }
      if (ev.kind !== "autonomous_started") {
        return;
      }
      // 先翻转后台任务状态 (completedTask 可能在没有 assistantMessage 时也存在)。
      // 后台任务完成是跨轮的:发起它的主轮早已结束,那条 tool_use block 已从 liveBlocks
      // 落进 messages。mergeSubagentMeta 只翻 liveBlocks(覆盖极少数仍在流的竞态),真正
      // 命中的是 messages —— 必须一并翻,否则面板胶囊 + 行内 pill 永远 spin (bug #2)。
      if (ev.completedTask?.toolUseId) {
        const { toolUseId, status, summary } = ev.completedTask;
        // 发起该后台任务的那条 tool_use 可能还挂在本会话任意一条在流的消息上
        // (哪条不确定,后台任务是跨轮的),逐条流试着翻一遍;真正命中的多半是
        // 下面的 messages。store 对未知 toolUseId 是 no-op。
        const streamsState = useChatStreamsStore.getState();
        for (const mid of streamsState.streams.get(sessionId)?.keys() ?? []) {
          streamsState.mergeSubagentMeta(sessionId, mid, toolUseId, {
            status,
            summary,
          } as chat_svc.ChatBlockSubagent);
        }
        setMessages((prev) =>
          flipSubagentStatusInMessages(prev, toolUseId, status, summary),
        );
      }
      if (!ev.assistantMessage || !ev.stream) {
        return;
      }
      const amsg = ev.assistantMessage;
      markSessionRunning(sessionId);
      openStream({
        name: ev.stream,
        sessionId,
        assistantMessageId: amsg.id,
        streamStartedAt: Date.now(),
      });
      setMessages((prev) => {
        if (prev.some((m) => m.id === amsg.id)) return prev;
        // R18:浏览器在空闲会话上「开新一轮」跑起的一轮,daemon 把发起方用户消息随
        // StreamAutonomousStarted 的 userMessages 带出 —— 先插 user 行再插 assistant,
        // 否则桌面端看到的又是「没有提问的回复」。来源标识在消息 DTO 上,转录渲染层
        // 复用 chat.message.fromDevice 的 inline pill(本机消息无 sourceDevice,零变化)。
        const userMsgs = ev.userMessages ?? [];
        const additions = [...userMsgs, amsg];
        return [...prev, ...additions];
      });
    },
    [sessionId, openStream, setMessages],
  );
  useChatStream(
    sessionId ? `chat:autonomous:${sessionId}` : null,
    onAutonomousEvent,
  );

  // ── 内部派生 breadcrumb（从 session.projectId + useProjectTree）──
  const { tree } = useProjectTree();
  const sessionProjectId = session?.projectId ?? 0;
  const currentSessionId = session?.id ?? 0;
  const composerCwd = React.useMemo(
    () =>
      session?.cwd ?? findProjectPath(tree, newSessionContext?.projectId ?? 0),
    [newSessionContext?.projectId, session?.cwd, tree],
  );
  const newSessionProjectName = React.useMemo(() => {
    const projectId = newSessionContext?.projectId ?? 0;
    if (projectId <= 0) return "";
    return projectChain(tree, projectId).join(" / ");
  }, [newSessionContext?.projectId, tree]);
  const derivedTopline = React.useMemo(() => {
    if (currentSessionId <= 0) return null;
    const sessionIDNode = (
      <span className="text-muted-foreground">sess-{currentSessionId}</span>
    );
    if (sessionProjectId <= 0) return sessionIDNode;
    const chain = projectChain(tree, sessionProjectId);
    if (chain.length === 0) return sessionIDNode;
    const projectTextColorClass = agentTextColorClassName(
      findProjectColorToken(tree, sessionProjectId),
    );
    return (
      <span className="inline-flex items-center gap-1.5">
        <Folder
          className={cn("size-3", projectTextColorClass)}
          aria-hidden="true"
        />
        <span className={projectTextColorClass}>{chain.join(" / ")}</span>
        <span className="text-muted-foreground">·</span>
        {sessionIDNode}
      </span>
    );
  }, [tree, sessionProjectId, currentSessionId]);

  // 持久化的会话加载失败不再静默关闭 tab:改为在转录区渲染错误卡（Retry / Close），
  // 由用户决定去留。真正的删除流（confirmDelete）才调 onSessionDeleted。

  // ── Transcript 滚动跟随 ──
  // atBottomRef = 用户上次滚动后是否停在底部附近（32px 容差）。
  // 新内容到达时只有"在底部"才自动跟随，否则保持当前位置不打扰用户阅读。
  const transcriptRef = React.useRef<HTMLElement>(null);
  const transcriptHandleRef = React.useRef<ChatTranscriptHandle>(null);
  const [transcriptElement, setTranscriptElement] =
    React.useState<HTMLElement | null>(null);
  const atBottomRef = React.useRef(true);
  // autoFollowRef = 「贴底跟随意图」,与 atBottomRef(纯位置:距底 ≤32px)不同 ——
  // 它对「内容增长把底部推远」免疫,只有用户主动上滚才置 false、滚回底部再置 true。
  // 流式逐 chunk 的贴底必须用它做闸:位置式 atBottomRef 在内容增长快过滚动时(正是
  // 流式)会被误判成"用户离开了底部"而永久关掉跟随,导致转录区冻结、输出沉到折叠线下。
  const autoFollowRef = React.useRef(true);
  // 记录上一次滚动后的 scrollTop,用来区分「用户上滚(scrollTop 变小)」与「内容增长/
  // 程序化贴底(scrollTop 不变或变大)」—— 只有前者才解除 autoFollow。
  const lastScrollTopRef = React.useRef(0);
  const [showBackToBottom, setShowBackToBottom] = React.useState(() => {
    const saved = loadTranscriptScrollState(scrollStateKey);
    return Boolean(saved && !saved.atBottom);
  });
  const restoredScrollStateKeyRef = React.useRef<string | null>(null);
  const pendingScrollRestoreRef = React.useRef<{
    key: string;
    scrollTop: number;
  } | null>(null);
  const collapsedScrollSaveGuardRef =
    React.useRef<CollapsedScrollRestoreGuard | null>(null);
  const collapsedRestoreFrameRef = React.useRef<number | null>(null);
  const collapsedRestoreTimerRef = React.useRef<number | null>(null);
  const sidebarOpen = useChatSidebarStore((s) => s.open);
  const setSidebarOpen = useChatSidebarStore((s) => s.setOpen);
  // 右侧 outline 高亮联动：跟踪 transcript 当前视野焦点对应的 message id。
  const activeMessageId = useVisibleMessageId(transcriptRef);

  const cancelCollapsedRestoreFrame = React.useCallback(() => {
    if (collapsedRestoreFrameRef.current == null) return;
    window.cancelAnimationFrame(collapsedRestoreFrameRef.current);
    collapsedRestoreFrameRef.current = null;
  }, []);

  const setTranscriptPaintSuppressed = React.useCallback(
    (suppressed: boolean) => {
      const el = transcriptRef.current;
      if (!el) return;
      el.style.visibility = suppressed ? "hidden" : "";
    },
    [],
  );

  const cancelCollapsedRestoreTimer = React.useCallback(() => {
    if (collapsedRestoreTimerRef.current == null) return;
    window.clearTimeout(collapsedRestoreTimerRef.current);
    collapsedRestoreTimerRef.current = null;
  }, []);

  // releaseCollapsedGuard 是「结束这次抑制」的唯一出口:清 guard、让转录区恢复可见、
  // 停掉 rAF 收敛循环与期限定时器。收尾以前在两个分支里逐行重复,漏一行就会把
  // 转录区留在 visibility:hidden 上。
  const releaseCollapsedGuard = React.useCallback(() => {
    collapsedScrollSaveGuardRef.current = null;
    setTranscriptPaintSuppressed(false);
    cancelCollapsedRestoreFrame();
    cancelCollapsedRestoreTimer();
  }, [
    cancelCollapsedRestoreFrame,
    cancelCollapsedRestoreTimer,
    setTranscriptPaintSuppressed,
  ]);

  const saveScrollSnapshot = React.useCallback(
    (snapshot: TranscriptScrollSnapshot) => {
      atBottomRef.current = snapshot.atBottom;
      setShowBackToBottom(!snapshot.atBottom);
      saveTranscriptScrollState(scrollStateKey, snapshot);
    },
    [scrollStateKey],
  );

  const restoreCollapsedScrollPosition = React.useCallback(() => {
    const guard = collapsedScrollSaveGuardRef.current;
    if (!guard || guard.key !== scrollStateKey) return false;
    const el = transcriptRef.current;
    if (!el) return false;
    const metrics = readScrollMetrics(el);
    if (metrics.maxScrollTop >= guard.minMaxScrollTop) {
      const nextScrollTop = guard.atBottom
        ? metrics.maxScrollTop
        : guard.scrollTop;
      el.scrollTop = nextScrollTop;
      saveScrollSnapshot({
        atBottom: guard.atBottom,
        scrollTop: nextScrollTop,
      });
      releaseCollapsedGuard();
      return true;
    }
    if (Date.now() <= guard.until) return false;
    releaseCollapsedGuard();
    return false;
  }, [releaseCollapsedGuard, scrollStateKey, saveScrollSnapshot]);

  const startCollapsedRestoreLoop = React.useCallback(() => {
    cancelCollapsedRestoreFrame();
    const tick = () => {
      collapsedRestoreFrameRef.current = null;
      const guard = collapsedScrollSaveGuardRef.current;
      if (!guard || guard.key !== scrollStateKey) return;
      if (restoreCollapsedScrollPosition()) return;
      if (collapsedScrollSaveGuardRef.current?.key !== scrollStateKey) return;
      collapsedRestoreFrameRef.current = window.requestAnimationFrame(tick);
    };
    collapsedRestoreFrameRef.current = window.requestAnimationFrame(tick);
  }, [
    cancelCollapsedRestoreFrame,
    scrollStateKey,
    restoreCollapsedScrollPosition,
  ]);

  React.useEffect(
    () => () => {
      cancelCollapsedRestoreFrame();
      cancelCollapsedRestoreTimer();
    },
    [cancelCollapsedRestoreFrame, cancelCollapsedRestoreTimer],
  );

  const saveBottomScrollPosition = React.useCallback(
    (metrics: ScrollMetrics) => {
      const el = transcriptRef.current;
      if (!el) return;
      el.scrollTop = metrics.maxScrollTop;
      saveScrollSnapshot({ atBottom: true, scrollTop: el.scrollTop });
    },
    [saveScrollSnapshot],
  );

  const skipWhileCollapsedHeight = React.useCallback(
    (metrics: ScrollMetrics) => {
      const guard = collapsedScrollSaveGuardRef.current;
      if (!guard || guard.key !== scrollStateKey) return false;
      return isCollapsedBelowGuard(metrics, guard);
    },
    [scrollStateKey],
  );

  const armCollapsedScrollRestore = React.useCallback(
    (saved: TranscriptScrollSnapshot) => {
      const guard: CollapsedScrollRestoreGuard = {
        atBottom: saved.atBottom,
        key: scrollStateKey ?? "",
        minMaxScrollTop: Math.max(
          0,
          saved.scrollTop - TRANSCRIPT_BOTTOM_THRESHOLD,
        ),
        scrollTop: saved.scrollTop,
        until: Date.now() + COLLAPSED_RESTORE_GUARD_MS,
      };
      collapsedScrollSaveGuardRef.current = guard;
      setTranscriptPaintSuppressed(true);
      startCollapsedRestoreLoop();
      // guard.until 只在 restoreCollapsedScrollPosition 里被比较,而那个函数只有两个
      // 调用方:rAF 收敛循环,和用户滚动。rAF 在窗口被遮挡 / 不在前台时会整段停摆
      // (本地实测 Chromium 停过 6.4s,同期 longtask 最长 143ms —— 是节流不是阻塞),
      // 于是期限永远等不到被检查:转录区无限期停在 visibility:hidden,只有用户滚一下
      // 才解除。所以期限得有自己的定时器 —— setTimeout 同样会被节流,但只是被钳到
      // ~1s 量级,不会停摆。
      cancelCollapsedRestoreTimer();
      collapsedRestoreTimerRef.current = window.setTimeout(() => {
        collapsedRestoreTimerRef.current = null;
        // 期间又 arm 过一次(切走再切回)→ 那次有自己的定时器,这枚过期的不许收尾。
        if (collapsedScrollSaveGuardRef.current !== guard) return;
        releaseCollapsedGuard();
      }, COLLAPSED_RESTORE_GUARD_MS);
    },
    [
      cancelCollapsedRestoreTimer,
      releaseCollapsedGuard,
      scrollStateKey,
      setTranscriptPaintSuppressed,
      startCollapsedRestoreLoop,
    ],
  );

  const handleTranscriptScroll = React.useCallback(() => {
    const el = transcriptRef.current;
    if (!el) return;
    const metrics = readScrollMetrics(el);
    // prevScrollTop 留给 nextAutoFollow 区分「用户上滚」与「内容增长/程序化贴底」。
    const prevScrollTop = lastScrollTopRef.current;
    lastScrollTopRef.current = metrics.scrollTop;
    const guard = collapsedScrollSaveGuardRef.current;
    if (guard && guard.key === scrollStateKey) {
      if (restoreCollapsedScrollPosition()) return;
      if (skipWhileCollapsedHeight(metrics)) return;
    }
    const saved = loadTranscriptScrollState(scrollStateKey);
    if (
      saved?.atBottom &&
      metrics.maxScrollTop > saved.scrollTop + TRANSCRIPT_BOTTOM_THRESHOLD &&
      metrics.scrollTop <= saved.scrollTop + TRANSCRIPT_BOTTOM_THRESHOLD
    ) {
      el.scrollTop = metrics.maxScrollTop;
      saveScrollSnapshot({ atBottom: true, scrollTop: metrics.maxScrollTop });
      autoFollowRef.current = true;
      lastScrollTopRef.current = metrics.maxScrollTop;
      return;
    }
    const atBottom = isTranscriptAtBottom(metrics);
    autoFollowRef.current = nextAutoFollow({
      prev: autoFollowRef.current,
      prevScrollTop,
      scrollTop: metrics.scrollTop,
      atBottom,
    });
    // 非贴底才记锚点(贴底走 followOnAppend / 结构性 follow 还原,不需要)。
    const anchor = atBottom ? null : computeTopVisibleAnchor(el);
    saveScrollSnapshot({
      atBottom,
      scrollTop: metrics.scrollTop,
      ...(anchor ?? {}),
    });
  }, [
    restoreCollapsedScrollPosition,
    saveScrollSnapshot,
    scrollStateKey,
    skipWhileCollapsedHeight,
  ]);
  const setTranscriptNode = React.useCallback((node: HTMLElement | null) => {
    transcriptRef.current = node;
    setTranscriptElement(node);
  }, []);

  // ── 当前选中会话的派生视图 ──
  // 没有 LiveStream entry 表示该会话当前不在生成中；UI 的 typing indicator /
  // liveDelta / liveThinking / liveBlocks 全部来自 store,天然按 sessionId 隔离。
  // 逐条 assistant 消息的流式内容表 —— 多条流并存时各渲各的,喂给 ChatTranscript。
  // 不能只喂主流:后台任务完成的自主续轮与用户轮会重叠,只喂一条会让另一条瞬间
  // 掉回持久化态(用户可见症状:「已输出内容清空回退」,sess-1950)。
  const liveByMessageId = React.useMemo(
    () => liveContentByMessageId(sessionStreams),
    [sessionStreams],
  );
  // 全部在流的 liveBlocks 拍平 —— 后台任务面板 / task-progress 是**会话级**视图,
  // 用户轮和后台流里起的任务都要收进来。
  const allLiveBlocks = React.useMemo(() => {
    if (!sessionStreams) return [] as ChatBlockData[];
    return Array.from(sessionStreams.values()).flatMap((s) => s.liveBlocks);
  }, [sessionStreams]);
  // 会话级读数取主流。
  const liveUsage = currentStream?.liveUsage ?? null;
  const liveContextWindow = currentStream?.liveContextWindow ?? 0;
  const liveCompacting = currentStream?.liveCompacting ?? false;
  const streaming = currentStream !== null;
  // 连接态是会话级的(不挂在某条流上),由 ChatStreamsHost 订阅 chat:conn:<sid> 写入。
  // 断连期间会话仍然是「运行中」—— 这里只换活信号的形态,不碰 agentStatus。
  const reconnecting = useSessionConnectionState(sessionId) === "reconnecting";

  // CLI mode 控件：claudecode 使用 permission mode，codex 使用 collaboration
  // mode 的 default/plan 子集。DB 是 source-of-truth；新会话还没有 sessionId
  // 时先保存在本地 state，首发 Send payload 会把 mode 写入新 session 行。
  // 空会话态改选了执行目标时，以该档的 backend type 为准（overrideBackendType 由
  // NewSessionExecTargetLine 报上来），caps / pill 与 Send 都跟随实际后端。
  const newSessionBackendType =
    effectiveTarget?.backendType ||
    (execTargetOverride && overrideBackendType
      ? overrideBackendType
      : (newSessionAgent?.backendType ?? ""));
  const activeBackendType = session?.backendType ?? newSessionBackendType;

  // Claude Code OAuth 配额 HUD:仅 claudecode backend 显示。device 维度优先 session
  // (已存在的会话),sessionId=0 新建态回退到 newSessionAgent —— 否则远端 agent 起的
  // 新会话还没发送时,quotaDeviceKey 会落到 "local" 把桌面本机配额错画上去。
  const activeDeviceID = session?.deviceID ?? newSessionAgent?.deviceID ?? "";
  const [localCommandHistoryScope, setLocalCommandHistoryScope] =
    React.useState<LocalCommandHistoryScope>();
  const [localCommandScopeRefreshTick, setLocalCommandScopeRefreshTick] =
    React.useState(0);
  const localCommandScopeResolutionRef = React.useRef(0);
  const commandScopeSessionId = sessionId > 0 ? sessionId : 0;
  const commandScopeAgentId =
    commandScopeSessionId === 0 ? (newSessionAgent?.id ?? 0) : 0;
  const commandScopeProjectId =
    commandScopeSessionId === 0 ? (newSessionContext?.projectId ?? 0) : 0;
  const commandScopeTargetAgentId =
    commandScopeSessionId > 0 ? (session?.agentId ?? 0) : commandScopeAgentId;
  const commandScopeTargetBackendType =
    commandScopeSessionId > 0
      ? (session?.backendType ?? "")
      : (newSessionAgent?.backendType ?? "");
  const commandScopeTargetCwd =
    commandScopeSessionId > 0 ? (session?.cwd ?? "") : composerCwd;
  const commandScopeTargetDeviceId =
    commandScopeSessionId > 0
      ? (session?.deviceID ?? "")
      : (newSessionAgent?.deviceID ?? "");
  const commandScopeTargetProjectId =
    commandScopeSessionId > 0
      ? (session?.projectId ?? 0)
      : commandScopeProjectId;
  React.useLayoutEffect(() => {
    const resolutionID = ++localCommandScopeResolutionRef.current;
    setLocalCommandHistoryScope(undefined);
    if (commandScopeSessionId <= 0 && commandScopeAgentId <= 0) return;

    const request: chat_svc.ResolveLocalCommandScopeRequest = {
      sessionId: commandScopeSessionId,
      agentId: commandScopeAgentId,
      projectId: commandScopeProjectId,
    };
    void (async () => {
      try {
        const scope = await ResolveLocalCommandScope(request);
        if (localCommandScopeResolutionRef.current !== resolutionID) return;
        setLocalCommandHistoryScope({
          deviceId: scope.deviceId,
          cwd: scope.cwd,
        });
      } catch {
        if (localCommandScopeResolutionRef.current !== resolutionID) return;
        setLocalCommandHistoryScope(undefined);
      }
    })();
    return () => {
      if (localCommandScopeResolutionRef.current === resolutionID) {
        localCommandScopeResolutionRef.current += 1;
      }
    };
  }, [
    // These target scalars are refresh signals only. History scope is always
    // the resolver response above, never a frontend-derived device/cwd fallback.
    commandScopeAgentId,
    commandScopeProjectId,
    commandScopeSessionId,
    commandScopeTargetAgentId,
    commandScopeTargetBackendType,
    commandScopeTargetCwd,
    commandScopeTargetDeviceId,
    commandScopeTargetProjectId,
    localCommandScopeRefreshTick,
  ]);
  const handleLocalCommandModeChange = React.useCallback((active: boolean) => {
    if (active) setLocalCommandScopeRefreshTick((tick) => tick + 1);
  }, []);
  const activeDeviceName =
    session?.deviceName ?? newSessionAgent?.deviceName ?? "";
  const quotaDeviceKey =
    activeBackendType === "claudecode"
      ? activeDeviceID
        ? `remote:${activeDeviceID}`
        : "local"
      : "";
  const quotaUsage = useCCUsage(quotaDeviceKey);
  const quotaDeviceLabel = activeDeviceID
    ? activeDeviceName || `device #${activeDeviceID}`
    : t("chatPanel.localDevice");
  // caps 来自后端 runtime 的 Capabilities — UI 不再按 backendType 硬分支。
  // 已有 session 走 GetSessionCapabilities;新对话(sessionId<=0)按
  // newSessionBackendType 走 GetBackendCapabilities — 这样 PermissionModePill
  // 在新对话首发前就能正确渲染并落定 backend 预设的 defaultPermissionMode。改选
  // 执行目标后 newSessionBackendType 跟随实际后端，caps 也随之更新。
  const { caps: sessionCaps } = useSessionCapabilities(
    sessionId > 0 ? sessionId : undefined,
  );
  const { caps: backendCaps } = useBackendCapabilities(
    sessionId > 0 ? undefined : newSessionBackendType || undefined,
  );
  const caps = sessionCaps ?? backendCaps;
  const isModeSwitchable = !!caps?.has("set_permission_mode");
  const canStopBackgroundTask = !!caps?.has("stop_background_task");
  const supportsImageInput = !!caps?.has("image_input");
  const supportsCompactRPC = caps
    ? caps.has("compact")
    : activeBackendType === "codex" || activeBackendType === "piagent";

  // composerContextUsage：当前会话 inputBox 底栏的「上下文用量」数据。
  //   - max  = session.contextWindow（解析顺序见 chat_svc.resolveContextWindowWithRuntime；为 0 时整块隐藏）。
  //   - used 优先用 liveUsage（runtime translator 在每次 API call 边界后端推一条
  //     StreamUsage,TotalInputTokens 已 family-aware 聚合好），fallback 到最新
  //     一条 assistant message 的 totalInputTokens 列。
  const effectiveContextWindow =
    liveContextWindow > 0 ? liveContextWindow : (session?.contextWindow ?? 0);
  const composerContextUsage = React.useMemo(
    () =>
      computeComposerContextUsage(messages, effectiveContextWindow, liveUsage),
    [messages, effectiveContextWindow, liveUsage],
  );
  const taskProgress = React.useMemo(
    () => deriveTaskProgress(messages, allLiveBlocks),
    [messages, allLiveBlocks],
  );
  const clearedList = useClearedBackgroundTasksStore((s) =>
    sessionId > 0 ? (s.cleared[sessionId] ?? EMPTY_CLEARED) : EMPTY_CLEARED,
  );
  const clearedSet = React.useMemo(() => new Set(clearedList), [clearedList]);
  const backgroundTasks = React.useMemo(
    () => deriveBackgroundTasks(messages, allLiveBlocks, clearedSet),
    [messages, allLiveBlocks, clearedSet],
  );
  const clearCompletedTasks = useClearedBackgroundTasksStore(
    (s) => s.clearCompleted,
  );
  const handleClearCompleted = React.useCallback(() => {
    if (sessionId <= 0) return;
    const doneIds = backgroundTasks
      .filter((tk) => tk.status !== "running")
      .map((tk) => tk.toolUseId);
    clearCompletedTasks(sessionId, doneIds);
  }, [sessionId, backgroundTasks, clearCompletedTasks]);
  // handleStopSubagent 停掉一个正在运行的后台任务/子 agent(下发 CLI stop_task,按发起它的
  // tool_use_id 定位;后端从持久化 subagent_state 读出 CLI task_id)。停成功后把块翻 canceled,
  // reload 让面板/卡片显示「已停止」。后台任务面板与转录 AgentSpawn 卡片共用它。
  const handleStopSubagent = React.useCallback(
    async (toolUseId: string) => {
      if (sessionId <= 0 || !toolUseId) return;
      try {
        await StopBackgroundTask({ sessionId, toolUseId });
        await reloadSession();
      } catch (e: unknown) {
        const { msg, detail } = splitErrorDetail(e);
        console.error("[chat] stop background task failed", e);
        setNotice({
          kind: "error",
          text: t("chatPanel.errors.stopBackgroundTask", { msg }),
          detail,
        });
        // 后端没停成(如缺 task_id / 已 evict):reload 把真实状态拉回,避免按钮点了没反应。
        await reloadSession();
      }
    },
    [sessionId, reloadSession, t],
  );
  // PermissionMode pill 数据从 caps.permissionModeMeta 拉;caps 未到位时
  // 用空 meta 做 placeholder(pill 整体被 isModeSwitchable 守护)。
  const permissionModeMeta = caps?.permissionModeMeta ?? {
    allowedModes: [],
    defaultMode: "",
    switchableDuringTurn: false,
    order: [],
  };
  const permissionMode = usePermissionMode({
    sessionId: isModeSwitchable && sessionId > 0 ? sessionId : undefined,
    permissionModeMeta,
    runtimeKey: activeBackendType,
    initialMode: session?.permissionMode,
    initialModeAtLaunch: session?.permissionModeAtLaunch,
    hasActiveSession: messages.length > 0,
    // 新会话场景下 session 还不存在 → initialMode 是 undefined；
    // 这里把 backend 管理员预设透下去，让 pill 起手值和 chat_svc.Send
    // spawn 时的 mode 一致（否则会被硬编码默认值覆盖）。
    backendDefaultMode: newSessionAgent?.defaultPermissionMode,
  });
  // switchableDuringTurn=false 的 runtime(典型 codex)在 turn 进行中不允许切 mode。
  const modeSwitchingDisabled =
    permissionModeMeta.switchableDuringTurn === false &&
    (streaming ||
      session?.agentStatus === "running" ||
      session?.agentStatus === "waiting");

  // ProviderPill:composer 里的 LLM 供应商选择器。新建会话（sessionId===0）与已有
  // 会话共用同一颗 pill（同一组件、同一弹层、同一位置），差异只在数据来源与禁用
  // 条件（规格 2026-08-10「已有会话切换 LLM 供应商」决策 10，取代 2026-08-09 决策 7
  // 的「已有会话不渲染任何切换器」）：不可切换（openclaw / 无兼容供应商 / 加载中）
  // 时 pill 常显但 disabled + tooltip 说明原因，不再隐藏。
  // 新建会话的绑定供应商来自 newSessionAgent（尚无 session 行）；已有会话来自
  // ChatSessionDetail.agentProviderKey / agentModelKey / providerKey / modelKey。已有会话
  // 选中后立即持久化（SetChatSessionModelTarget），成功再 reloadSession() 把新追加的
  // switch notice 拉进 transcript。
  const providerPill = useProviderPill({
    backendType: activeBackendType,
    boundProviderKey:
      sessionId > 0
        ? session?.agentProviderKey
        : (effectiveTarget?.llmProviderKey ?? newSessionAgent?.llmProviderKey),
    boundModelKey:
      sessionId > 0
        ? session?.agentModelKey
        : (effectiveTarget?.llmModelKey ?? undefined),
    sessionId,
    persistedProviderKey: sessionId > 0 ? session?.providerKey : undefined,
    persistedModelKey: sessionId > 0 ? session?.modelKey : undefined,
    // 远端执行时以目标 daemon 目录为可运行事实源（task 6 决策 12）：pill 据此禁用
    // daemon 上缺失/未同步的目标，并对旧 daemon 禁用 fixed-model。
    executionLocation:
      sessionId > 0
        ? (session?.deviceID ?? "")
        : (effectiveTarget?.deviceId ?? newSessionAgent?.deviceID ?? ""),
    onSwitched: () => void reloadSession(),
  });

  // prop 优先，无 prop 时降级到内部派生值。
  const effectiveTopline = headerTopline ?? derivedTopline;

  React.useLayoutEffect(() => {
    const el = transcriptRef.current;
    if (!el || !scrollStateKey) {
      return;
    }
    if (
      restoredScrollStateKeyRef.current === scrollStateKey &&
      pendingScrollRestoreRef.current?.key !== scrollStateKey
    ) {
      return;
    }
    const saved = loadTranscriptScrollState(scrollStateKey);
    if (!saved || saved.atBottom) {
      pendingScrollRestoreRef.current = null;
      restoredScrollStateKeyRef.current = scrollStateKey;
      return;
    }
    atBottomRef.current = false;
    // 优先锚点恢复:让虚拟器把保存时视口顶那条消息钉回原处,并随逐行复测收敛——
    // 不受"路由重挂时整列还是 estimate 高度→像素 scrollTop 落到错消息"的冷启动漂移。
    if (saved.anchorId != null) {
      if (
        transcriptHandleRef.current?.scrollToAnchor(
          saved.anchorId,
          saved.anchorOffset ?? 0,
          saved.anchorRowKey,
        )
      ) {
        pendingScrollRestoreRef.current = null;
        restoredScrollStateKeyRef.current = scrollStateKey;
        return;
      }
      // 锚点消息尚未加载进 displayMessages:先用 scrollTop 占位(避免顶部闪一下),
      // 留 pending 等下次 messages 变化(消息到位)再由 scrollToAnchor 精确钉回。
      pendingScrollRestoreRef.current = {
        key: scrollStateKey,
        scrollTop: saved.scrollTop,
      };
      el.scrollTop = saved.scrollTop;
      return;
    }
    // 回退:旧快照无锚点(贴底时存的 / 保存时无消息行)时,沿用像素恢复 + 逐渲染重试。
    pendingScrollRestoreRef.current = {
      key: scrollStateKey,
      scrollTop: saved.scrollTop,
    };
    el.scrollTop = saved.scrollTop;
    const metrics = readScrollMetrics(el);
    if (metrics.maxScrollTop < saved.scrollTop) {
      return;
    }
    pendingScrollRestoreRef.current = null;
    restoredScrollStateKeyRef.current = scrollStateKey;
  }, [messages, liveByMessageId, scrollStateKey, sessionId, transcriptElement]);

  React.useLayoutEffect(() => {
    if (pendingScrollRestoreRef.current?.key === scrollStateKey) {
      return;
    }
    if (!atBottomRef.current) {
      return;
    }
    const el = transcriptRef.current;
    if (!el) {
      return;
    }
    // 整个 chat 区被 display:none 收起时(App.tsx 在非 /chat·/projects 路由上这么做)
    // clientHeight=0、scrollHeight 也是 0;此时设 scrollTop=0 会让回来时停在顶部。
    // 跳过,等回到 /chat 后由 active 切换恢复逻辑兜底滚到底部。
    // 注意这挡不住「隐藏 tab」—— 非活跃面板是 visibility:hidden + absolute inset-0
    // (chat-panel-host.tsx:panelFrameClassName),照常参与布局,clientHeight 不为 0。
    if (el.clientHeight === 0) {
      return;
    }
    saveBottomScrollPosition(readScrollMetrics(el));
    // 依赖里只留 messages(结构性变化:首屏加载 / 发送乐观追加 / turn 落定 reload)。
    // 流式逐 chunk 的贴底由下面单独的 effect 接管(挂 liveDelta/liveThinking/...)。
  }, [messages, scrollStateKey, saveBottomScrollPosition]);

  // 流式逐 chunk 贴底。曾经把这件事完全交给虚拟器的 anchorTo:"end"(见 chat.tsx),
  // 但那条路只在「距底 ≤ 32px 钉底容差」时才钉:turn 开头结构性 follow 滚到的是
  // 占位行 estimate 高度的底部,真实流式文本测量出来更高 → 首帧就落后 >32px →
  // anchorTo:"end" 再也咬不回来,整轮转录区冻结、最新输出沉到折叠线下面(回归 bug)。
  // 这里改成「意图驱动」:只要 autoFollowRef(贴底跟随意图,对内容增长免疫、仅用户上滚
  // 才解除,见 nextAutoFollow)为真,就随每个流式增量把滚动钉到真实底部。读的是已提交 DOM
  // 的实时 scrollHeight(readScrollMetrics 同步触发 reflow),不是慢一帧的虚拟器 getTotalSize
  // 估值,故不掉队;钉到真实底部后虚拟器的 anchorTo:"end" 也回到容差内、自然协同而非互抢。
  React.useLayoutEffect(() => {
    if (pendingScrollRestoreRef.current?.key === scrollStateKey) {
      return;
    }
    if (!autoFollowRef.current) {
      return;
    }
    const el = transcriptRef.current;
    if (!el || el.clientHeight === 0) {
      return;
    }
    saveBottomScrollPosition(readScrollMetrics(el));
  }, [liveByMessageId, scrollStateKey, saveBottomScrollPosition]);

  // 面板重新变成活跃 tab 时，由父层 HostedPanel 传入的 active 信号驱动恢复：
  // 若用户切走前停在底部，就补一次 scrollTop=scrollHeight。上面那个 useLayoutEffect
  // 在整个 chat 区被路由 display:none 收起期间会被 clientHeight===0 跳过，回来时靠这里补。
  const prevActiveRef = React.useRef(active);
  React.useLayoutEffect(() => {
    const prev = prevActiveRef.current;
    prevActiveRef.current = active;
    if (!active || prev) return;
    const saved = loadTranscriptScrollState(scrollStateKey);
    if (saved && saved.scrollTop > 0) {
      armCollapsedScrollRestore(saved);
    }
    if (!atBottomRef.current) {
      return;
    }
    const el = transcriptRef.current;
    if (!el) {
      return;
    }
    const metrics = readScrollMetrics(el);
    if (skipWhileCollapsedHeight(metrics)) {
      return;
    }
    saveBottomScrollPosition(metrics);
  }, [
    active,
    armCollapsedScrollRestore,
    saveBottomScrollPosition,
    scrollStateKey,
    skipWhileCollapsedHeight,
  ]);

  // 切换会话时回到底部
  React.useEffect(() => {
    const saved = loadTranscriptScrollState(scrollStateKey);
    if (saved && !saved.atBottom) {
      atBottomRef.current = false;
      autoFollowRef.current = false;
      return;
    }
    atBottomRef.current = true;
    autoFollowRef.current = true;
    restoredScrollStateKeyRef.current = null;
    pendingScrollRestoreRef.current = null;
  }, [scrollStateKey, sessionId]);

  const handleBackToBottom = React.useCallback(() => {
    const el = transcriptRef.current;
    if (!el) return;
    // 用户主动点「回到底部」= 明确想跟随,恢复 autoFollow(上滚解除后由此重新咬合)。
    autoFollowRef.current = true;
    saveBottomScrollPosition(readScrollMetrics(el));
  }, [saveBottomScrollPosition]);

  // ── 补齐落定后的「跳到最新」──
  // 摘要由 ChatStreamsHost 在补齐落定那一发记下,这里只负责供转录行数、读与销账。
  //
  // 行数取数口:补齐窗口两端各调一次(掉线时快照、落定时做差),不是每帧算,所以
  // 现场 build 一次即可。两个数据源的取法不同:
  //   - messages 走 ref —— 它只在 reload 落定时换,慢一拍也是同一份;
  //   - 在流的内容直接读 store 的 getState() —— 补齐落定那一发连接态事件到达时,
  //     重放的内容早已进了 store 但 React 还没重渲,吃渲染期的 liveByMessageId
  //     会数到补齐前的旧值,差出来恒等于 0。
  // 这里不复现转录区的折叠(压缩前旧消息)与本地命令行:两者在窗口两端同增同减,
  // 做差时抵消,补齐本身也产不出它们。
  const messagesRef = React.useRef(messages);
  React.useEffect(() => {
    messagesRef.current = messages;
  }, [messages]);
  React.useEffect(() => {
    if (sessionId <= 0) return;
    return registerTranscriptRowCounter(
      sessionId,
      () =>
        buildTranscriptRows({
          displayMessages: messagesRef.current,
          autonomousIds: EMPTY_AUTONOMOUS_IDS,
          liveByMessageId: liveContentByMessageId(
            sessionStreamMap(useChatStreamsStore.getState(), sessionId),
          ),
        }).rows.length,
    );
  }, [sessionId]);

  // 销账条件是「人回到了底部」而不是「点了控件」:自己滚回底部同样意味着补齐内容
  // 已经看过了,不销账的话下次往上翻会撞见一枚早就过期的控件。贴底时本就沿用既有的
  // 贴底跟随,控件也永远不出现(渲染条件与销账条件是同一个 showBackToBottom)。
  const catchUp = useCatchUpSummary(sessionId);
  React.useEffect(() => {
    if (showBackToBottom || !catchUp) return;
    clearCatchUp(sessionId);
  }, [catchUp, sessionId, showBackToBottom]);

  // ── 跨路由 turn 落定后的善后 ──
  // store 在 done/error/closed 时给该 sessionId 自增 doneTick。我们只关心「当前正在
  // 显示」的会话:抓最新的 lastDoneEvent,reload 一次 useChatSession 把后端写好的
  // 最终 blocks(穿插顺序)拉回来,然后做 error 文案等副作用。
  // MarkChatSessionRead 不在这里调 —— 由下方 active-gated effect 在
  // reloadSession 拉到新的 session.lastMessageAt 后自动触发(隐藏 tab active=false
  // 不应被标已读)。
  // 第一次 mount 时 doneTick=0,什么都不做(用 ref 跳过首次)。
  const lastSeenDoneTickRef = React.useRef(doneTick);
  React.useEffect(() => {
    if (!sessionId) return;
    if (doneTick === lastSeenDoneTickRef.current) return;
    lastSeenDoneTickRef.current = doneTick;
    const ev = lastDoneEvent;
    if (!ev) return;
    if (ev.kind === "steer_consumed") {
      setMessages((prev) => applySteerConsumed(prev, ev));
      void reloadSession();
      onSidebarShouldReload?.();
    } else if (ev.kind === "done") {
      // 后端在发 done 前已经 chat_repo.Message().Update,reload 拿到最终顺序。
      void reloadSession();
      onSidebarShouldReload?.();
    } else if (ev.kind === "error") {
      // 错误路径:后端同样 Update 过 assistant.errorText,但有可能 final message 已附带
      // ev.message。两条路都靠 reload 把最新落库状态拿回来;再补 errorText 落到 UI。
      if (ev.message) {
        setMessages((prev) => upsertMessage(prev, ev.message!));
      } else if (ev.error) {
        setMessages((prev) => applyStreamError(prev, undefined, ev.error));
      }
      void reloadSession();
      onSidebarShouldReload?.();
    } else if (ev.kind === "aborted") {
      // 用户主动「停止」：后端已经把 partial 内容写入 DB 且 errorText 为空。
      // 走和 done 一样的 reload 路径即可，让 transcript 渲染 partial 结果；
      // 不调 MarkRead（abort 不是「用户已读完」语义）。
      void reloadSession();
      onSidebarShouldReload?.();
    } else if (ev.kind === "closed") {
      // closed 单独出现(没先来 done/error)通常意味着 wails 端被关掉,不算 turn 结束,
      // 不主动 reload 也不动 errorText —— 与旧版行为对齐。
    }
  }, [
    doneTick,
    lastDoneEvent,
    onSidebarShouldReload,
    reloadSession,
    sessionId,
    setMessages,
  ]);

  // ── Mark-read: 仅当当前 ChatPanel 是「可见 tab」时,把 lastMessageAt 推进到
  // 服务端 last_read_at 并同步到 read overlay。chat-panel-host 会把所有 tab
  // 都 mount(隐藏 tab 用 visibility:hidden),所以一定要 gate 在 active prop 上 ——
  // 否则后台 tab 在 turn 完成 / 启动恢复时会被错误地标记为已读,未读 indicator
  // 永远不出现。
  //
  // 触发时机:active 翻成 true、sessionId 变化、或 session.lastMessageAt 推进
  // (turn 落定 → reloadSession → meta/lastMessageAt 更新)。lastMessageAt=0
  // 是没有消息的新会话,无需 mark。
  const sessionLastMessageAt = session?.lastMessageAt ?? 0;
  React.useEffect(() => {
    if (!active || sessionId <= 0 || sessionLastMessageAt <= 0) return;
    void MarkChatSessionRead({
      sessionId,
      timestamp: sessionLastMessageAt,
    });
    useSessionReadStore.getState().markRead(sessionId, sessionLastMessageAt);
  }, [active, sessionId, sessionLastMessageAt]);

  async function doSend(
    targetSessionId: number,
    agentId: number,
    message: ChatComposerSubmit,
    permissionModeOverride?: string,
  ) {
    const text = message.text.trim();
    const images = message.images ?? [];
    // 发送消息时强制跟随到底部，无论用户当前在哪里
    atBottomRef.current = true;
    setShowBackToBottom(false);
    setSendInFlight(true);
    // 调用点都是 void doSend(...) fire-and-forget；这里必须自吞错误成 notice，
    // 否则 RPC 失败时 UI 完全无声（用户在 composer 干瞪眼）。doEnqueue 的 fallback
    // 也走这里，set notice 后不 rethrow，正好顶替 doEnqueue 原本的 setNotice。
    try {
      // R18 桌面派发：空会话态且生效目标是另一台桌面端（kind=desktop）时，新建会话
      // 走 peer RunFresh（对端真实会话 id 回传），不走本地 EnsureChatSession/Send。
      // agentred 与本机行为不变（R22）。
      if (
        targetSessionId === 0 &&
        effectiveTarget?.kind === "desktop" &&
        effectiveTarget.deviceId
      ) {
        const ack = await PeerRunFresh({
          fingerprint: effectiveTarget.deviceId,
          agentId,
          projectId: newSessionContext?.projectId ?? 0,
          title: text,
          text,
          permissionMode:
            permissionModeOverride ??
            (isModeSwitchable ? permissionMode.mode : ""),
          ...(providerPill.providerKey
            ? {
                providerKey: providerPill.providerKey,
                modelKey: providerPill.modelKey,
              }
            : {}),
        } as Parameters<typeof PeerRunFresh>[0]);
        onPeerSessionCreated?.({
          fingerprint: effectiveTarget.deviceId,
          sessionId: ack?.sessionId ?? 0,
          title: text,
          deviceName: effectiveTarget.deviceName,
        });
        onSidebarShouldReload?.();
        return;
      }
      // 新建会话路径：把项目上下文带上（仅 targetSessionId=0 时生效）；
      // 已存在会话续发：projectId 在 Send 端被忽略，传 0 也无害。
      const sendPayload: Record<string, unknown> = {
        sessionId: targetSessionId,
        agentId,
        text,
        projectId:
          targetSessionId === 0 ? (newSessionContext?.projectId ?? 0) : 0,
        permissionMode:
          permissionModeOverride ??
          (isModeSwitchable ? permissionMode.mode : ""),
        // 新建会话首发前预选：瞬态 ModelTarget 随 SendRequest.ProviderKey/ModelKey 透传,
        // 与 Session 一同落库（spec 2026-08-11「新建与已有会话流程」）。已有会话后端忽略
        // 该字段（改 target 走 SetChatSessionModelTarget）。
        ...(targetSessionId === 0 && providerPill.providerKey
          ? {
              providerKey: providerPill.providerKey,
              modelKey: providerPill.modelKey,
            }
          : {}),
        // R15a 手动指定执行目标：同一条规则，仅新建会话生效；0/未选时不传，
        // 后端按 R15 顺序自动挑第一个可用的档。
        ...(targetSessionId === 0 && execTargetOverride
          ? { execTargetOverride }
          : {}),
      };
      if (images.length > 0) {
        sendPayload.images = images.map((image) => ({
          name: image.name,
          dataUrl: image.dataUrl,
        }));
      }
      const resp = await SendChatMessage(
        chat_svc.SendRequest.createFrom(sendPayload),
      );
      // 新建会话路径：通知父级把 selectedSessionId 切到新 id。
      if (targetSessionId === 0 && resp.sessionId) {
        onSessionCreated?.(resp.sessionId, agentId);
      }
      setMessages((prev) => [
        ...prev,
        optimisticUser(resp.userMessageId, resp.sessionId, text, images),
        optimisticAssistantPlaceholder(resp.assistantMessageId, resp.sessionId),
      ]);
      // 乐观写 running: 后端 Send 已把 sess.AgentStatus="running" 落库, 但 turn
      // 起手没 emit session_status 事件, tab / 详情 toolbar 单纯读 store 会停在
      // 上一轮的 idle。这里同步翻成 running, 让所有订阅者一帧内看到运行态。
      markSessionRunning(resp.sessionId);
      openStream({
        name: resp.stream,
        sessionId: resp.sessionId,
        assistantMessageId: resp.assistantMessageId,
        streamStartedAt: Date.now(),
      });
      // 创建新会话时后端在 RPC 内已写入 AgentStatus="running" 并落库，
      // 立刻 reload 让左侧 sidebar 同步出现新会话 + running 状态，不用等 turn 结束。
      onSidebarShouldReload?.();
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] send failed", e);
      // 发送失败:ChatComposer 已清空输入框,这里把文本 + 图片原样放回(草稿保留),
      // 并给 notice 挂 Retry / Discard 动作。Retry 用同一份 message 重新 doSend;
      // Discard 清掉恢复的草稿。msg(headline)拼进 notice 文本让用户知道为什么失败,
      // cause(detail)按既有规则只在存在时渲染成详情块。
      composerRef.current?.restoreDraft(text, images);
      const retryMessage: ChatComposerSubmit =
        images.length > 0 ? { text, images } : { text };
      setNotice({
        kind: "error",
        text: `${t("chatPanel.sendRetry.restored")} · ${msg}`,
        detail,
        actions: {
          retry: () => {
            setNotice(null);
            void doSend(
              targetSessionId,
              agentId,
              retryMessage,
              permissionModeOverride,
            );
          },
          discard: () => {
            composerRef.current?.clearDraft();
            setNotice(null);
          },
        },
      });
    } finally {
      setSendInFlight(false);
    }
  }

  const launchLocalCommand = React.useCallback(
    async (
      sid: number,
      command: string,
    ): Promise<LocalCommandHistoryScope | undefined> => {
      const terminalId = crypto.randomUUID();
      const closeRetryInitialDelayMs = 100;
      const closeRetryMaxDelayMs = 5_000;
      // 端口的退订句柄:只摘自己、幂等、同步生效。同一条 PTY 上的终端视图
      // (卡片的“在终端中打开”)与本卡片共用一份扇出,谁走都不影响另一个。
      let releaseObservers: TerminalUnsubscribe | undefined;
      let settled = false;
      let closePromise: Promise<void> | undefined;
      let automaticCloseRequired = false;
      let automaticCloseGuardianTimer: number | undefined;
      let automaticCloseRetryDelayMs = closeRetryInitialDelayMs;
      let userStopRequested = false;
      const clearAutomaticCloseTimer = () => {
        if (automaticCloseGuardianTimer === undefined) return;
        window.clearTimeout(automaticCloseGuardianTimer);
        automaticCloseGuardianTimer = undefined;
      };
      const stopAutomaticCloseGuardian = () => {
        automaticCloseRequired = false;
        clearAutomaticCloseTimer();
      };
      const appendFailure = (error: unknown) => {
        if (settled) return;
        const commands = useLocalCommandsStore.getState();
        if (commands.get(terminalId)?.status === "running") {
          commands.appendOutput(terminalId, String(error));
        }
      };
      const settle = (
        status: "done" | "failed" | "stopped",
        exitCode?: number,
      ) => {
        if (settled) return;
        settled = true;
        stopAutomaticCloseGuardian();
        const commands = useLocalCommandsStore.getState();
        if (commands.get(terminalId)?.status === "running") {
          if (exitCode === undefined) commands.finish(terminalId, status);
          else commands.finish(terminalId, status, exitCode);
        }
        // 退订同步生效且只摘自己:走人之后本卡片再收不到任何帧,不需要“确认清干净”
        // 的重试世代 —— 底层监听撤没撤掉是传输层的事,与本卡片的结算无关。
        releaseObservers?.();
        localCommandRuntimeStore.unregister(terminalId, controller);
      };
      const fail = (error: unknown) => {
        if (settled) return;
        appendFailure(error);
        settle("failed", -1);
      };
      const scheduleAutomaticCloseGuardian = () => {
        if (
          settled ||
          !automaticCloseRequired ||
          automaticCloseGuardianTimer !== undefined
        ) {
          return;
        }
        const retryDelayMs = automaticCloseRetryDelayMs;
        automaticCloseRetryDelayMs = Math.min(
          automaticCloseRetryDelayMs * 2,
          closeRetryMaxDelayMs,
        );
        automaticCloseGuardianTimer = window.setTimeout(() => {
          automaticCloseGuardianTimer = undefined;
          void requestTerminalClose("automatic");
        }, retryDelayMs);
      };
      const requestTerminalClose = (
        ownership: "automatic" | "user",
      ): Promise<void> => {
        if (ownership === "user") {
          userStopRequested = true;
          clearAutomaticCloseTimer();
        }
        if (settled) return Promise.resolve();
        if (closePromise) return closePromise;
        const pending = (async () => {
          let authoritative = false;
          try {
            await TerminalClose(terminalId);
            authoritative = true;
          } catch (error: unknown) {
            if (isTerminalNotOpenError(error)) authoritative = true;
            else if (!automaticCloseRequired) appendFailure(error);
          }
          if (authoritative) {
            if (userStopRequested) settle("stopped");
            else settle("failed", -1);
            return;
          }
          scheduleAutomaticCloseGuardian();
        })();
        closePromise = pending;
        void pending.finally(() => {
          if (closePromise === pending) closePromise = undefined;
        });
        return pending;
      };
      const startAutomaticCloseGuardian = () => {
        if (settled) return;
        if (!automaticCloseRequired) {
          automaticCloseRetryDelayMs = closeRetryInitialDelayMs;
        }
        automaticCloseRequired = true;
        void requestTerminalClose("automatic");
      };
      const decode = makeStreamDecoder();
      const observers: TerminalSubscriber = {
        onData: (bytes) => {
          if (settled) return;
          useLocalCommandsStore
            .getState()
            .appendOutput(terminalId, decode(bytes));
        },
        onExit: ({ code, reason }) => {
          const status =
            reason === "killed" ? "stopped" : code === 0 ? "done" : "failed";
          settle(status, code);
        },
      };
      const controller: LocalCommandRuntimeController = {
        stop: () => requestTerminalClose("user"),
      };
      localCommandRuntimeStore.register(terminalId, controller);
      useLocalCommandsStore.getState().start({
        id: terminalId,
        sessionId: sid,
        command,
        createdAt: Date.now(),
      });
      // 先订阅再起 PTY:第一帧完全可能早于 TerminalRunCommand 兑现。
      // 传输层保证一次失败的 subscribe 不留半截注册,所以重试一次即可,
      // 不需要自持世代 / 清理看门狗。
      let observerError: unknown;
      for (let attempt = 0; attempt < 2 && !releaseObservers; attempt += 1) {
        try {
          releaseObservers = transport.subscribe(terminalId, observers);
        } catch (error: unknown) {
          observerError = error;
        }
      }
      const observersReady = releaseObservers !== undefined;
      try {
        const response = await TerminalRunCommand(
          terminalId,
          sid,
          command,
          80,
          24,
        );
        if (response.startError) fail(response.startError);
        else if (!observersReady) {
          appendFailure(observerError);
          startAutomaticCloseGuardian();
        }
        return {
          deviceId: response.scope.deviceId,
          cwd: response.scope.cwd,
        };
      } catch (error: unknown) {
        if (observersReady) fail(error);
        else {
          appendFailure(error);
          startAutomaticCloseGuardian();
        }
        return undefined;
      }
    },
    [transport],
  );

  React.useEffect(() => {
    if (
      sessionId > 0 ||
      !newSessionAgent ||
      ensuredLocalCommandSessionRef.current?.agentId !== newSessionAgent.id ||
      ensuredLocalCommandSessionRef.current?.projectId !==
        (newSessionContext?.projectId ?? 0)
    ) {
      ensuredLocalCommandSessionRef.current = null;
    }
  }, [newSessionAgent, newSessionContext?.projectId, sessionId]);

  function ensureLocalCommandSession(): Promise<number> {
    if (!newSessionAgent) return Promise.resolve(0);
    const agentId = newSessionAgent.id;
    const projectId = newSessionContext?.projectId ?? 0;
    const current = ensuredLocalCommandSessionRef.current;
    if (current?.agentId === agentId && current.projectId === projectId) {
      return current.promise;
    }

    const requestId = ++ensuredLocalCommandSessionRequestRef.current;
    const promise = (async () => {
      try {
        const sid = await EnsureChatSession(agentId, projectId);
        if (!sid) {
          if (ensuredLocalCommandSessionRef.current?.requestId === requestId) {
            ensuredLocalCommandSessionRef.current = null;
          }
          return 0;
        }
        onSessionCreated?.(sid, agentId);
        onSidebarShouldReload?.();
        return sid;
      } catch (e: unknown) {
        if (ensuredLocalCommandSessionRef.current?.requestId === requestId) {
          ensuredLocalCommandSessionRef.current = null;
        }
        const { msg, detail } = splitErrorDetail(e);
        setNotice({
          kind: "error",
          text: t("chatPanel.errors.send", { msg }),
          detail,
        });
        return 0;
      }
    })();
    ensuredLocalCommandSessionRef.current = {
      agentId,
      projectId,
      promise,
      requestId,
    };
    return promise;
  }

  async function runLocalCommand(
    targetSessionId: number,
    command: string,
  ): Promise<LocalCommandHistoryScope | undefined> {
    let sid = targetSessionId;
    if (!sid) sid = await ensureLocalCommandSession();
    if (!sid) return undefined;
    return launchLocalCommand(sid, command);
  }

  async function doCompact(sid: number) {
    if (!sid) return;
    try {
      atBottomRef.current = true;
      setShowBackToBottom(false);
      const resp = await CompactChatSession({ sessionId: sid });
      setMessages((prev) => {
        if (prev.some((m) => m.id === resp.assistantMessageId)) return prev;
        return [
          ...prev,
          optimisticAssistantPlaceholder(
            resp.assistantMessageId,
            resp.sessionId,
          ),
        ];
      });
      markSessionRunning(resp.sessionId);
      openStream({
        name: resp.stream,
        sessionId: resp.sessionId,
        assistantMessageId: resp.assistantMessageId,
        streamStartedAt: Date.now(),
      });
      onSidebarShouldReload?.();
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] compact failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.compact", { msg }),
        detail,
      });
    }
  }

  async function doGoal(sid: number, agentId: number, cmd: GoalCommand) {
    if (!sid) return;
    try {
      if (cmd.kind === "get") {
        const resp = await GetChatGoal({ sessionId: sid });
        const goal = resp.goal;
        setNotice({
          kind: "info",
          text: goal
            ? t("chatPanel.goal.current", {
                objective: goal.objective,
                status: goal.status,
                tokens: goal.tokensUsed ?? 0,
              })
            : t("chatPanel.goal.empty"),
        });
        return;
      }
      if (cmd.kind === "clear") {
        await ClearChatGoal({ sessionId: sid });
        setNotice({ kind: "info", text: t("chatPanel.goal.cleared") });
        return;
      }
      const payload =
        cmd.kind === "set"
          ? { sessionId: sid, objective: cmd.objective, status: "active" }
          : { sessionId: sid, status: cmd.status };
      const resp = await SetChatGoal(payload);
      setNotice({
        kind: "info",
        text: resp.goal
          ? t("chatPanel.goal.updatedWithObjective", {
              objective: resp.goal.objective,
            })
          : t("chatPanel.goal.updated"),
      });
      if (cmd.kind === "set") {
        await doSend(sid, agentId, { text: cmd.objective });
      }
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] goal failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.goal", { msg }),
        detail,
      });
    }
  }

  async function doStartGoal(
    agentId: number,
    cmd: Extract<GoalCommand, { kind: "set" }>,
  ) {
    try {
      const resp = await StartChatGoal({
        agentId,
        projectId: newSessionContext?.projectId ?? 0,
        objective: cmd.objective,
        status: "active",
        permissionMode: isModeSwitchable ? permissionMode.mode : "",
      });
      if (resp.sessionId) {
        onSessionCreated?.(resp.sessionId, agentId);
      }
      onSidebarShouldReload?.();
      setNotice({
        kind: "info",
        text: resp.goal
          ? t("chatPanel.goal.updatedWithObjective", {
              objective: resp.goal.objective,
            })
          : t("chatPanel.goal.updated"),
      });
      if (resp.sessionId) {
        await doSend(resp.sessionId, agentId, { text: cmd.objective });
      }
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] start goal failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.goal", { msg }),
        detail,
      });
    }
  }

  function notifyCompactNeedsSession() {
    setNotice({
      kind: "info",
      text: t("chatPanel.compact.needsSession"),
    });
  }

  function notifyCompactWaitForTurn() {
    setNotice({ kind: "info", text: t("chatPanel.compact.waitForTurn") });
  }

  const handlePlanActionStarted = React.useCallback(
    (resp: PlanActionStream, userText: string) => {
      if (!resp.stream || !resp.sessionId || !resp.assistantMessageId) return;
      atBottomRef.current = true;
      setShowBackToBottom(false);
      setMessages((prev) => {
        const next = [...prev];
        if (!next.some((m) => m.id === resp.userMessageId)) {
          next.push(
            optimisticUser(resp.userMessageId, resp.sessionId, userText),
          );
        }
        if (!next.some((m) => m.id === resp.assistantMessageId)) {
          next.push(
            optimisticAssistantPlaceholder(
              resp.assistantMessageId,
              resp.sessionId,
            ),
          );
        }
        return next;
      });
      markSessionRunning(resp.sessionId);
      openStream({
        name: resp.stream,
        sessionId: resp.sessionId,
        assistantMessageId: resp.assistantMessageId,
        streamStartedAt: Date.now(),
      });
      onSidebarShouldReload?.();
    },
    [onSidebarShouldReload, openStream, setMessages],
  );

  // doEnqueue：streaming 中按回车走这里。把新消息推到当前 turn 的排队队列，
  // 等 AI 跑到下一个安全点（claudecode PreToolUse hook / codex turn/steer RPC 即刻 /
  // builtin cago safe-point）才注入。
  async function doEnqueue(sid: number, agentId: number, text: string) {
    try {
      const resp = await EnqueueChatMessage({ sessionId: sid, text });
      useQueuedMessagesStore.getState().append(sid, {
        id: resp.queuedId,
        text,
        cancellable: resp.cancellable,
      });
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      if (isChatSteerNoActiveError(msg)) {
        // turn 已结束（done/closed 事件即将到 / 已到），按普通 send 重新起一轮。
        await doSend(sid, agentId, { text });
        return;
      }
      console.error("[chat] enqueue failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.enqueue", { msg }),
        detail,
      });
    }
  }

  async function doCancelQueued(sid: number, queuedId: string) {
    try {
      const resp = await CancelQueuedChatMessage({ sessionId: sid, queuedId });
      useQueuedMessagesStore.getState().consume(sid, resp.removed);
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] cancel queued failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.cancelQueued", { msg }),
        detail,
      });
    }
  }

  // doStop 软中断当前 turn。后端会按 backend 分别走 control_request{interrupt}
  // /turn/interrupt/ctx-cancel，子进程都保留，发个 StreamAborted 事件让 store
  // bump tick → reload 拿 partial 内容。这里不做乐观 UI，等 aborted 事件回来。
  async function doStop(sid: number) {
    try {
      await StopChatMessage({ sessionId: sid });
      // 「重启遗孤」会话(DB 卡在 running/waiting 但本地无活跃 stream):后端已把它
      // reconcile 回 idle,但这类会话没有活跃 stream 不会推 aborted 事件,doneTick
      // effect 也不会触发 reload —— 必须主动 reload 才能让那颗一直亮着的「停止」按钮
      // 回灰。正常活跃 turn 的 abort 仍由 aborted 事件驱动 reload,这里多一次读无害。
      await reloadSession();
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      // turn 已自然完成与点击 Stop 发生 race —— 后端 activeCancels 已经清掉了，
      // 不算错，静默即可（用户的意图是「让这轮停下」，结果已经停了）。但前端视图可能
      // 还停在 running,reload 一次把 DB 的终态拉回来,避免按钮一直亮着点了没反应。
      if (isChatStopNoActiveError(msg)) {
        console.warn("[chat] stop race-lost (turn already finished)");
        await reloadSession();
        return;
      }
      console.error("[chat] stop failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.stop", { msg }),
        detail,
      });
    }
  }

  function handleRegenerate(messageId: number) {
    if (!sessionId) return;
    setPendingRegenId(messageId);
  }

  async function confirmRegenerate() {
    const messageId = pendingRegenId;
    if (messageId == null || !sessionId) {
      setPendingRegenId(null);
      return;
    }
    setPendingRegenId(null);

    const snapshot = messages;
    const targetIdx = snapshot.findIndex((m) => m.id === messageId);
    if (targetIdx < 0) return;
    let userIdx = -1;
    for (let i = targetIdx - 1; i >= 0; i--) {
      if (snapshot[i].role === "user") {
        userIdx = i;
        break;
      }
    }
    if (userIdx < 0) return;
    const userText = textOfChatMessage(snapshot[userIdx]);

    try {
      atBottomRef.current = true;
      setShowBackToBottom(false);
      const resp = await RegenerateChatMessage({
        sessionId,
        messageId,
        permissionMode: isModeSwitchable ? permissionMode.mode : "",
      });
      markSessionRunning(resp.sessionId);
      openStream({
        name: resp.stream,
        sessionId: resp.sessionId,
        assistantMessageId: resp.assistantMessageId,
        streamStartedAt: Date.now(),
      });
      setMessages([
        ...snapshot.slice(0, userIdx),
        optimisticUser(resp.userMessageId, resp.sessionId, userText),
        optimisticAssistantPlaceholder(resp.assistantMessageId, resp.sessionId),
      ]);
      onSidebarShouldReload?.();
    } catch (e: unknown) {
      console.error("[chat] regenerate failed", e);
      const { msg, detail } = splitErrorDetail(e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.regenerate", { msg }),
        detail,
      });
    }
  }

  async function confirmRename() {
    if (!pendingRename) return;
    const next = pendingRename.draft.trim();
    if (!next) {
      setPendingRename(null);
      return;
    }
    const id = pendingRename.id;
    setPendingRename(null);
    try {
      await RenameChatSession({ sessionId: id, title: next });
      onSidebarShouldReload?.();
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] rename failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.rename", { msg }),
        detail,
      });
    }
  }

  function handleDelete(id: number) {
    setPendingDeleteId(id);
  }

  async function handleCopyLaunchCommand(sid: number) {
    try {
      const resp = await GetChatLaunchCommand({ sessionId: sid });
      await copyTextWithToast(resp.command, {
        errorTitle: t("chatPanel.launchCommand.copyFailed"),
        successTitle: t("chatPanel.launchCommand.copyDone"),
        successDescription: t("chatPanel.launchCommand.copyDescription"),
      });
    } catch (e: unknown) {
      const { msg, detail } = splitErrorDetail(e);
      console.error("[chat] copy launch command failed", e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.copyLaunchCommand", { msg }),
        detail,
      });
    }
  }

  async function confirmDelete() {
    const id = pendingDeleteId;
    if (id == null) return;
    setPendingDeleteId(null);
    await DeleteChatSession({ sessionId: id });
    onSessionDeleted?.();
    onSidebarShouldReload?.();
  }

  function handleEdit(messageId: number) {
    if (!sessionId) return;
    const target = messages.find((m) => m.id === messageId);
    if (!target || target.role !== "user") return;
    setEditingMessage({
      sessionId,
      messageId,
      text: textOfChatMessage(target),
    });
  }

  async function confirmEdit(newText: string) {
    const pending = activeEditing;
    if (pending == null || !sessionId) {
      setEditingMessage(null);
      return;
    }
    const trimmed = newText.trim();
    if (!trimmed) {
      setEditingMessage(null);
      return;
    }
    setEditingMessage(null);

    const snapshot = messages;
    const targetIdx = snapshot.findIndex((m) => m.id === pending.messageId);
    if (targetIdx < 0) return;

    try {
      atBottomRef.current = true;
      setShowBackToBottom(false);
      const resp = await EditChatMessage({
        sessionId,
        messageId: pending.messageId,
        text: trimmed,
        permissionMode: isModeSwitchable ? permissionMode.mode : "",
      });
      markSessionRunning(resp.sessionId);
      openStream({
        name: resp.stream,
        sessionId: resp.sessionId,
        assistantMessageId: resp.assistantMessageId,
        streamStartedAt: Date.now(),
      });
      setMessages([
        ...snapshot.slice(0, targetIdx),
        optimisticUser(resp.userMessageId, resp.sessionId, trimmed),
        optimisticAssistantPlaceholder(resp.assistantMessageId, resp.sessionId),
      ]);
      onSidebarShouldReload?.();
    } catch (e: unknown) {
      console.error("[chat] edit failed", e);
      const { msg, detail } = splitErrorDetail(e);
      setNotice({
        kind: "error",
        text: t("chatPanel.errors.edit", { msg }),
        detail,
      });
    }
  }

  // ── render ──
  const showNewSessionPrompt = !sessionId && newSessionAgent;
  // 不可对话 Agent（chattable===false）的新 tab：输入框上方内联引导块 + 禁用 composer。
  // 用 === false 判定：newSessionAgent 缺 chattable 字段（如测试/部分数据）时保持现状可用。
  const showNewSessionGuard = Boolean(
    showNewSessionPrompt && newSessionAgent?.chattable === false,
  );
  const showEmpty = !sessionId && !newSessionAgent;

  return (
    <TooltipProvider delayDuration={200}>
      {/* Wails 事件订阅器现在挂在 App 顶层的 <ChatStreamsHost />,跨路由长存,
          ChatPanel 这里只读 store 状态、不再自己订阅。 */}

      {showEmpty ? (
        (emptyState ?? null)
      ) : (
        // 关键：showNewSessionPrompt → 已有会话 切换时，<ChatComposer> 必须保持挂载，
        // 否则 TipTap editor 实例随子树卸载重建，用户刚发完首条消息焦点就跑了。
        // 布局：toolbar 整行铺顶（无 newSessionPrompt 时），下面 flex row 分两栏 ——
        //   左栏 flex-col：transcript（或新会话占位）+ notice + ChatComposer，
        //   右栏：ChatContextSidebar 占满整高（从 toolbar 下沿一直到底）。
        //   ChatComposer 固定在左栏的最后一个 child 位置，跨分支保持同一 React 实例。
        <main className="flex min-h-0 min-w-0 flex-1 flex-col bg-background">
          {/* ── Toolbar / Header ── */}
          {/* 单行 meta：breadcrumb · dot+agent · relativeTime · device(tooltip cwd)。
              原先三行 (breadcrumb / agent+RUNNING / device+cwd) 在 44-52px 内压缩到
              一行；状态文案 (RUNNING/WAITING) 用裸 dot 的颜色携带，去掉大写 mono 标签。
              cwd 太长不适合常驻一行，挪到 DeviceTag 的 tooltip。 */}
          {showNewSessionPrompt ? null : (
            <div
              role="toolbar"
              aria-label={t("chatPanel.toolbar.aria")}
              className="flex min-h-[44px] shrink-0 items-center gap-3 border-b border-border px-5 py-1.5"
            >
              {session
                ? (() => {
                    const rawStatus: AgentStatus =
                      (session.agentStatus as AgentStatus) || "idle";
                    const toolbarStatus = reasonToDisplayStatus(
                      attentionReason,
                      rawStatus,
                    );
                    const statusTone =
                      statusConfig[toolbarStatus].textClassName;
                    return (
                      <>
                        <AgentAvatar
                          name={session.agentName}
                          initials={session.agentName.charAt(0)}
                          color={
                            (session.agentColor as AgentColor) || "agent-1"
                          }
                          size="md"
                        />
                        <div className="min-w-0 flex-1">
                          <div
                            className="line-clamp-2 break-words text-sm font-semibold leading-snug"
                            title={session.title || t("chatPanel.untitled")}
                          >
                            {session.title || t("chatPanel.untitled")}
                          </div>
                          <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0 font-mono text-2xs text-muted-foreground">
                            {effectiveTopline ? (
                              <>
                                <span className="inline-flex min-w-0 items-center text-primary-text">
                                  {effectiveTopline}
                                </span>
                                <span className="text-border-strong">·</span>
                              </>
                            ) : null}
                            <span className="inline-flex shrink-0 items-center gap-1">
                              <StatusDot status={toolbarStatus} size="xs" />
                              <span className={cn(statusTone)}>
                                {session.agentName}
                              </span>
                            </span>
                            <span className="text-border-strong">·</span>
                            <span className="shrink-0">
                              {relativeTime(session.lastMessageAt)}
                            </span>
                            {/* 机器 chip 守卫（R15/R20）：远端会话今天就显示；
                                多档 Agent（execTargetCount > 1）的本机会话也总是
                                显示——本机走 DeviceTag 既有的 deviceId === "" 分支
                                （组件本来就实现了这一支，只是这里以前没调用）。
                                单档 Agent 的本机会话维持今天"什么都不显示"的行为。 */}
                            {session.deviceID || session.execTargetCount > 1 ? (
                              <>
                                <span className="text-border-strong">·</span>
                                {session.cwd ? (
                                  <Tooltip>
                                    <TooltipTrigger asChild>
                                      <DeviceTag
                                        deviceId={session.deviceID}
                                        deviceName={
                                          session.deviceName || session.deviceID
                                        }
                                        online={session.online ?? true}
                                      />
                                    </TooltipTrigger>
                                    <TooltipContent
                                      side="bottom"
                                      className="max-w-[480px] break-all font-mono text-[11px]"
                                    >
                                      {session.cwd}
                                    </TooltipContent>
                                  </Tooltip>
                                ) : (
                                  <DeviceTag
                                    deviceId={session.deviceID}
                                    deviceName={
                                      session.deviceName || session.deviceID
                                    }
                                    online={session.online ?? true}
                                  />
                                )}
                              </>
                            ) : null}
                          </div>
                        </div>
                        {/* 后台任务胶囊：有运行中任务时显示，点击展开只读弹层 */}
                        <BackgroundTasksChip
                          tasks={backgroundTasks}
                          onClearCompleted={handleClearCompleted}
                          onStopTask={
                            canStopBackgroundTask
                              ? (task) => handleStopSubagent(task.toolUseId)
                              : undefined
                          }
                        />
                        {(() => {
                          // canStop 双源：
                          //   1. currentStream !== null —— 本客户端刚起的 turn，
                          //      openStream 是同步 store 写，比 useChatSession.reload
                          //      早；解决 Regenerate / Edit / Send-existing 的「服务端
                          //      已 running 但前端 agentStatus 还是上轮 idle」窗口。
                          //   2. status === running/waiting —— 服务端权威态，覆盖
                          //      「app 重启时另一会话仍在跑」场景（store 里没 entry）。
                          const canStop =
                            currentStream !== null ||
                            toolbarStatus === "running" ||
                            toolbarStatus === "waiting";
                          return (
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              disabled={!canStop}
                              onClick={() => void doStop(session.id)}
                              title={
                                canStop
                                  ? t("chatPanel.toolbar.stopActiveTitle")
                                  : t("chatPanel.toolbar.stopInactiveTitle")
                              }
                            >
                              <Square
                                data-icon="inline-start"
                                aria-hidden="true"
                              />
                              {t("chatPanel.toolbar.stop")}
                            </Button>
                          );
                        })()}
                        <Button
                          type="button"
                          variant="outline"
                          size="icon-sm"
                          aria-label={t("chatPanel.toolbar.contextSidebar")}
                          onClick={() => setSidebarOpen(!sidebarOpen)}
                          title={
                            sidebarOpen
                              ? t("chatPanel.toolbar.hideContextSidebar")
                              : t("chatPanel.toolbar.showContextSidebar")
                          }
                        >
                          {sidebarOpen ? (
                            <PanelRightClose
                              data-icon="only"
                              aria-hidden="true"
                            />
                          ) : (
                            <PanelRight data-icon="only" aria-hidden="true" />
                          )}
                        </Button>
                        {/* Pencil Ozwj8 — More dropdown */}
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              type="button"
                              variant="outline"
                              size="icon-sm"
                              aria-label={t("common.moreActions")}
                            >
                              <MoreHorizontal
                                data-icon="only"
                                aria-hidden="true"
                              />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem
                              onClick={() =>
                                setPendingRename({
                                  id: session.id,
                                  draft: session.title,
                                })
                              }
                            >
                              {t("chatPanel.actions.rename")}
                            </DropdownMenuItem>
                            {(session.backendType === "claudecode" ||
                              session.backendType === "codex" ||
                              session.backendType === "piagent") && (
                              <DropdownMenuItem
                                onClick={() =>
                                  void handleCopyLaunchCommand(session.id)
                                }
                              >
                                {t("chatPanel.launchCommand.copy")}
                              </DropdownMenuItem>
                            )}
                            <DropdownMenuItem
                              className="text-destructive focus:text-destructive"
                              onClick={() => void handleDelete(session.id)}
                            >
                              {t("common.delete")}
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </>
                    );
                  })()
                : null}
            </div>
          )}

          {/* ── Body row: 左栏 chat / 右栏 sidebar 占满整高 ──
              输入框宽度 = transcript 宽度,与对话流同列;sidebar 从 toolbar 下沿一路顶到底。 */}
          <div className="flex min-h-0 min-w-0 flex-1">
            {/* ── Body: chat ── */}
            <div className="flex min-h-0 min-w-0 flex-1 flex-col">
              {showNewSessionPrompt ? (
                <div className="flex flex-1 items-center justify-center">
                  <div className="flex flex-col items-center gap-2 text-center">
                    <div className="text-sm font-semibold">
                      {newSessionProjectName
                        ? t("chatPanel.newProjectSession.title", {
                            agentName: newSessionAgent.name,
                            projectName: newSessionProjectName,
                          })
                        : t("chatPanel.newSession.title", {
                            name: newSessionAgent.name,
                          })}
                    </div>
                    {newSessionAgent ? (
                      <NewSessionExecTargetLine
                        agentId={newSessionAgent.id}
                        agentName={newSessionAgent.name}
                        projectId={newSessionContext?.projectId ?? 0}
                        overrideBackendId={execTargetOverride}
                        onOverride={setExecTargetOverride}
                        onOverrideBackendType={setOverrideBackendType}
                        onEffectiveTarget={setEffectiveTarget}
                      />
                    ) : null}
                  </div>
                </div>
              ) : (
                <section
                  ref={setTranscriptNode}
                  data-testid="chat-transcript-scroll"
                  onScroll={handleTranscriptScroll}
                  className="min-h-0 flex-1 overflow-auto px-7 py-6"
                >
                  {sessionError ? (
                    (() => {
                      const loadMsg = splitErrorDetail(sessionError).msg;
                      // 后端 ChatSessionNotFound 的 zh 文案。不写字面量,用码点拼 ——
                      // i18n 守卫会把任何 Han 字符串字面量当硬编码 UI copy 拦下;
                      // 这是匹配动态后端错误文本,不是 UI copy,不进 t()。
                      const chatSessionNotFoundZh = String.fromCharCode(
                        0x4f1a, // 会
                        0x8bdd, // 话
                        0x4e0d, // 不
                        0x5b58, // 存
                        0x5728, // 在
                      );
                      const loadNotFound =
                        loadMsg.includes("Chat session not found") ||
                        loadMsg.includes(chatSessionNotFoundZh);
                      return (
                        <div
                          role="alert"
                          aria-label={t("chatPanel.loadError.title")}
                          className="flex w-full max-w-measure flex-col gap-3 rounded-lg border border-status-error/40 bg-destructive-soft px-4 py-4"
                        >
                          <div className="flex items-center gap-2">
                            <TriangleAlert
                              className="size-4 shrink-0 text-status-error"
                              aria-hidden="true"
                            />
                            <span className="text-sm font-semibold text-status-error">
                              {t("chatPanel.loadError.title")}
                            </span>
                          </div>
                          <p
                            data-selectable-text="true"
                            className="text-aux text-status-error"
                          >
                            {loadMsg}
                          </p>
                          {loadNotFound ? (
                            <p className="text-meta text-status-error">
                              {t("chatPanel.loadError.notFoundHint")}
                            </p>
                          ) : null}
                          <div className="flex items-center gap-2">
                            <Button
                              type="button"
                              size="xs"
                              onClick={() => void reloadSession()}
                            >
                              {t("chatPanel.loadError.retry")}
                            </Button>
                            <Button
                              type="button"
                              size="xs"
                              variant="ghost"
                              onClick={() => onSessionDeleted?.()}
                            >
                              {t("chatPanel.loadError.close")}
                            </Button>
                          </div>
                        </div>
                      );
                    })()
                  ) : sessionLoading && !session && messages.length === 0 ? (
                    <div
                      role="status"
                      aria-label={t("chatPanel.loading.aria")}
                      className="flex flex-col gap-3 py-2"
                    >
                      <div className="h-10 w-2/3 rounded-lg bg-muted" />
                      <div className="h-6 w-1/2 rounded-md bg-muted" />
                      <div className="h-24 w-full rounded-lg bg-muted" />
                      <div className="h-6 w-3/4 rounded-md bg-muted" />
                    </div>
                  ) : (
                    <>
                      <ChatTranscript
                        ref={transcriptHandleRef}
                        agentName={session?.agentName ?? "Agent"}
                        agentColor={
                          (session?.agentColor as AgentColor) || "agent-1"
                        }
                        cwd={session?.cwd}
                        sessionId={session?.id ?? 0}
                        scrollElement={transcriptElement}
                        virtualize
                        active={active}
                        messages={messages}
                        liveByMessageId={liveByMessageId}
                        streaming={streaming}
                        liveCompacting={liveCompacting}
                        reconnecting={reconnecting}
                        onContinue={() => {
                          if (!session) return;
                          void doSend(session.id, session.agentId, {
                            text: "continue",
                          });
                        }}
                        onRerun={(messageId) =>
                          void handleRegenerate(messageId)
                        }
                        onEdit={(messageId) => handleEdit(messageId)}
                        onPlanActionStarted={handlePlanActionStarted}
                        onStopSubagent={
                          canStopBackgroundTask ? handleStopSubagent : undefined
                        }
                        onStopLocalCommand={handleStopLocalCommand}
                        tabStateKey={scrollStateKey}
                      />
                      {showBackToBottom ? (
                        <TranscriptJumpControl
                          catchUp={catchUp}
                          onJump={handleBackToBottom}
                        />
                      ) : null}
                    </>
                  )}
                </section>
              )}

              {/* ── Inline notice (取代 window.alert)。
                    error / info 两种态共用一个 slot，最多挂一条；右侧 × 关闭。
                    info 用 default Alert 样式（中性），error 用 destructive。 */}
              {notice ? (
                <div className="border-t border-border bg-background px-5 pt-2">
                  <Alert
                    variant={
                      notice.kind === "error" ? "destructive" : "default"
                    }
                    className="py-2 pr-2"
                  >
                    <TriangleAlert aria-hidden="true" />
                    <AlertDescription className="flex min-w-0 items-start gap-2">
                      <div className="flex min-w-0 flex-1 flex-col gap-1">
                        <span className="min-w-0 break-words text-xs leading-snug">
                          {notice.text}
                        </span>
                        {notice.detail ? (
                          <span
                            data-testid="notice-detail"
                            data-selectable-text="true"
                            className="min-w-0 break-words font-mono text-[11px] leading-snug opacity-80"
                          >
                            {notice.detail}
                          </span>
                        ) : null}
                        {notice.actions ? (
                          <div className="flex shrink-0 items-center gap-2 pt-1">
                            <Button
                              type="button"
                              size="xs"
                              variant="outline"
                              onClick={notice.actions.retry}
                            >
                              {t("chatPanel.sendRetry.retry")}
                            </Button>
                            <Button
                              type="button"
                              size="xs"
                              variant="ghost"
                              onClick={notice.actions.discard}
                            >
                              {t("chatPanel.sendRetry.discard")}
                            </Button>
                          </div>
                        ) : null}
                      </div>
                      <button
                        type="button"
                        aria-label={t("chatPanel.notice.close")}
                        onClick={() => setNotice(null)}
                        className="-mr-1 inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded-sm text-current opacity-70 transition-opacity hover:opacity-100"
                      >
                        <X className="size-3" aria-hidden="true" />
                      </button>
                    </AlertDescription>
                  </Alert>
                </div>
              ) : null}

              {/* 会话所在机器离线（R15b）：钉住的档在远端且当前离线，续轮不会改派——
                  给一条走得通的路，而不是让用户对着卡死的输入框干等。 */}
              {session && session.deviceID && session.online === false ? (
                <SessionOfflineBanner
                  deviceName={session.deviceName || session.deviceID}
                  onCreateNewSession={() =>
                    useChatTabsStore
                      .getState()
                      .openNewSession(
                        session.projectId ?? 0,
                        session.agentId,
                        "",
                      )
                  }
                />
              ) : null}

              {/* ── Composer ── */}
              {showNewSessionGuard && newSessionAgent ? (
                <NewSessionChatGuard agent={newSessionAgent} />
              ) : null}
              <ChatComposer
                ref={composerRef}
                sending={sendInFlight}
                editing={activeEditing !== null}
                editDraft={activeEditing?.text}
                onCancelEdit={() => setEditingMessage(null)}
                // 新建会话场景下，ChatPanel 的 key 变化让 Composer 重新挂载 → 自动抓焦点，
                // 用户一进来就能直接打字。续聊已有会话不抢焦点，避免打断侧栏切换的鼠标交互。
                // 不可对话 Agent 的输入框已禁用，不再抢焦点。
                autoFocusOnMount={!!newSessionAgent && !showNewSessionGuard}
                disabled={showNewSessionGuard}
                placeholder={
                  showNewSessionGuard
                    ? t("chatPanel.newSession.guard.placeholder")
                    : undefined
                }
                contextUsage={composerContextUsage}
                quotaUsage={quotaUsage}
                quotaDeviceLabel={quotaDeviceLabel}
                permissionModeSlot={
                  isModeSwitchable ? (
                    <PermissionModePill
                      mode={permissionMode.mode}
                      modes={permissionModeMeta.order}
                      onSelect={permissionMode.setMode}
                      errorMessage={permissionMode.error}
                      disabled={modeSwitchingDisabled}
                      runtimeKey={activeBackendType}
                      permissionModeAtLaunch={
                        permissionMode.permissionModeAtLaunch
                      }
                      hasActiveSession={permissionMode.hasActiveSession}
                    />
                  ) : null
                }
                modelSlot={
                  activeBackendType ? <ProviderPill {...providerPill} /> : null
                }
                onShiftTab={
                  isModeSwitchable && !modeSwitchingDisabled
                    ? permissionMode.cycleMode
                    : undefined
                }
                topSlot={
                  <>
                    <TaskProgressBar progress={taskProgress} />
                    <QueuedMessagesBar
                      queued={currentQueued ?? []}
                      onCancel={(id) => void doCancelQueued(sessionId, id)}
                      onClearAll={() => void doCancelQueued(sessionId, "")}
                      dropped={
                        droppedQueue && droppedQueue.sessionId === sessionId
                          ? droppedQueue
                          : null
                      }
                      onRestoreDropped={() =>
                        useQueuedMessagesStore.getState().restoreDropped()
                      }
                      onDiscardDropped={() =>
                        useQueuedMessagesStore.getState().dismissDropped()
                      }
                    />
                  </>
                }
                onSubmit={(message: ChatComposerSubmit | string) => {
                  message =
                    typeof message === "string" ? { text: message } : message;
                  const text = message.text.trim();
                  const images = message.images ?? [];
                  if (images.length > 0 && !supportsImageInput) {
                    setNotice({
                      kind: "error",
                      text: t("chatPanel.errors.imageUnsupported"),
                    });
                    return;
                  }
                  if (activeEditing) {
                    void confirmEdit(text);
                    return;
                  }
                  // `/new`:沿用当前会话(或未首发新 tab)的 agent / 项目,新开一个全新
                  // 空白会话 tab 并跳转过去;当前会话完全不受影响(不发消息、不压缩)。
                  // 真正的 DB 会话由新 tab 首发消息时惰性创建,与「+ / ⌘N 新建会话」一致。
                  if (isExactNewCommand(text)) {
                    const newAgentId =
                      session?.agentId ?? newSessionAgent?.id ?? 0;
                    if (newAgentId > 0) {
                      const newProjectId =
                        session?.projectId ?? newSessionContext?.projectId ?? 0;
                      useChatTabsStore
                        .getState()
                        .openNewSession(newProjectId, newAgentId, "");
                    }
                    return;
                  }
                  const goalCommand =
                    activeBackendType === "codex"
                      ? parseGoalCommand(text)
                      : null;
                  if (goalCommand) {
                    if (images.length > 0) {
                      setNotice({
                        kind: "error",
                        text: t("chatPanel.goal.imageUnsupported"),
                      });
                      return;
                    }
                    if (streaming) {
                      setNotice({
                        kind: "info",
                        text: t("chatPanel.goal.waitForTurn"),
                      });
                      return;
                    }
                    if (!sessionId) {
                      if (newSessionAgent && goalCommand.kind === "set") {
                        void doStartGoal(newSessionAgent.id, goalCommand);
                        return;
                      }
                      setNotice({
                        kind: "info",
                        text: t("chatPanel.goal.needsSession"),
                      });
                      return;
                    }
                    void doGoal(sessionId, session?.agentId ?? 0, goalCommand);
                    return;
                  }
                  if (supportsCompactRPC && isExactCompactCommand(text)) {
                    if (!sessionId) {
                      notifyCompactNeedsSession();
                      return;
                    }
                    if (streaming) {
                      notifyCompactWaitForTurn();
                      return;
                    }
                    if (images.length > 0) {
                      setNotice({
                        kind: "error",
                        text: t("chatPanel.compact.imageUnsupported"),
                      });
                      return;
                    }
                    void doCompact(sessionId);
                    return;
                  }
                  // 新建会话首发：targetSessionId=0，由 doSend 内的 RPC 返回真实 sessionId
                  // 并通过 onSessionCreated 回填到父 store；此时 composer 不会卸载（结构稳定）。
                  if (!sessionId && newSessionAgent) {
                    void doSend(0, newSessionAgent.id, message);
                    return;
                  }
                  if (streaming && sessionId > 0) {
                    if (images.length > 0) {
                      setNotice({
                        kind: "error",
                        text: t("chatPanel.errors.imageWhileStreaming"),
                      });
                      return;
                    }
                    // streaming 中：按回车走 Enqueue，把消息排队等下一个安全点注入。
                    void doEnqueue(sessionId, session?.agentId ?? 0, text);
                    return;
                  }
                  void doSend(sessionId, session?.agentId ?? 0, message);
                }}
                backendType={activeBackendType}
                agentId={session?.agentId ?? newSessionAgent?.id ?? 0}
                cwd={composerCwd}
                localCommandHistoryScope={localCommandHistoryScope}
                onCommandModeChange={handleLocalCommandModeChange}
                supportsImageInput={supportsImageInput}
                onRunCommand={(command) => runLocalCommand(sessionId, command)}
                onSlashRpc={(cmd) => {
                  console.warn(
                    `slash rpc not wired: cmd=${cmd.name} backend=${activeBackendType}`,
                  );
                }}
              />
            </div>
            {!showNewSessionPrompt && sidebarOpen ? (
              <ChatContextSidebar
                sessionId={session?.id ?? 0}
                messages={messages}
                activeMessageId={activeMessageId}
                cwd={session?.cwd ?? ""}
                remote={Boolean(session?.deviceID)}
                cwdUnavailableReason={session?.cwdUnavailableReason}
                projectId={session?.projectId}
                onCwdSpecified={() => void reloadSession()}
                onJumpToMessage={(mid) => {
                  transcriptHandleRef.current?.scrollToMessage(mid);
                }}
              />
            ) : null}
            {/* 最右一栏:文件预览面板。仅在选中了可预览文件时渲染(内部自行返回
                null),打开占位、关闭释放宽度;宽高记忆独立 persistenceKey。 */}
            <FilePreviewPanel sessionId={session?.id ?? 0} />
          </div>
        </main>
      )}
      <Dialog
        open={pendingRegenId !== null}
        onOpenChange={(open) => {
          if (!open) setPendingRegenId(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("chatPanel.regenerateDialog.title")}</DialogTitle>
          </DialogHeader>
          <DialogBody>
            <p className="text-sm text-muted-foreground">
              {t("chatPanel.regenerateDialog.description")}
            </p>
          </DialogBody>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPendingRegenId(null)}
            >
              {t("common.cancel")}
            </Button>
            <Button size="sm" onClick={() => void confirmRegenerate()}>
              {t("chatPanel.regenerateDialog.confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog
        open={pendingDeleteId !== null}
        onOpenChange={(open) => {
          if (!open) setPendingDeleteId(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("chatPanel.deleteDialog.title")}</DialogTitle>
          </DialogHeader>
          <DialogBody>
            <p className="text-sm text-muted-foreground">
              {t("chatPanel.deleteDialog.description")}
            </p>
          </DialogBody>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPendingDeleteId(null)}
            >
              {t("common.cancel")}
            </Button>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => void confirmDelete()}
            >
              {t("common.delete")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {/* Rename Dialog 取代旧 window.prompt：把 draft 提到 state 上，
          DialogClose 由 onOpenChange 统一管理（× / Esc / 取消 都走同一 setter）。 */}
      <Dialog
        open={pendingRename !== null}
        onOpenChange={(open) => {
          if (!open) setPendingRename(null);
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("chatPanel.renameDialog.title")}</DialogTitle>
          </DialogHeader>
          <DialogBody>
            <form
              id="rename-session-form"
              onSubmit={(e) => {
                e.preventDefault();
                void confirmRename();
              }}
            >
              <Input
                autoFocus
                value={pendingRename?.draft ?? ""}
                onChange={(e) =>
                  setPendingRename((prev) =>
                    prev ? { ...prev, draft: e.target.value } : prev,
                  )
                }
                placeholder={t("chatPanel.renameDialog.placeholder")}
                aria-label={t("chatPanel.renameDialog.nameAria")}
              />
            </form>
          </DialogBody>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPendingRename(null)}
            >
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              form="rename-session-form"
              size="sm"
              disabled={
                !pendingRename || pendingRename.draft.trim().length === 0
              }
            >
              {t("common.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </TooltipProvider>
  );
}

export { ChatPanel };
export type { ChatPanelProps };
