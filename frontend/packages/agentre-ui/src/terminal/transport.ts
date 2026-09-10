/**
 * 终端的**传输端口** —— 本包与宿主之间唯一的 PTY 边界。
 *
 * 为什么它不是 `TranscriptPorts` 的第 N 个方法：那批端口全是「一次性动作」
 * （按一下 → 一个 Promise → 完事）。终端不是动作，是一条**长连接的订阅流**：
 * 一条 PTY 从 open 到 exit 之间会持续吐字节，视图要在其间挂上/摘下监听。
 * 请求-响应的函数对象表达不了「摘下」，所以单独开一层。
 *
 * 它也不是 `TranscriptLiveState` 那种 hook 层：那层存在的唯一理由是**反应式读取**
 * （流起停要触发重渲染）。终端这里没有任何要被渲染读取的反应式状态 —— 字节流由
 * 调用方自己喂给 xterm，视图状态是调用方的 `useState`。所以这层是普通对象方法，
 * 和端口一样是模块级常量。
 *
 * 桌面端实现见 `src/components/agentre/terminal/terminal-transport-desktop.ts`
 * （Wails 绑定 + 事件流）；agentre-server 首版没有终端能力，见下面「不提供」的语义。
 */

/**
 * PTY 的终止原因。`natural` 是 shell 自己退出，其余四种都是外力：
 * 用户 kill / 与远端设备断链 / agentred 关停 / 压根没起来。
 */
export type TerminalExitReason =
  | "natural"
  | "killed"
  | "connection_lost"
  | "daemon_shutdown"
  | "error";

export interface TerminalExit {
  code: number;
  reason: TerminalExitReason;
  msg?: string;
}

export interface TerminalOpenInput {
  terminalId: string;
  projectId: number;
  /** 空串表示本机；非空表示这条 PTY 要开在那台远端设备上。 */
  deviceId: string;
  cols: number;
  rows: number;
}

/**
 * 一次订阅的两个回调。做成一个对象而不是两次 `subscribe` 调用，是因为 data 与
 * exit 属于同一条 PTY 生命周期：拆开就会有「只摘掉一半」的中间态，而那恰好是
 * 这层要根治的那类 bug。
 */
export interface TerminalSubscriber {
  /**
   * PTY 的原始 stdout **字节**。
   *
   * 为什么是 `Uint8Array` 而不是字符串：PTY 输出会在任意字节位置被切成块，
   * 一个多字节字符可能横跨两块；若在这层就解成字符串，跨块的那半个字符会被
   * 替换成 U+FFFD 永久损坏。字节交给 xterm，由它跨 write 重组。
   *
   * 为什么也不是 base64 字符串：base64 是**桌面端 Wails JSON 事件桥**的编码，
   * 不是终端的契约。agentre-server 走 relay，二进制帧本来就是字节，逼它先编成
   * base64 只为让包再解回来，是把一个宿主的传输细节写进跨端接口。所以解码归
   * 宿主适配层（桌面端是 `base64ToBytes`），端口只说字节。
   */
  onData(bytes: Uint8Array): void;
  /** PTY 结束。到达后不会再有 `onData`；退订仍由调用方自己做。 */
  onExit(exit: TerminalExit): void;
}

/**
 * 退订句柄。契约是**只摘自己**：同一个 terminalId 上的其它订阅者必须毫发无损。
 * 重复调用是幂等的（视图的 effect 清理与 exit 处理可能都想收一次）。
 */
export type TerminalUnsubscribe = () => void;

export interface TerminalTransport {
  /**
   * 挂上一个订阅者，返回只摘自己的退订句柄。
   *
   * **必须能在 `open()` 之前调用**：PTY 可能在 open 的 Promise 兑现之前就吐出
   * 第一帧，先 open 后 subscribe 会丢开头几个字节。同一个 terminalId 允许多个
   * 订阅者共存（终端面板 + 本地命令卡片会同时盯着同一条 PTY），实现方自己负责
   * 扇出，不得把「底层传输只允许一个监听者」泄漏给调用方。
   */
  subscribe(
    terminalId: string,
    subscriber: TerminalSubscriber,
  ): TerminalUnsubscribe;

  /** 开一条 PTY。已存在同名 PTY 时的语义由实现方决定（桌面端是复用）。 */
  open(input: TerminalOpenInput): Promise<void>;

  /** 关掉 PTY。对已经退出的 PTY 调用应当是无害的。 */
  close(terminalId: string): Promise<void>;

  /**
   * 写 stdin。这里是**字符串**而不是字节，和 `onData` 刻意不对称：stdin 来自
   * xterm 的 `onData`，本来就是一个完整的 JS 字符串，没有「跨块半个字符」问题；
   * 硬要编成字节只会让两端各多一次转换。
   */
  write(terminalId: string, data: string): Promise<void>;

  /** 视口尺寸变化。cols/rows 是字符格数，不是像素。 */
  resize(terminalId: string, cols: number, rows: number): Promise<void>;
}
