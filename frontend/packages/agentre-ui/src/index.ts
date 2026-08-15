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
export { HoverCard, HoverCardContent, HoverCardTrigger } from "./ui/hover-card";
export { Input } from "./ui/input";
export { Spinner } from "./ui/spinner";
export { Textarea } from "./ui/textarea";
export {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "./ui/tooltip";
export { formatDuration } from "./lib/format-duration";
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
export { statusConfig } from "./transcript/agent-status";
export type { AgentStatus, AgentStatusStyle } from "./transcript/agent-status";
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
