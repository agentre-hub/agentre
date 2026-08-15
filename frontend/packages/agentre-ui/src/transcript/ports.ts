/**
 * 对话流的**动作端口** —— 本包与宿主之间唯一的副作用边界。
 *
 * 为什么需要它：转录里的交互卡片（工具审批、回答提问、计划审批、exec 审批）
 * 原本各自直接 import Wails 绑定。桌面端那套接线 agentre-server 永远用不了 ——
 * 它的同一批动作要走 relay RPC 打到远端设备上。所以「渲染什么」进包，
 * 「按下去会发生什么」由宿主注入。
 *
 * 契约约定：
 *   - 所有方法都可能失败。包内只负责把 reject 冒泡给卡片自己的错误态，
 *     不吞异常、不做 toast——提示文案属于宿主的产品决策。
 *   - 形状对齐桌面端 Wails 绑定的 request/response，宿主零转换即可接上；
 *     server 侧则把它们映射成自己的 relay 请求。
 */

export interface AnswerToolPermissionInput {
  sessionId: number;
  requestId: string;
  allow: boolean;
  alwaysAllowSession?: boolean;
  denyReason?: string;
  targetPermissionMode?: string;
}

export interface AnswerUserQuestionInput {
  sessionId: number;
  requestId: string;
  answers?: { questionIndex: number; labels: string[]; otherText?: string }[];
  skipped?: boolean;
}

export interface AnswerToolApprovalInput {
  sessionId: number;
  requestId: string;
  allow: boolean;
}

export interface ResolveExecApprovalInput {
  sessionId: number;
  approvalId: string;
  decision: string;
}

export interface ResolveExecApprovalResult {
  status: string;
  decision?: string;
}

export interface ResolvePlanActionInput {
  sessionId: number;
  requestId?: string;
  actionId: string;
  feedback?: string;
}

/**
 * 解析计划动作会**开一轮新的对话**，因此回包带着新轮次的坐标。宿主据此接上
 * 自己的流式订阅（桌面端是 Wails 事件流，server 是 relay 帧流），包内不解释。
 */
export interface ResolvePlanActionResult {
  sessionId?: number;
  userMessageId?: number;
  assistantMessageId?: number;
  stream?: string;
}

export interface ReadFileResult {
  content: string;
  contentType?: string;
  binary?: boolean;
  tooLarge?: boolean;
}

export interface TranscriptPorts {
  // ─── 审批与回答：都是「用户对 agent 的一次答复」 ───────────────────────
  answerToolPermission(input: AnswerToolPermissionInput): Promise<void>;
  answerUserQuestion(input: AnswerUserQuestionInput): Promise<void>;
  answerToolApproval(input: AnswerToolApprovalInput): Promise<void>;
  resolveExecApproval(
    input: ResolveExecApprovalInput,
  ): Promise<ResolveExecApprovalResult>;
  resolvePlanAction(
    input: ResolvePlanActionInput,
  ): Promise<ResolvePlanActionResult>;

  // ─── 外壳能力：宿主环境差异最大的一批 ─────────────────────────────────
  /** 在宿主的文件管理器/编辑器里打开一个本地路径。浏览器端可不实现（见下）。 */
  openPath?(path: string): Promise<void>;
  /** 在外部浏览器打开 URL。桌面端走 Wails runtime，浏览器端就是 window.open。 */
  openExternalURL?(url: string): void;
  /** 读工作区文件（转录里内联图片需要）。 */
  readWorkspaceFile?(sessionId: number, path: string): Promise<ReadFileResult>;
  /** 把一条本地命令挂到终端标签上。纯桌面能力。 */
  attachTerminal?(input: { terminalId: string; command: string }): void;
}

/**
 * 可选端口的语义是**能力探测**而不是「静默失败」：卡片按 `ports.openPath` 是否
 * 存在来决定渲不渲染那个按钮，而不是渲染出来点了没反应。必填的那五个是
 * 「转录只要含这类卡片就必须能答」——缺了应当在装配期就炸，不该拖到用户点下去。
 */
export type RequiredPortName =
  | "answerToolPermission"
  | "answerUserQuestion"
  | "answerToolApproval"
  | "resolveExecApproval"
  | "resolvePlanAction";
