/**
 * `@agentre-ai/agentre-ui` —— Agentre 桌面端与 agentre-server 共用的前端层。
 *
 * 四个消费面：
 *   - `@agentre-ai/agentre-ui/tokens.css`         —— design tokens（无需 import 本模块）
 *   - `@agentre-ai/agentre-ui/code-highlight.css` —— CodeBlock 的 highlight.js 配色
 *   - `@agentre-ai/agentre-ui/i18n`               —— 语言包 + namespace（不拖组件树）
 *   - `@agentre-ai/agentre-ui`                    —— 对话流渲染器、数据契约与语言包
 */
export {
  AGENTRE_UI_NAMESPACE,
  agentreUiResources,
  useUiTranslation,
} from "./i18n";
export { cn } from "./lib/utils";
// agent 调色板的 token 词汇表（与 tokens.css 同源）+ token → css 变量。
export { agentColorOrder, tokenToCssColor } from "./lib/agent-color";
export type { AgentColor } from "./lib/agent-color";
export {
  COPY_TOAST_DURATION_MS,
  COPY_TOAST_ERROR_DURATION_MS,
  copyTextWithToast,
} from "./lib/clipboard-toast";
export {
  hasTextSelectionWithin,
  shouldIgnoreClickForSelection,
} from "./lib/copyable-text";
export { Badge } from "./ui/badge";
export { Button, buttonVariants } from "./ui/button";
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
export { HoverCard, HoverCardContent, HoverCardTrigger } from "./ui/hover-card";
export { Input } from "./ui/input";
export {
  Popover,
  PopoverAnchor,
  PopoverContent,
  PopoverTrigger,
} from "./ui/popover";
export { Spinner } from "./ui/spinner";
export { Textarea } from "./ui/textarea";
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
export { FreeGroupHeader } from "./session-index/free-group-header";
export type { FreeGroupHeaderProps } from "./session-index/free-group-header";
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
  loadTranscriptDraftState,
  loadTranscriptScrollState,
  nextAutoFollow,
  pruneChatPanelScrollState,
  saveTranscriptDraftState,
  saveTranscriptScrollState,
} from "./transcript/chat-panel-scroll-state";
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
// 对话流行模型 —— 宿主把消息喂进来、拿回虚拟行,渲染细节留在包里。
export {
  applyLiveTranscriptRows,
  buildSettledTranscriptRows,
  buildSourceByMessageId,
  buildTranscriptRows,
  estimateRowSizeWithSpacing,
  isLastRowOfMessage,
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
