import type { AgentStatus } from "../transcript/agent-status";

/**
 * 会话在 daemon 上的生命周期取值。真理源是
 * `internal/pkg/agentruntime/runtimes/remote/wire/wire.go` 的同名常量族；那份由 Go
 * 生成器写进 `@agentre-hub/agentre-wire` 的 `constants.gen.ts`。
 *
 * 这里再写一份而不是 import 那个包，理由与 `event-kinds.gen.ts` 那份词表逐字相同：
 * 本包以 **git 子目录 tarball** 的形态被 agentre-server 消费，抽出来的包里没有兄弟包
 * （见 `src/boundary.test.ts`）。漂移由**宿主**挡：桌面端同时看得见两个包，
 * `session-lifecycle-contract.test.ts` 在 `tsc -b` 阶段逐字比对这两份。
 */
export const SessionLifecycle = {
  running: "running",
  idle: "idle",
  failed: "failed",
  interrupted: "interrupted",
} as const;

export type SessionLifecycleState =
  (typeof SessionLifecycle)[keyof typeof SessionLifecycle];

/** `lifecycleToAgentStatus` 的输入：宿主把自己那一行摊成这两格。 */
export interface LifecycleStatusInput {
  /** 线上那一格原样。不认识的旧取值照收，不校验。 */
  lifecycleState: string;
  /**
   * 有待决的审批 / 提问挡在那里。它**不在**生命周期这条链上：是 running 之上的一层
   * 实时叠加，由 daemon 在应答时现算，永不落库。
   */
  waiting?: boolean;
}

/**
 * wire 的会话生命周期 → 展示用的 `AgentStatus`。**两端唯一的一份**。
 *
 * 此前两端各写一份 switch：控制台在 `lib/sessionView.ts`，桌面端在
 * `remote-devices/desktop-device-row.tsx`。同一个判断分成两处的代价 2026-09-04 兑现
 * 了一次 —— 控制台把 `interrupted` 折进出错档，桌面端没有；联调机上 agentred 重启
 * 一次，账号里 29 条对话在同一个 305 毫秒窗口里全变红且永不复原
 * （`Mirror.Revive` 对仍是 interrupted 的刻意不试接入）。
 *
 * 判定顺序不是审美：
 *
 *   - **等你处理压过一切**。挡在那儿等你按的那一轮在 daemon 眼里仍是 running，
 *     但用户要看的是「这条在等我」，不是「这条在跑」。
 *   - **红只留给 `failed`。** 它与 `interrupted` 在 wire 上是两件事（见那一族常量
 *     上的说明）：`failed` 是「上一轮的结局是错误」，会话本身完好，接得回实时流、
 *     发得出下一轮；`interrupted` 是「这条会话此刻接不上」的自锁终态，而 daemon
 *     每次启动都按 R10 把全部非终态会话整批标成它 —— 那是重启后的**常态**，不是
 *     任何一次故障。把常态画成警报，红色就变廉价，真跑挂的那条反而没人当回事。
 *   - **不认识的旧状态归闲置**，不猜。
 *
 * 归中性档不等于这条会话没话说：**状态文字仍由宿主分家**（「已中断」与「空闲」是
 * 两句话，两端各有自己的 i18n key）。这个函数只回答「要不要紧」，`SessionLifecycle`
 * 的词表跟着一起出去，宿主挑文案时不必再抄一份字面量。
 */
export function lifecycleToAgentStatus(
  input: LifecycleStatusInput,
): AgentStatus {
  if (input.waiting) return "waiting";
  switch (input.lifecycleState) {
    case SessionLifecycle.running:
      return "running";
    case SessionLifecycle.failed:
      return "error";
    default:
      return "idle";
  }
}
