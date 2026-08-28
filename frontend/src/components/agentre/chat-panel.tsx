import * as React from "react";
import { Folder } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
  ContextMeter,
  MachineOfflineBanner,
  PermissionModePill,
  TooltipProvider,
  TranscriptJumpControl,
  TranscriptSkeleton,
  buildTranscriptRows,
  type PlanActionStream,
  useTranscriptScroll,
} from "@agentre-hub/agentre-ui";

import { useCCUsage } from "@/hooks/use-cc-usage";
import { useChatSession } from "@/hooks/use-chat-session";
import { useProjectTree } from "@/hooks/use-project-tree";
import { useVisibleMessageId } from "@/hooks/use-visible-message-id";
import { reasonToDisplayStatus } from "@/lib/attention-display";
import { splitErrorDetail } from "@/lib/error-detail";
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
  useChatStreamsStore,
  type ChatBlockData,
  type LiveStream,
} from "@/stores/chat-streams-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useQueuedMessagesStore } from "@/stores/queued-messages-store";
import { useSessionConnectionState } from "@/stores/session-conn-store";
import { useSessionReadStore } from "@/stores/session-read-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { useBackendCapabilities } from "./capability/use-backend-capabilities";
import { useSessionCapabilities } from "./capability/use-session-capabilities";
import {
  ChatComposer,
  ChatTranscript,
  QuotaMeter,
  type ChatComposerHandle,
  type ChatTranscriptHandle,
} from "./chat";
import { ChatContextSidebar } from "./chat-context-sidebar";
import { ChatPanelHeader } from "./chat-panel/chat-panel-header";
import { ChatPanelConfirmDialogs } from "./chat-panel/confirm-dialogs";
import { NewSessionChatGuard } from "./chat-panel/new-session-chat-guard";
import type { ChatPanelNotice } from "./chat-panel/notice";
import { ChatPanelNoticeAlert } from "./chat-panel/notice-alert";
import {
  markSessionRunning,
  optimisticAssistantPlaceholder,
  optimisticUser,
} from "./chat-panel/optimistic";
import { SessionLoadError } from "./chat-panel/session-load-error";
import {
  applySteerConsumed,
  applyStreamError,
  liveContentByMessageId,
  upsertMessage,
} from "./chat-panel/stream-view";
import { useAutonomousTurnEvents } from "./chat-panel/use-autonomous-turn-events";
import { useChatActions } from "./chat-panel/use-chat-actions";
import { useLocalCommandLauncher } from "./chat-panel/use-local-command-launcher";
import { useMessageActions } from "./chat-panel/use-message-actions";
import { FilePreviewPanel } from "./file-preview/file-preview-panel";
import {
  clearCatchUp,
  registerTranscriptRowCounter,
  useCatchUpSummary,
} from "./chat-panel-catchup-state";
import { computeComposerContextUsage } from "./chat-panel-context-usage";
import { usePermissionMode } from "./permission-mode";
import { ProviderPill, useProviderPill } from "./model-pill";
import { NewSessionExecTargetLine } from "./session-exec-target";
import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import { QueuedMessagesBar } from "./queued-messages-bar";
import { deriveBackgroundTasks } from "./background-tasks/derive";
import { deriveTaskProgress } from "./task-progress/derive";
import { TaskProgressBar } from "./task-progress/task-progress-bar";
import type { AgentColor, AgentStatus } from "./types";
import { agentTextColorClassName } from "./types";

import {
  MarkChatSessionRead,
  StopBackgroundTask,
} from "../../../wailsjs/go/app/App";
import type { chat_svc } from "../../../wailsjs/go/models";

type ChatAgentItem = chat_svc.ChatAgentItem;

const EMPTY_CLEARED: string[] = [];

