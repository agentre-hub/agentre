/**
 * 对话流的数据契约 —— 本包唯一的输入形态。
 *
 * 形状逐字对齐桌面端 Wails 生成的 `chat_svc.ChatMessage` / `chat_svc.ChatBlock`
 * （去掉 wails 注入的 `convertValues`，并把 `view.` / `canonical.` / `blocks.`
 * 三个 namespace 拍平）。这样桌面端把生成对象**零转换**直接喂进来即可；
 * agentre-server 那侧则由 `reduceFrames()` 把 relay 上来的 wire 帧归约成同一形态。
 *
 * 这里是手写的第二份声明，因此桌面端配了一条编译期可赋值性守卫
 * （frontend/src/components/agentre/__tests__/transcript-dto-contract.ts）：
 * 后端一旦改动生成类型而本文件没跟上，`tsc -b` 会直接红，而不是运行时才炸。
 */

// ─── canonical 工具投影 ──────────────────────────────────────────────────────

export interface DiffLine {
  op: string;
  old?: number;
  new?: number;
  text: string;
}

export interface DiffHunk {
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  header?: string;
  lines: DiffLine[];
}

export interface FileEditPatch {
  path: string;
  kind: string;
  hunks: DiffHunk[];
  plus?: number;
  minus?: number;
  truncated?: boolean;
  replaceAll?: boolean;
}

export interface FileEdit {
  files: FileEditPatch[];
}

export interface FileWrite {
  path: string;
  content: string;
  lines: number;
  bytes: number;
  truncated?: boolean;
}

export interface PlanAction {
  id: string;
  kind: string;
  requiresFeedback?: boolean;
}

export interface PlanStep {
  id?: string;
  step: string;
  status: string;
}

export interface PlanUpdate {
  steps?: PlanStep[];
  text?: string;
  actions?: PlanAction[];
}

export interface PlanApproveRequest {
  requestId: string;
  planText: string;
  resolved?: boolean;
  allowed?: boolean;
  denyReason?: string;
  actions?: PlanAction[];
}

export interface AgentSpawnRun {
  id: string;
  index: number;
  agent?: string;
  profile?: string;
  agentSource?: string;
  task: string;
  requestedModel?: string;
}

export interface AgentSpawn {
  taskId: string;
  subagentType?: string;
  taskDescription?: string;
  prompt?: string;
  model?: string;
  mode?: string;
  runs?: AgentSpawnRun[];
  lastToolName?: string;
  toolUses?: number;
  totalTokens?: number;
  durationMs?: number;
  status?: string;
}

export interface ToolPermission {
  requestId: string;
  toolName: string;
  toolInput?: Record<string, unknown>;
  resolved?: boolean;
  allowed?: boolean;
  alwaysAllow?: boolean;
}

export interface UserAsk {
  requestId: string;
  questions: unknown;
  answers?: unknown;
  answered?: boolean;
  skipped?: boolean;
  expired?: boolean;
}

/**
 * translator 识别成功时填。`kind` 是宽化的 string(生成类型如此)，收窄成联合类型
 * 的判定逻辑在 `canonical-tool/tier.ts`，不在类型层做。
 */
export interface CanonicalDTO {
  kind: string;
  fileWrite?: FileWrite;
  fileEdit?: FileEdit;
  userAsk?: UserAsk;
  planUpdate?: PlanUpdate;
  planApprove?: PlanApproveRequest;
  agentSpawn?: AgentSpawn;
  toolPermission?: ToolPermission;
}

// ─── 结构化 block 载荷 ───────────────────────────────────────────────────────

export interface AskOptionDTO {
  label: string;
  description?: string;
  preview?: string;
}

export interface AskQuestionDTO {
  id?: string;
  question: string;
  header?: string;
  multiSelect?: boolean;
  isOther?: boolean;
  isSecret?: boolean;
  options: AskOptionDTO[];
}

export interface AskAnswerDTO {
  questionIndex: number;
  labels: string[];
  otherText?: string;
}

export interface TranscriptBlockImage {
  name?: string;
  mediaType: string;
  dataUrl: string;
}

export interface SubagentRun {
  id: string;
  index: number;
  agent?: string;
  profile?: string;
  agentSource?: string;
  task: string;
  requestedModel?: string;
  model?: string;
  status: string;
  lastToolName?: string;
  toolUses?: number;
  summary?: string;
  errorMessage?: string;
}

