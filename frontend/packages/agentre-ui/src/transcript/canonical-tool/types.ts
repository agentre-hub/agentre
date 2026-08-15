// MIRROR OF: internal/service/chat_svc/view/chat_block.go.
// Any change here must be reflected on the Go side + round-trip test.
//
// ─── 这份与 ../dto.ts 是什么关系 ─────────────────────────────────────────────
//
// 同一批 canonical 形状在本包里有**两层**声明,同名但不同,而且是有意的:
//
//   - `../dto.ts` —— **宽边界**。包的公开输入契约,逐字对齐 Wails 生成类型:
//     `CanonicalDTO.kind` 是 `string`、`DiffLine.op` 是 `string`、
//     `FileEditPatch.kind` 是 `string`。宿主(桌面端的生成对象 / server 的
//     reduceFrames 产物)零转换直接喂进来,所以边界上不能比生成类型更窄 ——
//     窄一格就把「后端多发了一个 kind」变成编译错误,而它本该是运行时兜底。
//   - 本文件 —— **窄内部视图**。卡片渲染要靠判别式收窄:`CanonicalDTO` 在这里
//     是可辨识联合(按 `kind` 分支即可拿到对应载荷)、`DiffOp` / `FileChangeKind`
//     是字面量联合。收窄的判定逻辑在 `tier.ts`,类型层只负责把结果表达出来。
//     不从包的 `index.ts` 导出:它是渲染层的内部形态,不是对外契约。
//
// 两层共存是刻意的,本轮不合并。但有两个**纯重复**:`DiffHunk` 与 `AskAnswerDTO`
// 与 `../dto.ts` 里的同名声明逐字段完全相同,不含任何收窄,属于可以直接收敛掉的
// 冗余 —— 留待后续单独处理,不在本轮搬迁的范围内。

export type CanonicalKind =
  | "file.write"
  | "file.edit"
  | "user.ask"
  | "plan.update"
  | "plan.approve_request"
  | "agent.spawn"
  | "tool.permission";

export type FileWriteDTO = {
  path: string;
  content: string;
  lines: number;
  bytes: number;
  truncated?: boolean;
};

export type DiffOp = " " | "+" | "-";
export type DiffLine = { op: DiffOp; old?: number; new?: number; text: string };
export type DiffHunk = {
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  header?: string;
  lines: DiffLine[];
};
export type FileChangeKind = "created" | "modified" | "deleted";
export type FileEditPatch = {
  path: string;
  kind: FileChangeKind;
  hunks: DiffHunk[];
  plus: number;
  minus: number;
  truncated?: boolean;
  replaceAll?: boolean;
};
export type FileEditDTO = { files: FileEditPatch[] };

export type AskQuestionDTO = {
  id?: string;
  question: string;
  header: string;
  multiSelect?: boolean;
  isOther?: boolean;
  isSecret?: boolean;
  options: { label: string; description: string; preview?: string }[];
};
export type AskAnswerDTO = {
  questionIndex: number;
  labels: string[];
  otherText?: string;
};
export type UserAskDTO = {
  requestId: string;
  questions: AskQuestionDTO[];
  answers?: AskAnswerDTO[];
  answered?: boolean;
  skipped?: boolean;
  expired?: boolean;
};

export type PlanStepDTO = {
  id?: string;
  step: string;
  status: "pending" | "inProgress" | "completed" | "canceled";
};

// PlanActionKind 与 internal/pkg/agentruntime/canonical/plan_action.go 一一对应。
// 前端按 kind 选 button variant/icon。
export type PlanActionKind = "approve" | "refine" | "reject";

// PlanActionDTO 单个按钮的稳定描述,id 是后端装配的 provider-neutral
// plan.* 命名空间 key;前端按 id + kind 渲染,不再分支 backendType/source。
export type PlanActionDTO = {
  id: string;
  kind: PlanActionKind;
  requiresFeedback?: boolean;
};

export type PlanUpdateDTO = {
  steps: PlanStepDTO[];
  text?: string;
  actions?: PlanActionDTO[];
};

export type PlanApproveRequestDTO = {
  requestId: string;
  planText: string;
  resolved?: boolean;
  allowed?: boolean;
  denyReason?: string;
  actions?: PlanActionDTO[];
};

export type AgentSpawnMode = "single" | "parallel" | "chain";
export type AgentSpawnStatus =
  | "waiting"
  | "running"
  | "completed"
  | "failed"
  | "canceled"
  | "skipped"
  | "unknown"
  | "partial";

export type AgentSpawnRunDTO = {
  id: string;
  index: number;
  agent?: string;
  profile?: string;
  agentSource?: string;
  task: string;
  requestedModel?: string;
  // The following fields are supplied by ChatBlockSubagent.runs full snapshots.
  model?: string;
  status?: AgentSpawnStatus;
  lastToolName?: string;
  toolUses?: number;
  summary?: string;
  errorMessage?: string;
};

export type AgentSpawnDTO = {
  taskId: string;
  subagentType?: string;
  taskDescription?: string;
  prompt?: string;
  // model 有两个互斥来源:入参别名(静态,如 "haiku")或子代理首帧实际模型
  // (运行时,如 "claude-haiku-4-5-20251001")。card.tsx 的 readSpawn 用既有
  // 「运行时非空覆盖静态」语义在渲染前解出最终值,这里只存原值,不做归一化。
  model?: string;
  mode?: AgentSpawnMode;
  runs?: AgentSpawnRunDTO[];
  lastToolName?: string;
  toolUses?: number;
  totalTokens?: number;
  durationMs?: number;
  status?: AgentSpawnStatus;
};

export type ToolPermissionDTO = {
  requestId: string;
  toolName: string;
  toolInput?: Record<string, unknown>;
  resolved?: boolean;
  allowed?: boolean;
  alwaysAllow?: boolean;
};

export type CanonicalDTO =
  | { kind: "file.write"; fileWrite: FileWriteDTO }
  | { kind: "file.edit"; fileEdit: FileEditDTO }
  | { kind: "user.ask"; userAsk: UserAskDTO }
  | { kind: "plan.update"; planUpdate: PlanUpdateDTO }
  | { kind: "plan.approve_request"; planApprove: PlanApproveRequestDTO }
  | { kind: "agent.spawn"; agentSpawn: AgentSpawnDTO }
  | { kind: "tool.permission"; toolPermission: ToolPermissionDTO };
