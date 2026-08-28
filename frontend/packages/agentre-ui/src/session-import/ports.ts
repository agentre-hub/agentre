/**
 * 导入本地会话这一面的宿主边界（规格 2026-08-26）。
 *
 * 与 `project/ports.ts` 同一条路子：包只认这里这几个 view 与 port，不认识桌面端的
 * Wails 绑定，也不认识 agentre-server 的 REST。**能力差异用可选 port 表达，不用
 * `isDesktop` 分支 —— 没有那个 port 就没有那个入口。**
 *
 * id 一律是字符串：桌面端的设备 / 会话 / Agent id 是数字，agentre-server 那侧是
 * syncId。包不关心它是什么，只把它原样送回宿主。
 */
import type { TranscriptMessage } from "../transcript/dto";

/**
 * 一档答不出的判别值（ok 就是「有候选、没 issue」）。
 *
 * 只有 `unavailable`「这台机器此刻答不出」这一档。曾经还有一个 `unsupported`
 * 「这个 agentred 版本不认识这个方法」，两个宿主都不再产出它：agentre ↔ agentred
 * 的握手要求协议版本逐字相等，认不出方法族的构建根本走不到扫描那一步，而 server
 * 一侧那本来就是协议违约、走错误而不是出一档。
 */
export type ImportScanStatus = "unavailable";

/** 一档答不出的原因。`backend` 为空 = 设备级（整台机器拨不通）。 */
export interface ImportScanIssue {
  backend: string;
  status: ImportScanStatus;
  reason: string;
}

/** 候选列表里的一行。 */
export interface ImportCandidateView {
  backend: string;
  providerSessionId: string;
  title: string;
  cwd: string;
  /** unix 毫秒。 */
  startedAt: number;
  endedAt: number;
  /** 轮数；元信息里拿不到时是 0 = **未知**，不是「空会话」。 */
  turns: number;
  /** 来源标记，只用于展示："terminal" / "agentre" / ""（认不出就不猜）。 */
  origin: string;
  locator: string;
  /** 库里已经有同一条 provider session：照常列出、不可选，并给「打开」的去处。 */
  imported: boolean;
  importedSessionId: string;
}

export interface ImportCandidatesResult {
  candidates: ImportCandidateView[];
  issues: ImportScanIssue[];
}

/** 一条缺口声明（思维链加密、坏行跳过…）。`text` 是后端给的本机语言说明。 */
export interface ImportGapView {
  kind: string;
  count: number;
  detail: string;
  text: string;
}

/** 一份已打开转录的元信息。 */
export interface ImportTranscriptMetaView {
  backend: string;
  providerSessionId: string;
  title: string;
  cwd: string;
  model: string;
  turns: number;
  toolCalls: number;
  compactions: number;
  startedAt: number;
  endedAt: number;
  origin: string;
  gaps: ImportGapView[];
  /** 为假时导入降级为只读：转录照导，续跑关掉（决策 16）。 */
  cwdExists: boolean;
  imported: boolean;
  importedSessionId: string;
}

/**
 * 预览结果。
 *
 * `messages` 已经是**投影好的** `TranscriptMessage` —— 与真实回放/重载同一条投影，
 * 所以预览栏直接喂进既有的 `buildSettledTranscriptRows` 渲染链，不另造第二个渲染器。
 */
export interface ImportPreviewResult {
  meta: ImportTranscriptMetaView;
  messages: TranscriptMessage[];
  previewedTurns: number;
  /** -1 = 元信息里没有轮数，说不出还剩几轮。 */
  remainingTurns: number;
}

/** 设备选择器里的一台机器（第一维筛选，规格「远端」）。 */
export interface ImportDeviceView {
  id: string;
  name: string;
  online: boolean;
  /** 本机那一台。名单里恒有且恒在最前。 */
  local: boolean;
}