export interface TranscriptBlockSubagent {
  taskId?: string;
  kind?: string;
  subagentType?: string;
  taskDescription?: string;
  prompt?: string;
  lastToolName?: string;
  toolUses?: number;
  totalTokens?: number;
  durationMs?: number;
  status?: string;
  summary?: string;
  mode?: string;
  runs?: SubagentRun[];
  model?: string;
}

export interface TranscriptBlockAskUserQuestion {
  requestId: string;
  questions: AskQuestionDTO[];
  answered?: boolean;
  answers?: AskAnswerDTO[];
  skipped?: boolean;
  expired?: boolean;
}

export interface TranscriptBlockToolPermission {
  requestId: string;
  toolName: string;
  toolInput: Record<string, unknown>;
  resolved?: boolean;
  allowed?: boolean;
  alwaysAllow?: boolean;
}

/** 展示安全的 OpenClaw Gateway 审批投影：不含 token / 环境 / systemRunPlan。 */
export interface TranscriptBlockExecApproval {
  id: string;
  commandText: string;
  commandPreview?: string;
  allowedDecisions?: string[];
  host?: string;
  nodeId?: string;
  agentId?: string;
  status: string;
  decision?: string;
  resolvedBy?: string;
  createdAtMs?: number;
  expiresAtMs?: number;
  resolvedAtMs?: number;
}

export interface TranscriptBlockToolApproval {
  toolKey: string;
  requestId: string;
  toolName: string;
  toolInput?: Record<string, unknown>;
  status: string;
  result?: string;
}

export interface TranscriptBlockCompactBoundary {
  preTokens?: number;
  trigger?: string;
  at: number;
}

// ─── 顶层 ───────────────────────────────────────────────────────────────────

export interface TranscriptBlock {
  type: string;
  text?: string;
  level?: string;
  providerKey?: string;
  providerName?: string;
  modelKey?: string;
  modelName?: string;
  noticeKind?: string;
  image?: TranscriptBlockImage;
  toolUseId?: string;
  toolName?: string;
  toolInput?: Record<string, unknown>;
  isError?: boolean;
  toolResultMeta?: Record<string, unknown>;
  parentToolUseId?: string;
  subagentRunId?: string;
  subagent?: TranscriptBlockSubagent;
  askUserQuestion?: TranscriptBlockAskUserQuestion;
  toolPermission?: TranscriptBlockToolPermission;
  execApproval?: TranscriptBlockExecApproval;
  toolApproval?: TranscriptBlockToolApproval;
  canonical?: CanonicalDTO;
  compact?: TranscriptBlockCompactBoundary;
  raw?: Record<string, unknown>;
}

export interface TranscriptMessage {
  id: number;
  sessionId: number;
  role: string;
  blocks: TranscriptBlock[];
  model: string;
  promptTokens: number;
  completionTokens: number;
  cachedTokens: number;
  cacheCreationTokens: number;
  reasoningTokens: number;
  totalInputTokens: number;
  durationMs: number;
  errorText: string;
  seq: number;
  createtime: number;
  sourceDevice?: string;
  sourceDeviceName?: string;
}

/** 流式期间尚未冻结成 block 的重试提示。 */
export interface RetryNotice {
  attempt: number;
  maxAttempts: number;
  message: string;
  details: string;
  at: number;
}

export type LocalCommandStatus = "running" | "done" | "failed" | "stopped";

/**
 * 本地 `!command` 执行条目在转录行模型里的形态。
 *
 * 条目的**产生**归宿主（桌面端是 `local-commands-store` 这个 zustand store，
 * 由 Wails 侧的 PTY 事件喂），包只按这个形状把它排进行里。所以这里是一份
 * 与宿主同形的声明，而不是 import 宿主的类型 —— 让包反向依赖 store 会拧反
 * 依赖方向，`boundary.test.ts` 也会当场拦下。
 *
 * 漂移由宿主的 `transcript-dto-contract.test.ts` 断言拦住。
 * 没有本地执行能力的宿主（agentre-server）恒定传空数组即可。
 */
export interface TranscriptLocalCommand {
  id: string;
  sessionId: number;
  command: string;
  createdAt: number;
  status: LocalCommandStatus;
  exitCode?: number;
  output: string;
  finishedAt?: number;
  expanded?: boolean;
}
