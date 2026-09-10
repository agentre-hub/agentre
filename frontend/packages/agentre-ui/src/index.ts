/**
 * `@agentre-hub/agentre-ui` —— Agentre 桌面端与 agentre-server 共用的前端层。
 *
 * 四个消费面：
 *   - `@agentre-hub/agentre-ui/tokens.css`         —— design tokens（无需 import 本模块）
 *   - `@agentre-hub/agentre-ui/code-highlight.css` —— CodeBlock 的 highlight.js 配色
 *   - `@agentre-hub/agentre-ui/i18n`               —— 语言包 + namespace（不拖组件树）
 *   - `@agentre-hub/agentre-ui`                    —— 对话流渲染器、数据契约与语言包
 */
export {
  AGENTRE_UI_NAMESPACE,
  agentreUiResources,
  useUiTranslation,
} from "./i18n";
export { cn } from "./lib/utils";
export { LlmProvidersPanel, AgentBackendsPanel } from "./engine/panels";
// 引擎设置端口经 context 下发：两个面板同时挂载时各用各的那一份，与谁最后渲染无关。
export {
  EngineSettingsPortsProvider,
  useEngineSettingsPorts,
} from "./engine/ports-context";
export {
  AgentBackendLogo,
  LlmModelLogo,
  LlmProviderLogo,
  resolveModelBrand,
} from "./engine/ai-brand-logo";
// 设备的端口转发小节：桌面端设备行下的子块与控制台设备卡展开区渲染同一份行。
export { PortForwardSection } from "./port-forward/port-forward-section";
export type { PortForwardMappingView } from "./port-forward/port-forward-section";
export { PermissionModePill } from "./permission-mode";
export {
  isPermissionModeDisabled,
  nextPermissionMode,
  normalizePermissionMode,
} from "./permission-mode";
export type { PermissionMode } from "./permission-mode";
export { ModelTargetPicker } from "./engine/model-target-picker";
export {
  ProviderPillResolution,
  ProviderPillTrigger,
} from "./engine/model-target-picker";
export type { ProviderPillState } from "./engine/model-target-picker";
export { resolveProviderPillState } from "./engine/model-target-picker";
export {
  readRecentTargets,
  recordRecentTarget,
  removeRecentTarget,
  recentStorageKey,
} from "./engine/model-target-picker/recents";
export {
  buildPickerCatalog,
  providerCompatibleForBackend,
} from "./engine/model-target-picker";
export type {
  ModelTarget,
  PickerProvider,
} from "./engine/model-target-picker/types";
export type {
  BackendView,
  EngineID,
  EngineSettingsPorts,
  ModelView,
  ProviderView,
} from "./engine/ports";
// 引擎设置面的公共零件：对话框外壳、执行设备判据、后端 flash 文本截断。桌面此前
// 各留一份逐行同构的副本，收敛后两端同取包里这一份（见 src/components/agentre/
// __tests__/shared-package-single-source.test.ts）。
export { AgentreDialog } from "./engine/app-dialog";
export { resolveExecutionDevice } from "./engine/device-identity";
// agent 调色板的 token 词汇表（与 tokens.css 同源）+ token → css 变量。
export { agentColorOrder, tokenToCssColor } from "./lib/agent-color";
export type { AgentColor } from "./lib/agent-color";
export { copyTextToClipboard, copyTextWithToast } from "./lib/clipboard-toast";
export { AgentAvatar, getAgentInitials } from "./ui/agent-avatar";
export { Alert, AlertTitle, AlertDescription } from "./ui/alert";
export { Badge } from "./ui/badge";
export { Button, buttonVariants } from "./ui/button";
export { Checkbox } from "./ui/checkbox";
export {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogBody,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "./ui/dialog";
export {
  DialogShell,
  DialogShellBody,
  DialogShellFooter,
  DialogShellHeader,
  DialogShellSubmit,
} from "./ui/dialog-shell";
export {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubTrigger,
  DropdownMenuSubContent,
} from "./ui/dropdown-menu";
export {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from "./ui/context-menu";
// engine/ 下最后四件私拷（field / radio-group / switch / table）回并到这里，
// 连同 Field 自己的两个零件 Label 与 Separator——`field` 那份私拷漏掉了
// orientation，把调用方写的横排静默改成了竖排。回并后两个宿主与引擎面板同取这
// 一份（守卫见 ./ui/single-source.test.ts）。
export {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "./ui/field";
export { HoverCard, HoverCardContent, HoverCardTrigger } from "./ui/hover-card";
export { Input } from "./ui/input";
export { Label } from "./ui/label";
export {
  Popover,
  PopoverAnchor,
  PopoverContent,
  PopoverTrigger,
} from "./ui/popover";
export { RadioGroup, RadioGroupItem } from "./ui/radio-group";
export {
  Select,
  SelectValue,
  SelectTrigger,
  SelectContent,
  SelectItem,
  SELECT_NONE,
} from "./ui/select";
export { SearchInput } from "./ui/search-input";
export { ResizableSidebar } from "./ui/resizable-sidebar";
export { Separator } from "./ui/separator";
export { Skeleton } from "./ui/skeleton";
export { Spinner } from "./ui/spinner";
export { Switch } from "./ui/switch";
export {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table";
export { Textarea } from "./ui/textarea";
export { Toggle } from "./ui/toggle";
export {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "./ui/tooltip";
export type { LiveTurnInput } from "./transcript/turn-stats";
export { classifyLink } from "./lib/link-classify";
export {
  previewKind,
  resolvePreviewRelPath,
  toRelPath,
} from "./lib/previewable";
// ── 文件预览的内容视图 ─────────────────────────────────────────────────────
// previewKind 判出来的四类内容各有一个渲染器：代码 / 文本与 markdown 源码走
// Monaco 只读（markdown 的渲染档是 MarkdownText），图片直接渲染，改动 diff 走
// Monaco 的并排 diff。Monaco 命名空间由宿主经 `monaco` prop 注入——装载器与
// worker 环境留在宿主（Vite `?worker` 进不了本包的纯 tsc 构建，见 ./file-preview/
// monaco.ts）。
export { monacoLanguageForPath } from "./file-preview/monaco-language";
// JSON 的 Monarch 词法：0.56 起 json 移出了 basic-languages，两端的装载器都要在
// 拿到命名空间之后补注册这一门语言。语法是纯数据 + 一次调用（对 monaco 只有类型
// 依赖），因此住在包里，而不是两个宿主各抄一份。
export { registerJsonLanguage } from "./file-preview/monaco-json";
export type { MonacoNS } from "./file-preview/monaco";
// 定位目标:PreviewAnchor 是链接里读出来的事实(包解析、交给宿主),
// PreviewRevealTarget 多一个宿主铸的 nonce(交回面板)。两端宿主都要用。
export type { PreviewAnchor, PreviewRevealTarget } from "./file-preview/anchor";
// 预览标签条：开着哪些标签、谁是活动标签、动作落到哪个 store，全部由宿主经 props
// 注入（桌面端是 file-preview-tabs-store，控制台是它自己的那份）。
export type { FilePreviewTab } from "./file-preview/preview-tab-strip";
// 预览面板：读取走注入的 ports、状态走 props，宿主只剩一层装配根。七个态（四类
// 内容 + tooLarge / binary / 文件不存在 / 对端离线）都在这里，两端一份实现。
export {
  FilePreviewPanel,
  previewNeedsMonaco,
} from "./file-preview/file-preview-panel";
export type { FilePreviewSegment } from "./file-preview/file-preview-panel";
// 失败归类留在宿主：它 reject 一个带 `kind` 的错误，面板据此分终态 / 可重试态。
export type {
  FilePreviewFailure,
  FilePreviewFailureKind,
  FilePreviewPorts,
  GitFileContentResult,
} from "./file-preview/ports";
// 与 `@agentre-hub/agentre-ui/base.css` 里的滚动条规则是一对：那半边把滑块颜色绑到
// --sb-thumb 并默认透明，这半边在滚动时改值。宿主两样都要接。
export { useAutoHideScrollbars } from "./hooks/use-auto-hide-scrollbars";
// 一键升级的状态机：两端（桌面端的设备行、控制台的设备卡）共用同一份态与迁移，
// 各端只注入自己的取数（Wails 绑定 / HTTP）。
export { useAgentredUpgrade } from "./hooks/use-agentred-upgrade";
export type {
  AgentredUpgrade,
  AgentredUpgradePhase,
} from "./hooks/use-agentred-upgrade";
export { isOpenInNewTabModifier } from "./lib/keyboard";
export { StatusDot } from "./ui/status-dot";
// ── 会话索引 ───────────────────────────────────────────────────────────────
// 行、分组容器，以及**轴投影本身**（规格 2026-08-18「共享包承载什么」把它收了
// 进来：组怎么分、怎么排、兜底组摆在哪只该有一份实现）。留在宿主的是各端的取数与
// 装配，以及**可选轴清单**——桌面端三档、server 控制台四档（决策 17）。
export { SessionGroup } from "./session-index/session-group";
export { SessionRow } from "./session-index/session-row";
export type {
  SessionAttentionRank,
  SessionRowModel,
} from "./session-index/types";
// 轴投影（纯函数）与组的形状契约 —— 两端共用的那一份。
export {
  buildAxisGroups,
  UNASSIGNED_PROJECT_KEY,
  UNKNOWN_MACHINE_KEY,
  UNNAMED_AGENT_KEY,
} from "./session-index/axis-groups";
export type {
  AgentInfo,
  AxisInput,
  IndexAxis,
  IndexGroup,
  IndexGroupRow,
  IndexRow,
  MachineInfo,
  ProjectNode,
} from "./session-index/axis-groups";
// 索引的零耦合呈现件：轴选择器、组头、行的前置槽与次行。
export { AxisPicker } from "./session-index/axis-picker";
export { ProjectGroupHeader } from "./session-index/project-group-header";
export { AgentGroupHeader } from "./session-index/agent-group-header";
export { MachineGroupHeader } from "./session-index/machine-group-header";
export { FreeGroupHeader } from "./session-index/free-group-header";
// 导入本地会话（规格 2026-08-26）：入口条目、候选列表、转录预览与对话框都归包，
// 宿主只提供 ports（发现 / 预览 / 写入 / 打开会话）。
export { ImportLocalSessionMenu } from "./session-import/import-menu";
export { ImportSessionDialog } from "./session-import/import-dialog";
export type { ImportDialogPrefill } from "./session-import/import-dialog";
export type {
  ImportCandidatesResult,
  ImportOutcome,
  ImportPreviewResult,
  SessionImportPorts,
} from "./session-import/ports";
export { OwnSessionsHeader } from "./session-index/own-sessions-header";
export { ProjectGlyph } from "./session-index/project-glyph";
export type { ProjectGlyphInfo } from "./session-index/project-glyph";
export {
  computeAttention,
  indexRowFromMeta,
  reasonToDisplayStatus,
  reasonToPillText,
  strongestAttentionTone,
} from "./session-index/attention";
export type {
  AttentionInput,
  AttentionReason,
} from "./session-index/attention";
export {
  lifecycleToAgentStatus,
  SessionLifecycle,
} from "./session-index/lifecycle-status";
export { RowLeadingSlot } from "./session-index/row-leading-slot";
export { RowSecondaryLine } from "./session-index/row-secondary-line";
export { mentionsToDisplayText } from "./chat-input/mentions/xml";
export type { MentionRef } from "./chat-input/mentions/xml";
// ── 聊天输入编辑器 ─────────────────────────────────────────────────────────
export { AIChatInput } from "./chat-input";
export type {
  AIChatInputHandle,
  LocalCommandSubmitHandler,
} from "./chat-input/types";
export { ChatComposer } from "./composer/chat-composer";
export type {
  ChatComposerDropZone,
  ChatComposerHandle,
  ChatComposerProps,
  ChatComposerSubmit,
  ChatImageAttachment,
} from "./composer/chat-composer";
export { ContextMeter } from "./composer/context-meter";
export { QueuedMessagesBar } from "./composer/queued-messages-bar";
// 排队队列的状态迁移:两个宿主共用同一份归约,只有键控与「被丢弃的字去哪儿」归宿主。
export {
  adoptSteerHandle,
  clearSteerQueue,
  consumeSteers,
  dropSteers,
  emptySteerQueue,
  enqueueSettledSteer,
  enqueueSteer,
  markSteerNotCancellable,
} from "./composer/steer-queue";
export type {
  ConsumedSteerRef,
  QueuedItem,
  SteerQueueState,
} from "./composer/steer-queue";
export { ReasoningEffortPicker } from "./composer/reasoning-effort-picker";
// 档位枚举与后端编辑器同源（engine/agent-backends-shared），两个宿主的 composer
// 接线都需要它。
export type { ReasoningEffortValue } from "./engine/agent-backends-shared";
export { usageLevel } from "./composer/usage-level";
export type { UsageLevel } from "./composer/usage-level";
export { formatTokens } from "./lib/format-tokens";
export { groupAgentsForPicking } from "./lib/agent-picking";
export { StatusBanner } from "./session-status/status-banner";
export type { StatusBannerTone } from "./session-status/status-banner";
export { MachineOfflineBanner } from "./session-status/machine-offline-banner";
export { SessionHeaderBand } from "./session-detail/header-band";
export type { SessionHeaderMetaPart } from "./session-detail/header-band";
export { buildMentionSources } from "./chat-input/mentions/build-sources";
// `/` 命令：机制与清单都在包里，宿主只递「问哪台机器拿到的 Skill 目录」和它
// 自己那几条命令（见 chat-input/slash/registry.ts 的文件头）。
export {
  SLASH_COMPACT,
  buildSlashCommands,
  isNativeCompactBackend,
  listAvailable,
  skillCommandPrefix,
  skillCommandsFromCatalog,
  useSlashCommands,
} from "./chat-input/slash/registry";
export type { SkillCommandSource } from "./chat-input/slash/registry";
export type { SlashCommand, SlashExec } from "./chat-input/slash/types";
// `!` Shell 历史：可选的宿主接缝（agentre-server 没有这条能力）。
export { LocalCommandHistoryProvider } from "./chat-input/local-command-history/access";
export type {
  LocalCommandHistoryAccess,
  LocalCommandHistoryScope,
} from "./chat-input/local-command-history/access";
export { scoreSuggestion } from "./lib/suggestion-score";
export type { SuggestionScoreInput } from "./lib/suggestion-score";
export {
  __resetChatPanelScrollStateForTesting,
  COLLAPSED_RESTORE_GUARD_MS,
  loadTranscriptScrollState,
  nextAutoFollow,
  pruneChatPanelScrollState,
} from "./transcript/chat-panel-scroll-state";
// 转录滚动几何(贴底跟随 / 折叠恢复 / 快照 / 回到底部)整块住在包里,宿主只接线。
export { useTranscriptScroll } from "./transcript/use-transcript-scroll";
// 生成指示器挂哪一条 —— 两端同一条规则,不各写一份(见函数注释)。
export {
  indicatorHostMessageId,
  isNoticeOnlyMessage,
} from "./transcript/generating-indicator";
// 对话流行模型 —— 宿主把消息喂进来、拿回虚拟行,渲染细节留在包里。
export {
  applyLiveTranscriptRows,
  buildSettledTranscriptRows,
  buildSourceByMessageId,
  buildTranscriptRows,
  estimateRowSizeWithSpacing,
  transcriptRowPadClass,
} from "./transcript/transcript-rows";
export type {
  LiveRowContent,
  TranscriptRow,
} from "./transcript/transcript-rows";
// 行渲染出口:活动块(工具步骤)与 canonical 工具卡路由。
export { CanonicalToolRouter } from "./transcript/canonical-tool/registry";
// 「本次会话」的工具 diff：把同一个文件的每一次工具调用重放成一个连续 diff。
// 纯函数（挑调用 + 重放）与呈现件都在包里，两端不各写一份（AGENTS.md 约束 6）。
export { resolveToolPathInRoot } from "./lib/work-root-path";
export type { PlanActionStream } from "./transcript/canonical-tool/props";
// 消息行装配:把行模型装成带外壳(头像/名字/时间戳/元信息)的一条消息。
// 这是 agentre-server 拿到「完整消息」而不只是正文块的那一层。
export {
  ChatMessage,
  ErrorCard,
  MessageMeta,
  TranscriptRenderContext,
  TranscriptRowView,
} from "./transcript/transcript-row-view";
export type { TranscriptRenderContextValue } from "./transcript/transcript-row-view";
export { AutoTriggerBanner } from "./transcript/auto-trigger-banner";
export { CodeBlock } from "./transcript/code-block";
export { CompactBoundaryDivider } from "./transcript/compact-boundary-divider";
export { TranscriptSkeleton } from "./transcript/transcript-skeleton";
export { TranscriptJumpControl } from "./transcript/transcript-jump-control";
export {
  autonomousTurnMessageIds,
  computeBottomVisibleMessageId,
  countTurnsAfterMessage,
} from "./transcript/transcript-turns";
export { MarkdownText } from "./transcript/markdown-text";
export { ThinkingBlock } from "./transcript/thinking-block";
export {
  TranscriptCard,
  TranscriptCardBody,
  TranscriptCardHeader,
  TranscriptPill,
} from "./transcript/transcript-card";
export { TranscriptUIStateProvider } from "./transcript/transcript-ui-state";
export { TranscriptPortsProvider } from "./transcript/ports-context";
export { TranscriptLiveStateProvider } from "./transcript/live-state";
export type { TranscriptLiveState } from "./transcript/live-state";
// 本地 `!command` 卡片 + 它与宿主状态之间的接缝(反应式投影 / 输出订阅 / 写动作)。
export { LocalCommandCard } from "./transcript/local-command/card";
export { isLocalCommandCollapsed } from "./transcript/local-command/collapsed";
export { makeStreamDecoder } from "./transcript/local-command/decode";
export { LocalCommandsProvider } from "./transcript/local-command/access";
export type {
  LocalCommandsAccess,
  LocalCommandView,
} from "./transcript/local-command/access";
export { statusConfig } from "./transcript/agent-status";
export type { AgentStatus } from "./transcript/agent-status";
// 消息外壳:头像列 + 内容列的布局骨架。头像节点由调用方给(见 message-row.tsx)。
export { MESSAGE_AVATAR_CLASS } from "./transcript/message-row";
export type {
  AnswerToolPermissionInput,
  AnswerUserQuestionInput,
  ReadFileResult,
  TranscriptPorts,
} from "./transcript/ports";
export type {
  RetryNotice,
  TranscriptBlock,
  TranscriptBlockExecApproval,
  TranscriptBlockToolApproval,
  TranscriptLocalCommand,
  TranscriptMessage,
} from "./transcript/dto";
// wire 事件帧的归约 —— 上面那份 DTO 的**另一条入口**。桌面端自己的会话由 Go 侧
// 把块算好直接喂 DTO 进来;只拿得到 wire 事件流的那两个面(agentre-server 的 relay、
// 桌面端 Peer Tab 的 peer_svc 事件)在浏览器里补上同一次投影。帧来源留在各宿主。
export {
  createTranscriptProjector,
  interactiveRequestIds,
  opensAssistantMessage,
  reduceFrames,
  reduceSessionState,
} from "./transcript/frames";
export type { TranscriptFrame, TranscriptProjector } from "./transcript/frames";
// 终端视图 —— 交互式 PTY 面板(live 开新 PTY / attach 接管本地命令那条)。
export { TerminalPanel } from "./terminal/terminal-panel";
// 终端传输端口 —— 订阅式接缝(长连接的字节流),与上面那批一次性动作端口分开。
export {
  TerminalTransportProvider,
  useTerminalTransport,
} from "./terminal/transport-context";
export type {
  TerminalExit,
  TerminalSubscriber,
  TerminalTransport,
  TerminalUnsubscribe,
} from "./terminal/transport";
// ── 组织面：部门轴索引 + 主区三栏详情 ─────────────────────────────────────
// 规格 2026-08-18「server 端的组织管理面」要求两端「索引与详情同形、同一批共享
// 组件」。所以进来的是**只吃 props** 的那一层：索引投影、落点判据、行与组头、
// 归属下拉、工具清单、执行目标行。留在宿主的是取数、拖拽传感器与它们的 DnD 装配、
// store，以及身份怎么画（头像 / 图标注册表，经 slot 注入）。
export { buildOrgIndex, buildOrgReportsToOptions } from "./org/org-index-model";
export type { OrgIndexGroup, OrgIndexRow } from "./org/org-index-model";
export { buildOrgReportToMap, resolveOrgReportTo } from "./org/reporting";
export { computeOrgReorder } from "./org/reorder";
export {
  isValidOrgDepartmentDrop,
  isValidOrgDrop,
  resolveOrgDrop,
} from "./org/org-drop";
export type {
  OrgDragSubject,
  OrgDropContext,
  OrgDropTarget,
} from "./org/org-drop";
export {
  ProjectHeaderActions,
  ProjectHeaderContextMenu,
} from "./project/project-header-actions";
export type {
  ProjectHeaderActionsProps,
  ProjectHeaderMember,
} from "./project/project-header-actions";
export { ProjectCreateDialog } from "./project/project-create-dialog";
export { ProjectDeleteDialog } from "./project/project-delete-dialog";
export { ProjectSettingsDialog } from "./project/project-settings-dialog";
// 身份区与字形选择器：两个弹窗共用那一份，宿主也可能要单独摆（新建向导之类）。
export type {
  DirectoryFailure,
  DirectoryFailureKind,
  ListDirOutcome,
  MkdirOutcome,
  PickerMachine,
  ProjectCandidateView,
  ProjectCreatePorts,
  ProjectDeletePorts,
  ProjectFieldValues,
  ProjectFsPort,
  ProjectMachineView,
  ProjectMemberView,
  ProjectSettingsPorts,
  ProjectSettingsView,
  ProjectWriteFailure,
  ProjectWriteOutcome,
} from "./project/ports";
export { isOrgSystemAgent } from "./org/types";
export type {
  OrgAgentModel,
  OrgBackendModel,
  OrgDepartmentModel,
  OrgSelection,
} from "./org/types";
export type { OrgSortableRowBinding } from "./org/drag-binding";
export { OrgAgentRow } from "./org/org-agent-row";
export { OrgGroupHeader } from "./org/org-group-header";
export { OrgInsertLine } from "./org/org-insert-line";
export { OrgPlacementField } from "./org/org-placement-field";
export type { OrgPlacement } from "./org/org-placement-field";
export { OrgToolList } from "./org/org-tool-list";
export { OrgExecTargetRow } from "./org/org-exec-target-row";
export type {
  OrgExecTargetRowProps,
  OrgExecTargetStatus,
} from "./org/org-exec-target-row";
export {
  orgBackendTypeLabel,
  orgExecTargetMachineLabel,
  orgExecTargetReasonLabel,
} from "./org/exec-target-reasons";

// 主题:三态 + 存储端口 + 切换按钮。两个宿主原本各写一份,而 `.dark` 变体本来就
// 由本包的 tokens.css 定义 —— 契约在包里、实现在宿主的倒挂到此为止。
export { ThemeProvider, ThemeToggle, useTheme } from "./theme";
export type { AppTheme, AppThemePreference } from "./theme";
// 图标词表:key 是持久化的 avatar_icon 列值,两个宿主必须同一份;
// `iconNode` 把 key 解成画好的节点,两端不再各写一份(选择器仍是宿主各画各的)。
export {
  ICON_VOCABULARY,
  hasIcon,
  iconCategories,
  iconForKey,
  iconList,
  iconMeta,
  iconNode,
  iconsByCategory,
  searchIcons,
} from "./org/icon-registry";
export type { IconCategory, IconMeta } from "./org/icon-registry";
// 相对时间:同一套 60 秒 / 60 分 / 24 小时阶梯的三种输出形态。
export {
  formatCompactRelativeTime,
  formatIntlRelativeTime,
  formatRelativeTime,
} from "./lib/relative-time";
export { computeContextUsage } from "./composer/context-usage";
/**
 * 看板一族：宿主中立的呈现件 + 8 档色调表。桌面端与 agentre-server 的 /issues
 * 画的是同一块板，取数与拖拽手势各自留在宿主。
 */
export { IssueBoard } from "./board/issue-board";
// 取值域只有一份，与 `IssueTone` 同一个文件：两处各写一份 8 档的话，谁跟
// issue_entity 的 allowedTones 对齐是碰运气。
export { BOARD_STAGES } from "./board/types";
export type {
  BoardCardProject,
  BoardCardView,
  BoardColumnView,
  BoardDragBindings,
  BoardStage,
  BoardViewModel,
  IssueTone,
} from "./board/types";
// 看板的**查询面**：范围选择器、六条筛选、任务表单壳与标签管理。取数、拖拽手势与
// 执行归属的三颗 pill 实现都留在宿主，包只发意图、只画同一形状。
export { ProjectScopePicker } from "./board/project-scope-picker";
export { buildScopeRows } from "./board/scope-tree";
export { BoardFilterBar } from "./board/board-filter-bar";
export { activeConditions } from "./board/query-conditions";
export { TaskFormShell } from "./board/task-form";
export type { BoardAgentOption, ExecPillContext } from "./board/exec-ports";
export { initialTaskFormValue } from "./board/use-task-form";
export { LabelManagerPanel } from "./board/label-manager";
export { ALL_PROJECTS_SCOPE, EMPTY_BOARD_QUERY } from "./board/query-types";
export type {
  BoardQuery,
  LabelMutation,
  LabelUsageView,
  ProjectScope,
  ScopeProjectNode,
  TaskFormValue,
  TimeRange,
} from "./board/query-types";

// ── agentred 接入引导 ─────────────────────────────────────────────────
// 桌面端与 agentre-server 的引导渲染同一份实现：命令是唯一的一份，两段正文与外壳
// 归包，各自宿主的配对表单 / 设备码输入 / 路由留在宿主，经 props 与插槽接进来。
export {
  AGENTRED_RELEASES_URL,
  agentredLoginCommand,
  agentredPairCommand,
} from "./onboarding/agentred-commands";
export type {
  AgentredInstallMethod,
  AgentredRunMode,
  AgentredTargetOS,
} from "./onboarding/agentred-commands";
export { CommandCard } from "./onboarding/command-card";
export { GuideStepRail } from "./onboarding/guide-step-rail";
export type { GuideStep } from "./onboarding/guide-step-rail";
export {
  AgentredInstallDocsLink,
  AgentredInstallSection,
} from "./onboarding/agentred-install-section";
export { AgentredServiceSection } from "./onboarding/agentred-service-section";