/** 续跑要绑的 agent —— 它带出 backend / provider / model（决策 15）。 */
export interface ImportAgentOption {
  id: string;
  name: string;
  color?: string;
  /** 与候选后端不同的 agent 接不住这条会话，选择器里不列（规格「续跑」）。 */
  backend: string;
  backendLabel?: string;
  model?: string;
}

export interface ImportOutcome {
  sessionId: string;
  /** 为真时一行都没写，`sessionId` 指向库里早就存在的那条。 */
  alreadyImported: boolean;
  /** cwd 已不存在：转录照导，但没写 provider_session_id，这条会话不能续跑。 */
  readOnly: boolean;
  cwd: string;
  importedTurns: number;
}

export interface ImportCandidatesRequest {
  deviceId: string;
  /** 空 = 这台设备上注册的全部后端。 */
  backends: string[];
  cwdPrefix: string;
  titleQuery: string;
}

export interface ImportPreviewRequest {
  deviceId: string;
  backend: string;
  locator: string;
}

export interface ImportRunRequest {
  deviceId: string;
  backend: string;
  locator: string;
  agentId: string;
  projectId: string;
  /**
   * 用户另选的工作目录（`pickDirectory` 的结果）。空 = 用磁盘转录里记的那个。
   *
   * 它是**这条会话去哪跑**，不是候选列表的筛选条件：写入侧把它落在会话的 cwd 上。
   * 换了目录的会话仍然只读 —— provider session id 是按原目录记的（决策 16）。
   */
  cwd: string;
  /** 这一笔导入的标识：`cancelImport` 拿同一个值把它停掉。 */
  requestId: string;
}

/**
 * 宿主给的那几件事。
 *
 * 三个必选 port 是这件功能的骨架；两个可选 port 是宿主**真的做得到**才声明的能力，
 * 声明不了就别画那个按钮 —— 画一个点了不动的「取消」比没有更糟。
 */
export interface SessionImportPorts {
  /** 设备名单（本机在前）。只有一台时选择器退成一行说明。 */
  devices: ImportDeviceView[];
  /** 可选的续跑目标。包按候选后端过滤，宿主不必先筛。 */
  agents: ImportAgentOption[];
  listCandidates(req: ImportCandidatesRequest): Promise<ImportCandidatesResult>;
  preview(req: ImportPreviewRequest): Promise<ImportPreviewResult>;
  runImport(req: ImportRunRequest): Promise<ImportOutcome>;
  /**
   * 按轮上报导入进度，返回退订函数。长任务的进度经事件到达而不是返回值。
   * 不声明就只画一个不带刻度的「正在导入」。
   */
  onImportProgress?(
    listener: (done: number, total: number) => void,
  ): () => void;
  /** 打开库里那条已导入的会话。 */
  openSession(sessionId: string): void;
  /**
   * 停掉一笔正在写库的导入（`requestId` 是发起时那一个）。
   *
   * 长任务要给得出退路：42 轮的会话写到一半才发现选错了 agent，只能等它写完再删
   * 是最糟的一种。写入侧整笔回滚，所以取消之后库里不留半截会话。
   *
   * **可选**：宿主真的接得上这条取消通道才声明 —— 摆一颗点了不动的「取消」比没有更糟。
   */
  cancelImport?(requestId: string): void;
  /**
   * cwd 不存在时的另一条出路：弹一个目录选择器，返回用户选中的绝对路径
   * （取消时返回 `null`）。
   *
   * 选中的目录成为**这条会话的工作目录**（随 `runImport` 的 `cwd` 交回宿主，
   * 写入侧落在会话那一列上），而不是改扫描筛选。它不会把这条会话变得可续跑：
   * provider session id 是按原目录记的，claude 的 `--resume` 换个目录找不到它
   * （决策 16）——换来的是「接着聊时从哪儿起 CLI」有了答案。
   *
   * **可选**：宿主弹得出目录选择器才声明。
   */
  pickDirectory?(): Promise<string | null>;
}
