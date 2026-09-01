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
export { PermissionModePill } from "./permission-mode";
export type { PermissionModePillProps } from "./permission-mode";
export {
  PERMISSION_MODE_DISABLED_REASON_KEY,
  PERMISSION_MODE_META_UI,
  fallbackPermissionModeMetaUI,
  isPermissionModeDisabled,
  nextPermissionMode,
  normalizePermissionMode,
} from "./permission-mode";
export type {
  PermissionMode,
  PermissionModeDisableCtx,
  PermissionModeMetaUI,
} from "./permission-mode";
export { ModelTargetPicker } from "./engine/model-target-picker";
export {
  ProviderPillResolution,
  ProviderPillTrigger,
} from "./engine/model-target-picker";
export type { ProviderPillState } from "./engine/model-target-picker";
export { resolveProviderPillState } from "./engine/model-target-picker";
export type { ProviderPillStateInput } from "./engine/model-target-picker";
export type { ModelTargetPickerProps } from "./engine/model-target-picker";
export {
  readRecentTargets,
  recordRecentTarget,
  removeRecentTarget,
  recentStorageKey,
} from "./engine/model-target-picker/recents";
export {
  buildPickerCatalog,
  isNativeTarget,
  providerCompatibleForBackend,
  sameTarget,
  useModelTargetCatalog,
} from "./engine/model-target-picker";
export type {
  ModelTarget,
  PickerModel,
  PickerProvider,
  PickerScenario,
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
export {
  deviceSelectValue,
  resolveExecutionDevice,
} from "./engine/device-identity";
export type {
  ExecutionDeviceResolution,
  PairedDeviceIdentity,
} from "./engine/device-identity";
export { truncateFlashText } from "./engine/agent-backends-utils";
// agent 调色板的 token 词汇表（与 tokens.css 同源）+ token → css 变量。
export { agentColorOrder, tokenToCssColor } from "./lib/agent-color";
export type { AgentColor } from "./lib/agent-color";
export {
  COPY_TOAST_DURATION_MS,
  COPY_TOAST_ERROR_DURATION_MS,
  copyTextToClipboard,
  copyTextWithToast,
} from "./lib/clipboard-toast";
export {
  hasTextSelectionWithin,
  shouldIgnoreClickForSelection,
} from "./lib/copyable-text";
export { AgentAvatar, getAgentInitials } from "./ui/agent-avatar";
export type { AgentAvatarProps, AgentAvatarSize } from "./ui/agent-avatar";
export { Alert, AlertTitle, AlertDescription } from "./ui/alert";
export { Badge } from "./ui/badge";
export { Button, buttonVariants } from "./ui/button";
export { Checkbox } from "./ui/checkbox";
export {
  Dialog,
  DialogTrigger,
  DialogPortal,
  DialogOverlay,
  DialogClose,
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
export type { DialogShellSaveState, DialogShellSize } from "./ui/dialog-shell";
export {
  DropdownMenu,
  DropdownMenuPortal,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuItem,
  DropdownMenuCheckboxItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
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
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSeparator,
  FieldSet,
  FieldTitle,
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
  SelectGroup,
  SelectValue,
  SelectTrigger,
  SelectContent,
  SelectLabel,
  SelectItem,
  SelectSeparator,
  SELECT_NONE,
} from "./ui/select";
export { SearchInput } from "./ui/search-input";
export type { SearchInputProps, SearchInputVariant } from "./ui/search-input";
export { ResizableSidebar } from "./ui/resizable-sidebar";
export type { ResizableSidebarProps } from "./ui/resizable-sidebar";
export {
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  SIDEBAR_WIDTH_KEY_PREFIX,
  clampSidebarWidth,
  readSidebarWidth,
  writeSidebarWidth,
} from "./ui/sidebar-width-state";
export { Separator } from "./ui/separator";
export { Spinner } from "./ui/spinner";
export { Switch } from "./ui/switch";
export {
  Table,
  TableBody,
  TableCaption,
  TableCell,
  TableFooter,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table";
export { Textarea } from "./ui/textarea";
export { Toggle, toggleVariants } from "./ui/toggle";
export { ToggleGroup, ToggleGroupItem } from "./ui/toggle-group";
export {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "./ui/tooltip";
export { formatDuration, formatTurnDuration } from "./lib/format-duration";
export type { LiveTurnInput } from "./transcript/turn-stats";
export { classifyLink, isLocalFileURL } from "./lib/link-classify";
export type { LinkClass, LocalPathKind } from "./lib/link-classify";
export {
  previewKind,
  resolvePreviewRelPath,
  toRelPath,
} from "./lib/previewable";
export type { PreviewKind } from "./lib/previewable";
export { splitStreamingMarkdown } from "./lib/streaming-markdown";
export type { SplitStreamingMarkdown } from "./lib/streaming-markdown";
export { useCollapsible } from "./hooks/use-collapsible";
// 与 `@agentre-hub/agentre-ui/base.css` 里的滚动条规则是一对：那半边把滑块颜色绑到
// --sb-thumb 并默认透明，这半边在滚动时改值。宿主两样都要接。
export { useAutoHideScrollbars } from "./hooks/use-auto-hide-scrollbars";
export { isOpenInNewTabModifier } from "./lib/keyboard";
export { StatusDot } from "./ui/status-dot";
export type { StatusDotProps } from "./ui/status-dot";
// ── 会话索引 ───────────────────────────────────────────────────────────────
// 行、分组容器，以及**轴投影本身**（规格 2026-08-18「共享包承载什么」把它收了
// 进来：组怎么分、怎么排、兜底组摆在哪只该有一份实现）。留在宿主的是各端的取数与
// 装配，以及**可选轴清单**——桌面端三档、server 控制台四档（决策 17）。
export { SessionGroup } from "./session-index/session-group";
export type { SessionGroupProps } from "./session-index/session-group";
export { SessionRow } from "./session-index/session-row";
export type {
  SessionRowLinkProps,
  SessionRowLinkRenderer,
  SessionRowProps,
} from "./session-index/session-row";
export type {
  SessionAttentionRank,
  SessionRowModel,
} from "./session-index/types";
export {
  readSidebarExpanded,
  SIDEBAR_EXPANDED_KEY_PREFIX,
  writeSidebarExpanded,
} from "./session-index/expanded-state";
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
  GroupKind,
  IndexAxis,
  IndexGroup,
  IndexGroupRow,
  IndexRow,
  MachineInfo,
  ProjectNode,
} from "./session-index/axis-groups";
// 索引的零耦合呈现件：轴选择器、组头、行的前置槽与次行。
export { AxisPicker } from "./session-index/axis-picker";
export type { AxisPickerProps } from "./session-index/axis-picker";
export {
  IndexGroupHeader,
  groupActionRevealClassName,
  groupActionRevealTouchClassName,
  groupGlyphClassName,
} from "./session-index/group-header";
export type { IndexGroupHeaderProps } from "./session-index/group-header";
export { ProjectGroupHeader } from "./session-index/project-group-header";
export type { ProjectGroupHeaderProps } from "./session-index/project-group-header";
export { AgentGroupHeader } from "./session-index/agent-group-header";
export type { AgentGroupHeaderProps } from "./session-index/agent-group-header";
export { MachineGroupHeader } from "./session-index/machine-group-header";
export type { MachineGroupHeaderProps } from "./session-index/machine-group-header";
export { FreeGroupHeader } from "./session-index/free-group-header";
export type { FreeGroupHeaderProps } from "./session-index/free-group-header";
// 导入本地会话（规格 2026-08-26）：入口条目、候选列表、转录预览与对话框都归包，
// 宿主只提供 ports（发现 / 预览 / 写入 / 打开会话）。
export {
  IMPORT_MENU_ITEM_ID,
  ImportLocalSessionIcon,
  ImportLocalSessionMenu,
  useImportLocalSessionLabel,
} from "./session-import/import-menu";
export type { ImportLocalSessionMenuProps } from "./session-import/import-menu";
export { ImportSessionDialog } from "./session-import/import-dialog";
export type {
  ImportDialogPrefill,
  ImportSessionDialogProps,
} from "./session-import/import-dialog";
export {
  CandidateList,
  formatCandidateTime,
} from "./session-import/candidate-list";
export type { CandidateListProps } from "./session-import/candidate-list";
export { PreviewPane } from "./session-import/preview-pane";
export type {
  PreviewPaneProps,
  PreviewState,
} from "./session-import/preview-pane";
export { buildCandidateGroups } from "./session-import/candidate-groups";
export type {
  CandidateBucket,
  CandidateGroup,
} from "./session-import/candidate-groups";
export type {
  ImportAgentOption,
  ImportCandidateView,
  ImportCandidatesRequest,
  ImportCandidatesResult,
  ImportDeviceView,
  ImportGapView,
  ImportOutcome,
  ImportPreviewRequest,
  ImportPreviewResult,
  ImportRunRequest,
  ImportScanIssue,
  ImportScanStatus,
  ImportTranscriptMetaView,
  SessionImportPorts,
} from "./session-import/ports";
export { OwnSessionsHeader } from "./session-index/own-sessions-header";
export type { OwnSessionsHeaderProps } from "./session-index/own-sessions-header";
export { ProjectGlyph } from "./session-index/project-glyph";
export type {
  ProjectGlyphInfo,
  ProjectGlyphProps,
} from "./session-index/project-glyph";
export { RowLeadingSlot } from "./session-index/row-leading-slot";
export type { RowLeadingSlotProps } from "./session-index/row-leading-slot";
export { RowSecondaryLine } from "./session-index/row-secondary-line";
export type { RowSecondaryLineProps } from "./session-index/row-secondary-line";
export {
  computeTerminalHeight,
  FALLBACK_CELL_PX,
  MAX_ROWS,
  MIN_ROWS,
  PADDING_PX,
} from "./terminal/terminal-height";
export {
  readTerminalTheme,
  resolveTerminalTheme,
  TERMINAL_FONT_FAMILY,
} from "./terminal/terminal-theme";
export {
  mentionsToDisplayText,
  parseMentionXml,
  serializeMentionXml,
} from "./chat-input/mentions/xml";
export type {
  MentionKind,
  MentionRef,
  MentionSegment,
} from "./chat-input/mentions/xml";
// ── 聊天输入编辑器 ─────────────────────────────────────────────────────────
export { AIChatInput } from "./chat-input";
export type { AIChatInputProps } from "./chat-input";
export type {
  AIChatInputDraft,
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
export { ComposerOptionPicker } from "./composer/composer-option-picker";
export type { ComposerOption } from "./composer/composer-option-picker";
export { ContextMeter } from "./composer/context-meter";
export type { ContextMeterProps } from "./composer/context-meter";
export { usageLevel } from "./composer/usage-level";
export type { UsageLevel } from "./composer/usage-level";
export { formatTokens } from "./lib/format-tokens";
export { groupAgentsForPicking } from "./lib/agent-picking";
export { StatusBanner } from "./session-status/status-banner";
export type {
  StatusBannerProps,
  StatusBannerTone,
} from "./session-status/status-banner";
export { MachineOfflineBanner } from "./session-status/machine-offline-banner";
export type { MachineOfflineBannerProps } from "./session-status/machine-offline-banner";
export type {
  AgentPickingGroups,
  AgentPickingInput,
} from "./lib/agent-picking";
export {
  buildEditorDocFromMessage,
  extractPlainText,
} from "./chat-input/content";
export { buildMentionSources } from "./chat-input/mentions/build-sources";
export type { MentionItem, MentionSources } from "./chat-input/mentions/types";
export {
  classifyDroppedPaths,
  formatPathsForInput,
  resolveDroppedPaths,
} from "./chat-input/drop";
export type {
  DroppedImageAttachment,
  DroppedImageItem,
} from "./chat-input/drop";
export { useFileDropZone } from "./chat-input/use-file-drop";
export type { DropZoneRegistrar } from "./chat-input/use-file-drop";
// `/` 命令：机制在包里，清单归宿主（见 chat-input/slash/types.ts）。
export { filterByQuery } from "./chat-input/slash/filter";
export { detectSlashTrigger } from "./chat-input/slash/trigger";
export type { SlashTriggerHit } from "./chat-input/slash/trigger";
export {
  findValidSlashRanges,
  SlashHighlight,
} from "./chat-input/slash/slash-highlight";
export type { SlashRange } from "./chat-input/slash/slash-highlight";
export { SlashPopover } from "./chat-input/slash/slash-popover";
export { useSlashMenu } from "./chat-input/slash/use-slash-menu";
export type { SlashMenuState } from "./chat-input/slash/use-slash-menu";
export type { SlashCommand, SlashExec } from "./chat-input/slash/types";
// `!` Shell 历史：可选的宿主接缝（agentre-server 没有这条能力）。
export {
  LocalCommandHistoryProvider,
  useOptionalLocalCommandHistoryAccess,
} from "./chat-input/local-command-history/access";
export type {
  LocalCommandHistoryAccess,
  LocalCommandHistoryEntry,
  LocalCommandHistoryMutation,
  LocalCommandHistoryScope,
} from "./chat-input/local-command-history/access";
export {
  LOCAL_COMMAND_HISTORY_CLEAR_SELECTOR,
  localCommandHistoryOptionId,
} from "./chat-input/local-command-history/history-popover";
export {
  normalizeSuggestionQuery,
  scoreSuggestion,
} from "./lib/suggestion-score";
export type { SuggestionScoreInput } from "./lib/suggestion-score";
export {
  __resetChatPanelScrollStateForTesting,
  clearTranscriptDraftState,
  COLLAPSED_RESTORE_GUARD_MS,
  computeTopVisibleAnchor,
  loadTranscriptDraftState,
  loadTranscriptScrollState,
  nextAutoFollow,
  pruneChatPanelScrollState,
  saveTranscriptDraftState,
  saveTranscriptScrollState,
} from "./transcript/chat-panel-scroll-state";
export type { TranscriptScrollState } from "./transcript/chat-panel-scroll-state";
// 转录滚动几何(贴底跟随 / 折叠恢复 / 快照 / 回到底部)整块住在包里,宿主只接线。
export { useTranscriptScroll } from "./transcript/use-transcript-scroll";
export type {
  TranscriptAnchorScroller,
  UseTranscriptScrollOptions,
  UseTranscriptScrollResult,
} from "./transcript/use-transcript-scroll";
export {
  MARKDOWN_AUTOLINK_TAG,
  rehypeMarkdownAutolinks,
  tokenizeMarkdownAutoLinks,
} from "./transcript/markdown-autolinks";
export type { MarkdownAutoLinkSegment } from "./transcript/markdown-autolinks";
export {
  formatCommandExecutionCommand,
  relativizePath,
  summarizeRawTool,
} from "./transcript/canonical-tool/raw/summary";
export type { SummarizeOptions } from "./transcript/canonical-tool/raw/summary";
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
  isLastRowOfMessage,
  transcriptRowPadClass,
} from "./transcript/transcript-rows";
export type {
  LiveRowContent,
  TranscriptRow,
  TranscriptRowItem,
} from "./transcript/transcript-rows";
// 行渲染出口:活动块(工具步骤)与 canonical 工具卡路由。
export { ActivityBlock } from "./transcript/activity-block/block";
export type { ActivityBlockProps } from "./transcript/activity-block/block";
export { CanonicalToolRouter } from "./transcript/canonical-tool/registry";
// 「本次会话」的工具 diff：把同一个文件的每一次工具调用重放成一个连续 diff。
// 纯函数（挑调用 + 重放）与呈现件都在包里，两端不各写一份（AGENTS.md 约束 6）。
export { collectReplayCalls } from "./transcript/canonical-tool/file-edit/replay-calls";
export { resolveToolPathInRoot } from "./lib/work-root-path";
export { replayPatches } from "./transcript/canonical-tool/file-edit/replay";
export type {
  ReplayCall,
  ReplayFailureReason,
  ReplayResult,
  ReplaySegment,
} from "./transcript/canonical-tool/file-edit/replay";
export { ReplayedFileDiff } from "./transcript/canonical-tool/file-edit/replay-view";
export { PlanApproveCard } from "./transcript/canonical-tool/plan-approve-request/card";
export type { PlanActionStream } from "./transcript/canonical-tool/props";
// 两张按 block.type 直接路由的审批卡(不进 CanonicalToolRouter)。
export { OpenClawExecApprovalCard } from "./transcript/openclaw-exec-approval/card";
export { ToolApprovalCard } from "./transcript/tool-approval/card";
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
export type { CodeBlockProps } from "./transcript/code-block";
export {
  CollapsibleCode,
  CollapsibleCodeParams,
  stringifyToolValue,
  toolInputEntries,
} from "./transcript/collapsible-code";
export type { CollapsibleCodeSurface } from "./transcript/collapsible-code";
export { CompactBoundaryDivider } from "./transcript/compact-boundary-divider";
export { TranscriptSkeleton } from "./transcript/transcript-skeleton";
export { TranscriptJumpControl } from "./transcript/transcript-jump-control";
export type {
  TranscriptCatchUpSummary,
  TranscriptJumpControlProps,
} from "./transcript/transcript-jump-control";
export type { TranscriptSkeletonProps } from "./transcript/transcript-skeleton";
export {
  autonomousTurnMessageIds,
  computeBottomVisibleMessageId,
  countTurnsAfterMessage,
} from "./transcript/transcript-turns";
export type { TurnMessage } from "./transcript/transcript-turns";
export {
  classifyMarkdownImage,
  MarkdownImage,
} from "./transcript/markdown-image";
export type { MarkdownImageClass } from "./transcript/markdown-image";
export { MarkdownText, StreamingMarkdown } from "./transcript/markdown-text";
export type {
  MarkdownInlineDecorator,
  MarkdownInlineSegment,
} from "./transcript/markdown-text";
export { RichLink } from "./transcript/rich-link";
export { ThinkingBlock } from "./transcript/thinking-block";
export {
  TranscriptCard,
  TranscriptCardBody,
  TranscriptCardHeader,
  TranscriptPill,
} from "./transcript/transcript-card";
export type { TranscriptCardTone } from "./transcript/transcript-card";
export {
  TranscriptUIStateProvider,
  useTranscriptBooleanState,
} from "./transcript/transcript-ui-state";
export {
  TranscriptPortsProvider,
  useOptionalPort,
  useTranscriptPorts,
} from "./transcript/ports-context";
export {
  noopTranscriptLiveState,
  TranscriptLiveStateProvider,
  useIsStreamActive,
  useMarkToolPermissionResolved,
} from "./transcript/live-state";
export type {
  MarkToolPermissionResolvedInput,
  TranscriptLiveState,
} from "./transcript/live-state";
// 本地 `!command` 卡片 + 它与宿主状态之间的接缝(反应式投影 / 输出订阅 / 写动作)。
export { LocalCommandCard } from "./transcript/local-command/card";
export { isLocalCommandCollapsed } from "./transcript/local-command/collapsed";
export { makeStreamDecoder } from "./transcript/local-command/decode";
export {
  LocalCommandsProvider,
  useLocalCommand,
  useLocalCommandsAccess,
} from "./transcript/local-command/access";
export type {
  LocalCommandOutputListener,
  LocalCommandsAccess,
  LocalCommandUnsubscribe,
  LocalCommandView,
} from "./transcript/local-command/access";
export { statusConfig } from "./transcript/agent-status";
export type { AgentStatus, AgentStatusStyle } from "./transcript/agent-status";
// 消息外壳:头像列 + 内容列的布局骨架。头像节点由调用方给(见 message-row.tsx)。
export {
  MESSAGE_AVATAR_CLASS,
  MessageCopyButton,
  MessageRow,
} from "./transcript/message-row";
export type {
  AnswerToolApprovalInput,
  AnswerToolPermissionInput,
  AnswerUserQuestionInput,
  ReadFileResult,
  RequiredPortName,
  ResolveExecApprovalInput,
  ResolveExecApprovalResult,
  ResolvePlanActionInput,
  ResolvePlanActionResult,
  TranscriptPorts,
} from "./transcript/ports";
export type {
  AgentSpawn,
  AgentSpawnRun,
  AskAnswerDTO,
  AskOptionDTO,
  AskQuestionDTO,
  CanonicalDTO,
  DiffHunk,
  DiffLine,
  FileEdit,
  FileEditPatch,
  FileWrite,
  PlanAction,
  PlanApproveRequest,
  PlanStep,
  PlanUpdate,
  RetryNotice,
  SubagentRun,
  LocalCommandStatus,
  ToolPermission,
  TranscriptBlock,
  TranscriptBlockAskUserQuestion,
  TranscriptBlockCompactBoundary,
  TranscriptBlockExecApproval,
  TranscriptBlockImage,
  TranscriptBlockSubagent,
  TranscriptBlockToolApproval,
  TranscriptBlockToolPermission,
  TranscriptLocalCommand,
  TranscriptMessage,
  UserAsk,
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
export type {
  SessionRuntimeState,
  TranscriptFrame,
  TranscriptProjector,
} from "./transcript/frames";
// 终端视图 —— 交互式 PTY 面板(live 开新 PTY / attach 接管本地命令那条)。
export { TerminalPanel } from "./terminal/terminal-panel";
export type { TerminalPanelProps } from "./terminal/terminal-panel";
// 终端传输端口 —— 订阅式接缝(长连接的字节流),与上面那批一次性动作端口分开。
export {
  TerminalTransportProvider,
  useOptionalTerminalTransport,
  useTerminalTransport,
} from "./terminal/transport-context";
export type {
  TerminalExit,
  TerminalExitReason,
  TerminalOpenInput,
  TerminalSubscriber,
  TerminalTransport,
  TerminalUnsubscribe,
} from "./terminal/transport";
// ── 组织面：部门轴索引 + 主区三栏详情 ─────────────────────────────────────
// 规格 2026-08-18「server 端的组织管理面」要求两端「索引与详情同形、同一批共享
// 组件」。所以进来的是**只吃 props** 的那一层：索引投影、落点判据、行与组头、
// 归属下拉、工具清单、执行目标行。留在宿主的是取数、拖拽传感器与它们的 DnD 装配、
// store，以及身份怎么画（头像 / 图标注册表，经 slot 注入）。
export {
  buildOrgIndex,
  buildOrgReportsToOptions,
  EMPTY_ORG_FILTERS,
} from "./org/org-index-model";
export type {
  OrgIndexFilters,
  OrgIndexGroup,
  OrgIndexInput,
  OrgIndexModel,
  OrgIndexRow,
} from "./org/org-index-model";
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
  OrgWriteOp,
} from "./org/org-drop";
export { DirectoryPicker } from "./project/directory-picker";
export type { DirectoryPickerProps } from "./project/directory-picker";
export {
  ProjectHeaderActions,
  ProjectHeaderContextMenu,
} from "./project/project-header-actions";
export type {
  ProjectHeaderActionsProps,
  ProjectHeaderMember,
  ProjectMenuCapabilities,
} from "./project/project-header-actions";
export { ProjectCreateDialog } from "./project/project-create-dialog";
export type { ProjectCreateDialogProps } from "./project/project-create-dialog";
export { ProjectDeleteDialog } from "./project/project-delete-dialog";
export type { ProjectDeleteDialogProps } from "./project/project-delete-dialog";
export { ProjectSettingsDialog } from "./project/project-settings-dialog";
export type { ProjectSettingsDialogProps } from "./project/project-settings-dialog";
// 身份区与字形选择器：两个弹窗共用那一份，宿主也可能要单独摆（新建向导之类）。
export { ProjectIdentityFields } from "./project/project-identity-fields";
export type { ProjectIdentityFieldsProps } from "./project/project-identity-fields";
export { ProjectGlyphPicker } from "./project/project-glyph-picker";
export type { ProjectGlyphPickerProps } from "./project/project-glyph-picker";
export { breadcrumbOf, joinPath } from "./project/ports";
export type {
  DirectoryEntry,
  DirectoryFailure,
  DirectoryFailureKind,
  FsOutcome,
  ListDirOutcome,
  ListDirResult,
  MkdirOutcome,
  PickerMachine,
  ProjectCandidateView,
  ProjectCreateDraft,
  ProjectCreateOutcome,
  ProjectCreatePorts,
  ProjectDeletePorts,
  ProjectFieldValues,
  ProjectGitInfo,
  ProjectFsPort,
  ProjectMachineView,
  ProjectMemberView,
  ProjectSettingsPorts,
  ProjectSettingsView,
  ProjectWriteFailure,
  ProjectWriteFailureKind,
  ProjectWriteOutcome,
} from "./project/ports";
export { isOrgSystemAgent, ORG_SYSTEM_BADGE } from "./org/types";
export type {
  OrgAgentBackendSummary,
  OrgAgentModel,
  OrgBackendModel,
  OrgDepartmentModel,
  OrgSelection,
} from "./org/types";
export type {
  OrgDragHandleBinding,
  OrgDropState,
  OrgSortableRowBinding,
} from "./org/drag-binding";
export { OrgAgentRow } from "./org/org-agent-row";
export type { OrgAgentRowProps } from "./org/org-agent-row";
export { OrgGroupHeader } from "./org/org-group-header";
export type { OrgGroupHeaderProps } from "./org/org-group-header";
export { OrgInsertLine } from "./org/org-insert-line";
export type { OrgInsertLineProps } from "./org/org-insert-line";
export { OrgPlacementField } from "./org/org-placement-field";
export type {
  OrgPlacement,
  OrgPlacementFieldProps,
} from "./org/org-placement-field";
export { OrgToolList } from "./org/org-tool-list";
export type { OrgToolListProps } from "./org/org-tool-list";
export { buildOrgToolList, ORG_APPROVAL_TOOLS } from "./org/tool-catalog";
export type { OrgAgentTool, OrgToolListItem } from "./org/tool-catalog";
export { OrgExecTargetRow } from "./org/org-exec-target-row";
export type {
  OrgExecTargetRowProps,
  OrgExecTargetStatus,
} from "./org/org-exec-target-row";
export {
  ORG_EXEC_TARGET_DESTRUCTIVE_REASONS,
  orgBackendTypeLabel,
  orgExecTargetMachineLabel,
  orgExecTargetReasonLabel,
} from "./org/exec-target-reasons";

// 主题:三态 + 存储端口 + 切换按钮。两个宿主原本各写一份,而 `.dark` 变体本来就
// 由本包的 tokens.css 定义 —— 契约在包里、实现在宿主的倒挂到此为止。
export {
  THEME_PREFERENCE_ORDER,
  THEME_STORAGE_KEY,
  ThemeProvider,
  ThemeToggle,
  applyDocumentTheme,
  getSystemTheme,
  isAppTheme,
  isAppThemePreference,
  nextThemePreference,
  resolveThemePreference,
  useTheme,
} from "./theme";
export type {
  AppTheme,
  AppThemePreference,
  ThemeContextValue,
  ThemeProviderProps,
  ThemeStoragePort,
  ThemeToggleProps,
} from "./theme";
// 图标词表:key 是持久化的 avatar_icon 列值,两个宿主必须同一份;渲染留宿主。
export {
  ICON_CATEGORY_ORDER,
  ICON_VOCABULARY,
  hasIcon,
  iconCategories,
  iconForKey,
  iconList,
  iconMeta,
  iconsByCategory,
  searchIcons,
} from "./org/icon-registry";
export type {
  IconCategory,
  IconMeta,
  IconTextSource,
  IconTranslate,
  IconVocabularyEntry,
} from "./org/icon-registry";
// 相对时间:同一套 60 秒 / 60 分 / 24 小时阶梯的三种输出形态。
export {
  formatCompactRelativeTime,
  formatIntlRelativeTime,
  formatRelativeTime,
  relativeTimeBucket,
} from "./lib/relative-time";
export type {
  FormatRelativeTimeOptions,
  RelativeTimeBucket,
  RelativeTimeTranslate,
} from "./lib/relative-time";
export { computeContextUsage } from "./composer/context-usage";
export type {
  ContextUsage,
  ContextUsageLive,
  ContextUsageMessage,
} from "./composer/context-usage";
/**
 * 看板一族：宿主中立的呈现件 + 8 档色调表。桌面端与 agentre-server 的 /issues
 * 画的是同一块板，取数与拖拽手势各自留在宿主。
 */
export { IssueBoard } from "./board/issue-board";
export type { IssueBoardProps } from "./board/issue-board";
export { BoardColumn } from "./board/board-column";
export type { BoardColumnProps } from "./board/board-column";
export { BoardCard } from "./board/board-card";
export type { BoardCardProps } from "./board/board-card";
export { BoardCardLabels, CARD_LABEL_LIMIT } from "./board/card-labels";
export type { BoardCardLabelsProps } from "./board/card-labels";
export { BoardCardMenu } from "./board/card-menu";
export type { BoardCardMenuProps } from "./board/card-menu";
export { BoardEmptyState } from "./board/board-empty-state";
export type {
  BoardEmptyKind,
  BoardEmptyStateProps,
} from "./board/board-empty-state";
export { BoardSkeleton } from "./board/board-skeleton";
export { DONE_VISIBLE_LIMIT, useBoardColumns } from "./board/use-board-columns";
export type {
  BoardColumnState,
  UseBoardColumnsResult,
} from "./board/use-board-columns";
export { BOARD_STAGE_META } from "./board/stages";
export { toneClass, toneClassNames } from "./board/tones";
// 取值域只有一份，与 `IssueTone` 同一个文件：两处各写一份 8 档的话，谁跟
// issue_entity 的 allowedTones 对齐是碰运气。
export { BOARD_STAGES, ISSUE_TONES } from "./board/types";
export type {
  BoardCardDragBinding,
  BoardCardDragState,
  BoardCardProject,
  BoardCardView,
  BoardColumnDragBinding,
  BoardColumnDropState,
  BoardColumnView,
  BoardDragBindings,
  BoardLabelView,
  BoardPorts,
  BoardStage,
  BoardViewModel,
  IssueTone,
} from "./board/types";
// 看板的**查询面**：范围选择器、六条筛选、任务表单壳与标签管理。取数、拖拽手势与
// 执行归属的三颗 pill 实现都留在宿主，包只发意图、只画同一形状。
export { ProjectScopePicker } from "./board/project-scope-picker";
export type { ProjectScopePickerProps } from "./board/project-scope-picker";
export { ProjectScopeTrigger } from "./board/scope-trigger";
export type { ProjectScopeTriggerProps } from "./board/scope-trigger";
export { ProjectScopePopover } from "./board/scope-popover";
export type { ProjectScopePopoverProps } from "./board/scope-popover";
export { useProjectScope } from "./board/use-project-scope";
export type { UseProjectScopeResult } from "./board/use-project-scope";
export {
  buildScopeRows,
  filterScopeRows,
  splitMatch,
} from "./board/scope-tree";
export type { MatchSegment, ScopeRow } from "./board/scope-tree";
export { BoardFilterBar } from "./board/board-filter-bar";
export type { BoardFilterBarProps } from "./board/board-filter-bar";
export { BoardFilterPanel } from "./board/filter-panel";
export type { BoardFilterPanelProps } from "./board/filter-panel";
export { BoardFilterChips } from "./board/filter-chips";
export type { BoardFilterChipsProps } from "./board/filter-chips";
export { BoardSearchBox } from "./board/board-search";
export type { BoardSearchBoxProps } from "./board/board-search";
export {
  activeConditionCount,
  activeConditions,
  buildFilterChips,
  dropChip,
} from "./board/query-conditions";
export type { ConditionKey, FilterChip } from "./board/query-conditions";
export {
  BOARD_SEARCH_DEBOUNCE_MS,
  useBoardQuery,
} from "./board/use-board-query";
export type { UseBoardQueryResult } from "./board/use-board-query";
export { TaskFormShell } from "./board/task-form";
export type { TaskFormShellProps } from "./board/task-form";
export {
  TASK_PILL_CLASS,
  TaskLabelChips,
  TaskProjectPill,
  TaskStagePill,
} from "./board/task-form-pills";
export type {
  TaskLabelChipsProps,
  TaskProjectPillProps,
  TaskStagePillProps,
} from "./board/task-form-pills";
export { TaskExecPills } from "./board/exec-pills";
export type { TaskExecPillsProps } from "./board/exec-pills";
export type {
  BoardAgentOption,
  ExecPillContext,
  ExecTargetPort,
  ModelTargetPort,
} from "./board/exec-ports";
export { initialTaskFormValue, useTaskForm } from "./board/use-task-form";
export type {
  InitialTaskFormInput,
  UseTaskFormResult,
} from "./board/use-task-form";
export { LabelManagerPanel } from "./board/label-manager";
export type { LabelManagerPanelProps } from "./board/label-manager";
export { LabelPalette } from "./board/label-palette";
export type { LabelPaletteProps } from "./board/label-palette";
export { useLabelManager } from "./board/use-label-manager";
export type {
  LabelMutateResult,
  UseLabelManagerResult,
} from "./board/use-label-manager";
export {
  ALL_PROJECTS_SCOPE,
  ANY_TIME,
  DEFAULT_DONE_RETENTION,
  EMPTY_BOARD_QUERY,
} from "./board/query-types";
export type {
  BoardQuery,
  BoardQueryPorts,
  DoneRetention,
  LabelMatchMode,
  LabelMutation,
  LabelUsageView,
  ProjectScope,
  ScopeProjectNode,
  TaskFormValue,
  TimePreset,
  TimeRange,
} from "./board/query-types";