// EMPTY_AUTONOMOUS_IDS:行数快照不关心「哪条消息是自主续轮」——那只影响首行要不要
// 挂 banner,不改行数。渲染路径自己会算真值。
const EMPTY_AUTONOMOUS_IDS: ReadonlySet<number> = new Set<number>();

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

  const [notice, setNotice] = React.useState<ChatPanelNotice | null>(null);
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
    hasEarlierBlocks,
    loadingEarlierBlocks,
    loadEarlierBlocks,
  } = useChatSession(sessionId);
  // ChatComposer 命令式句柄:doSend 失败时用它恢复草稿（restoreDraft）/ 丢弃草稿
  // （clearDraft）。ChatComposer 内部已清空输入框,不恢复用户刚打的内容会永久丢失。
  const composerRef = React.useRef<ChatComposerHandle>(null);
  // 发送 RPC 在途时禁用发送按钮并显示 spinner。
  const [sendInFlight, setSendInFlight] = React.useState(false);

  const { reason: attentionReason } = useSessionAttention(sessionId);

  // 自主续轮(后台任务完成后 CLI 自己跑的一轮)与后台 subagent 活动轮的会话级旁路
  // 订阅整块住在 useAutonomousTurnEvents 里。
  useAutonomousTurnEvents({
    sessionId,
    openStream,
    setMessages,
    setNotice,
  });

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
  // 滚动几何(贴底跟随意图 / 折叠恢复守卫 / 快照存取 / 回到底部 / 下面还有 N 轮)整块
  // 住在共享包的 useTranscriptScroll 里 —— 那是纯 DOM 工作,不认 Wails 也不认 store。
  // 这里只留转录渲染器的句柄;接线在下面 liveByMessageId 备好之后。
  const transcriptHandleRef = React.useRef<ChatTranscriptHandle>(null);
  const sidebarOpen = useChatSidebarStore((s) => s.open);
  const setSidebarOpen = useChatSidebarStore((s) => s.setOpen);

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

  // 宿主只递 tab / 活跃态 / 消息与流式内容的身份进去,并把锚点恢复转交给转录渲染器
  // (只有虚拟器知道某条消息此刻落在哪一行)。
  const {
    followBottom: followTranscriptBottom,
    onScroll: handleTranscriptScroll,
    scrollElement: transcriptElement,
    scrollRef: transcriptRef,
    scrollToBottom: handleBackToBottom,
    setScrollElement: setTranscriptNode,
    showBackToBottom,
    turnsBelow,
  } = useTranscriptScroll({
    active,
    liveRevision: liveByMessageId,
    messages,
    scrollToAnchor: (anchorId, anchorOffset, anchorRowKey) =>
      transcriptHandleRef.current?.scrollToAnchor(
        anchorId,
        anchorOffset,
        anchorRowKey,
      ) ?? false,
    sessionId,
    tabKey: scrollStateKey,
  });
  // 右侧 outline 高亮联动：跟踪 transcript 当前视野焦点对应的 message id。
  const activeMessageId = useVisibleMessageId(transcriptRef);
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
  // 本地命令那一族(起停 PTY、命令卡结算、命令历史检索范围、未首发会话的惰性建会
  // 话)整块住在 useLocalCommandLauncher 里。
  const {
    localCommandHistoryScope,
    handleLocalCommandModeChange,
    handleStopLocalCommand,
    runLocalCommand,
  } = useLocalCommandLauncher({
    sessionId,
    session,
    newSessionAgent,
    newSessionProjectId: newSessionContext?.projectId,
    composerCwd,
    onSessionCreated,
    onSidebarShouldReload,
    setNotice,
  });
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

  // 重生成 / 编辑 / 删除 / 改名这一族(确认弹窗 state + RPC + 乐观截断重排)整块
  // 住在 useMessageActions 里。
  const {
    pendingRegenId,
    setPendingRegenId,
    handleRegenerate,
    confirmRegenerate,
    pendingDeleteId,
    setPendingDeleteId,
    handleDelete,
    confirmDelete,
    pendingRename,
    setPendingRename,
    confirmRename,
    activeEditing,
    setEditingMessage,
    handleEdit,
    confirmEdit,
  } = useMessageActions({
    sessionId,
    messages,
    setMessages,
    isModeSwitchable,
    permissionModeValue: permissionMode.mode,
    followTranscriptBottom,
    openStream,
    onSessionDeleted,
    onSidebarShouldReload,
    setNotice,
  });

  // composer / 头部触发的那一族会话 RPC(首发与续发、压缩、goal、排队与撤销、软中断、
  // 复制启动命令)与回车的分派整块住在 useChatActions 里。
  const {
    doSend,
    doStop,
    doCancelQueued,
    handleCopyLaunchCommand,
    handleComposerSubmit,
  } = useChatActions({
    sessionId,
    session,
    newSessionAgent,
    newSessionContext,
    setMessages,
    reloadSession,
    openStream,
    followTranscriptBottom,
    composerRef,
    setSendInFlight,
    setNotice,
    onSessionCreated,
    onPeerSessionCreated,
    onSidebarShouldReload,
    streaming,
    activeBackendType,
    isModeSwitchable,
    permissionModeValue: permissionMode.mode,
    supportsImageInput,
    supportsCompactRPC,
    execTargetOverride,
    effectiveTarget,
    providerKey: providerPill.providerKey,
    modelKey: providerPill.modelKey,
    editing: activeEditing !== null,
    confirmEdit,
  });

  // prop 优先，无 prop 时降级到内部派生值。
  const effectiveTopline = headerTopline ?? derivedTopline;

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
      //
      // 但不能只靠 reload:finishStream 是同步的,liveDelta / liveBlocks 当场清零,
      // 而 messages 里那条 assistant 还是发送时插的空占位(blocks: [])——
      // 中间那段 LoadChatSession 往返里,最后一轮的正文整段消失、行数塌陷,
      // 响应回来才重新长出来。done 事件本身就带着最终 assistant 消息
      // (chat_svc 的 `ChatStreamEvent{Kind: StreamDone, Message: final}`),
      // 先同步落表,空窗就没了。reload 仍要发 —— 本轮可能还改了别的行
      // (user 消息、subagent 子行、审批块),done 只覆盖 assistant 那一条。
      if (ev.message) {
        setMessages((prev) => upsertMessage(prev, ev.message!));
      }
      void reloadSession();
      onSidebarShouldReload?.();
    } else if (ev.kind === "error") {
      // 错误路径:后端同样 Update 过 assistant.errorText,但有可能 final message 已附带
      // ev.message。两条路都靠 reload 把最新落库状态拿回来;再补 errorText 落到 UI。
      if (ev.message) {
        setMessages((prev) => upsertMessage(prev, ev.message!));
      } else if (ev.error) {
        setMessages((prev) => applyStreamError(prev, ev.error));
      }
      void reloadSession();
      onSidebarShouldReload?.();
    } else if (ev.kind === "aborted") {
      // 用户主动「停止」：后端已经把 partial 内容写入 DB 且 errorText 为空。
      // 走和 done 一样的路径:事件自带 partial 消息就先同步落表(同样是为了不
      // 在等 reload 的这段里把已经生成的内容闪没),再 reload 兜其余的行；
      // 不调 MarkRead（abort 不是「用户已读完」语义）。
      if (ev.message) {
        setMessages((prev) => upsertMessage(prev, ev.message!));
      }
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

  // handlePlanActionStarted 不做 memo:它经 useTranscriptCallbacks 的 useEvent 代理
  // 进转录(每渲染更新 ref、对外恒是同一个稳定引用),父侧引用变不变都到不了行组件。
  function handlePlanActionStarted(resp: PlanActionStream, userText: string) {
    if (!resp.stream || !resp.sessionId || !resp.assistantMessageId) return;
    followTranscriptBottom();
    setMessages((prev) => {
      const next = [...prev];
      if (!next.some((m) => m.id === resp.userMessageId)) {
        next.push(optimisticUser(resp.userMessageId, resp.sessionId, userText));
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
  }

  // ── render ──
  const showNewSessionPrompt = !sessionId && newSessionAgent;
  // 不可对话 Agent（chattable===false）的新 tab：输入框上方内联引导块 + 禁用 composer。
  // 用 === false 判定：newSessionAgent 缺 chattable 字段（如测试/部分数据）时保持现状可用。
  const showNewSessionGuard = Boolean(
    showNewSessionPrompt && newSessionAgent?.chattable === false,
  );
  const showEmpty = !sessionId && !newSessionAgent;

  // ── 头部 ──
  // 头部在「已有会话 / 新建未首发 / 加载中 / 加载失败」四态都渲染，高度写死为两行
  // 标题的高度、内容整块垂直居中：首发消息落地、切 tab、标题长短都不再顶动版面
  // （规格 2026-08-23 决策 2/3）。
  const headerStatus = reasonToDisplayStatus(
    attentionReason,
    (session?.agentStatus as AgentStatus) || "idle",
  );
  // canStop 双源：
  //   1. currentStream !== null —— 本客户端刚起的 turn，openStream 是同步 store 写，
  //      比 useChatSession.reload 早；解决 Regenerate / Edit / Send-existing 的「服务端
  //      已 running 但前端 agentStatus 还是上轮 idle」窗口。
  //   2. status === running/waiting —— 服务端权威态，覆盖「app 重启时另一会话仍在跑」
  //      场景（store 里没 entry）。
  const canStop =
    currentStream !== null ||
    headerStatus === "running" ||
    headerStatus === "waiting";
  const headerTitle = session
    ? session.title || t("chatPanel.untitled")
    : showNewSessionPrompt
      ? t("chatPanel.header.newSessionTitle", { name: newSessionAgent.name })
      : sessionError
        ? t("chatPanel.header.unavailableTitle")
        : t("chatPanel.loading.aria");
  // meta 行的三段：项目/分支 · 谁在跑 · 多久没动（新建态换成将要用的后端与模型）。
  const headerToplineNode =
    effectiveTopline ??
    (showNewSessionPrompt && newSessionProjectName
      ? newSessionProjectName
      : null);
  const headerAgentName = session
    ? session.agentName
    : showNewSessionPrompt
      ? newSessionAgent.name
      : "";
  // 输入带的边界只在真的有转录可滚时才有话说：新建会话未首发时没有转录，
  // 上一条会话残留的滚动位置不该在这里画一条线。
  const showBandEdge = showBackToBottom && !showNewSessionPrompt;
  // 还没有任何转录可看：加载中且既无会话也无消息。骨架与滚动带的 busy 语义共用
  // 这一个判定，两者因此不会各说各话。
  const awaitingTranscript =
    sessionLoading && !session && messages.length === 0;
  const headerMetaTail = session
    ? relativeTime(session.lastMessageAt)
    : showNewSessionPrompt
      ? providerPill.resolvedModelLabel || newSessionAgent.backendType
      : "";

  return (
    <TooltipProvider delayDuration={200}>
      {/* Wails 事件订阅器现在挂在 App 顶层的 <ChatStreamsHost />,跨路由长存,
          ChatPanel 这里只读 store 状态、不再自己订阅。 */}

      {showEmpty ? (
        (emptyState ?? null)
      ) : (
        // 关键：showNewSessionPrompt → 已有会话 切换时，<ChatComposer> 必须保持挂载，
        // 否则 TipTap editor 实例随子树卸载重建，用户刚发完首条消息焦点就跑了。
        // 布局：头部整行铺顶（四种会话情形都在），下面 flex row 分两栏 ——
        //   左栏 flex-col：transcript（或新会话占位）+ notice + ChatComposer，
        //   右栏：ChatContextSidebar 占满整高（从 toolbar 下沿一直到底）。
        //   ChatComposer 固定在左栏的最后一个 child 位置，跨分支保持同一 React 实例。
        <main className="flex min-h-0 min-w-0 flex-1 flex-col bg-background">
          <ChatPanelHeader
            session={session}
            newSessionAgent={newSessionAgent}
            showNewSessionPrompt={Boolean(showNewSessionPrompt)}
            title={headerTitle}
            status={headerStatus}
            topline={headerToplineNode}
            agentName={headerAgentName}
            metaTail={headerMetaTail}
            backgroundTasks={backgroundTasks}
            onClearCompletedTasks={handleClearCompleted}
            onStopTask={
              canStopBackgroundTask
                ? (task) => handleStopSubagent(task.toolUseId)
                : undefined
            }
            canStop={canStop}
            onStop={(id) => void doStop(id)}
            sidebarOpen={sidebarOpen}
            onToggleSidebar={() => setSidebarOpen(!sidebarOpen)}
            onRename={(target) =>
              setPendingRename({ id: target.id, draft: target.title })
            }
            onCopyLaunchCommand={(id) => void handleCopyLaunchCommand(id)}
            onDelete={(id) => handleDelete(id)}
          />

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
                  // 「下面还会变」由这一条 busy 说，骨架自己对读屏隐身（决策 9）。
                  aria-busy={awaitingTranscript}
                  className="min-h-0 flex-1 overflow-auto px-7 pt-6"
                >
                  {sessionError ? (
                    <SessionLoadError
                      error={sessionError}
                      onRetry={() => void reloadSession()}
                      onClose={() => onSessionDeleted?.()}
                    />
                  ) : awaitingTranscript ? (
                    <TranscriptSkeleton className="ml-10 max-w-measure py-2" />
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
                        hasEarlierMessages={hasEarlierBlocks}
                        loadingEarlier={loadingEarlierBlocks}
                        onLoadEarlier={() => void loadEarlierBlocks()}
                        liveByMessageId={liveByMessageId}
                        fallbackModel={providerPill.resolvedModelLabel}
                        streaming={streaming}
                        liveCompacting={liveCompacting}
                        reconnecting={reconnecting}
                        onContinue={() => {
                          if (!session) return;
                          void doSend(session.id, session.agentId, {
                            text: "continue",
                          });
                        }}
                        onRerun={(messageId) => handleRegenerate(messageId)}
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
                          turnsBelow={turnsBelow}
                          onJump={handleBackToBottom}
                          // 这一端的转录列靠左（滚动容器是整面板宽的 px-7，列本身
                          // 让出 40px 头像 gutter 再封顶 max-w-measure，没有
                          // mx-auto）。药丸要与消息正文、输入框共用一条中线，就得
                          // 按这条列算 —— 与 chat-composer-column 同一组类名。
                          className="ml-10 max-w-measure"
                        />
                      ) : null}
                    </>
                  )}
                </section>
              )}

              {/* ── 输入带 ──
                    转录、通知、离线横幅、守卫、输入框共用同一条列（决策 5）：左右
                    内边距与转录同值，内容让出 28px 头像列 + gap-3 再封顶
                    --container-measure —— 输入框的第一个字符与消息正文的第一个字符
                    落在同一条竖线上。
                    边界跟随贴底（决策 6）：贴底 = 一整片，没有分隔线也没有渐隐；
                    未贴底 = border-top + 一段向上渐隐把末行压掉一半，读作"下面还有"。
                    信号复用 showBackToBottom（与「回到底部」浮层同条件），零新状态。 */}
              <div
                data-testid="chat-composer-band"
                data-scrolled={showBandEdge ? "true" : "false"}
                className={cn(
                  "relative shrink-0 px-7 pt-2 pb-4",
                  showBandEdge && "border-t border-border",
                )}
              >
                {showBandEdge ? (
                  <div
                    data-testid="chat-composer-band-fade"
                    aria-hidden="true"
                    className="pointer-events-none absolute inset-x-0 -top-3.5 h-3.5 bg-gradient-to-t from-background to-transparent"
                  />
                ) : null}
                <div
                  data-testid="chat-composer-column"
                  className="ml-10 flex max-w-measure flex-col gap-2"
                >
                  {notice ? (
                    <ChatPanelNoticeAlert
                      notice={notice}
                      onDismiss={() => setNotice(null)}
                    />
                  ) : null}

                  {/* 会话所在机器离线（R15b）：钉住的档在远端且当前离线，续轮不会改派——
                  给一条走得通的路，而不是让用户对着卡死的输入框干等。 */}
                  {session && session.deviceID && session.online === false ? (
                    <MachineOfflineBanner
                      machineName={session.deviceName || session.deviceID}
                      onStartNew={() =>
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
                    // 底栏：设置项（pills）跟在快捷键提示后，计量器贴着提交键 ——
                    // 「发之前看一眼还剩多少」的读序，与 agentre-server 同一套。
                    leadingControls={
                      isModeSwitchable || activeBackendType ? (
                        <div className="flex shrink-0 items-center gap-1">
                          {isModeSwitchable ? (
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
                          ) : null}
                          {activeBackendType ? (
                            <ProviderPill {...providerPill} />
                          ) : null}
                        </div>
                      ) : null
                    }
                    trailingControls={
                      <>
                        <QuotaMeter
                          data={quotaUsage}
                          deviceLabel={quotaDeviceLabel}
                        />
                        {composerContextUsage &&
                        composerContextUsage.max > 0 ? (
                          <ContextMeter
                            used={composerContextUsage.used}
                            max={composerContextUsage.max}
                          />
                        ) : null}
                      </>
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
                    onSubmit={handleComposerSubmit}
                    backendType={activeBackendType}
                    agentId={session?.agentId ?? newSessionAgent?.id ?? 0}
                    cwd={composerCwd}
                    localCommandHistoryScope={localCommandHistoryScope}
                    onCommandModeChange={handleLocalCommandModeChange}
                    supportsImageInput={supportsImageInput}
                    onCommandSubmit={(command: string) =>
                      runLocalCommand(sessionId, command)
                    }
                    onSlashRpc={(cmd) => {
                      console.warn(
                        `slash rpc not wired: cmd=${cmd.name} backend=${activeBackendType}`,
                      );
                    }}
                  />
                </div>
              </div>
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
            <FilePreviewPanel
              sessionId={session?.id ?? 0}
              messages={messages}
              cwd={session?.cwd ?? ""}
            />
          </div>
        </main>
      )}
      <ChatPanelConfirmDialogs
        pendingRegenId={pendingRegenId}
        setPendingRegenId={setPendingRegenId}
        onConfirmRegenerate={() => void confirmRegenerate()}
        pendingDeleteId={pendingDeleteId}
        setPendingDeleteId={setPendingDeleteId}
        onConfirmDelete={() => void confirmDelete()}
        pendingRename={pendingRename}
        setPendingRename={setPendingRename}
        onConfirmRename={() => void confirmRename()}
      />
    </TooltipProvider>
  );
}

export { ChatPanel };
export type { ChatPanelProps };
