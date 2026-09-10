import { useCallback, useEffect, useRef, useState } from "react";

/**
 * 「点了一键升级之后发生了什么」的状态机（规格「远程一键升级」）。桌面端与
 * server 控制台共用**这一份**：两端对同一台机器的同一次升级，看到的态与它们之间的
 * 迁移必须一模一样，各写一份的结果是两边各自漂（`requesting` 这一态就是漏在
 * 两端各自的实现里才让「点了没反应」出现的）。
 *
 * 留在宿主的只有取数：怎么发起那次受理调用、怎么读那台机器此刻自报的版本
 * ——桌面端走 Wails 绑定，控制台走 HTTP。它们由 {@link AgentredUpgradePorts} 注入。
 *
 * 结果怎么判：受理只回一个布尔，升级过程本身不可观察 —— daemon 受理之后就会重启，
 * 到那台机器的连接必断。因此从**版本号的变化**推断：轮询 readVersion，变了就是成功，
 * 5 分钟内没变按超时失败呈现（决策 6/7 的已知代价：没有监管者的形态升级后不会自己
 * 回来）。
 *
 * 活跃轮次拒绝（决策 8/21）不是失败，是需要显式越过的一道闸：这里只负责在用户点了
 * 「仍要升级」之后打开确认、并且只有确认之后才真的把 force=true 发出去 —— 重试绝不
 * 能被读成默许。主动作禁不禁用是呈现层的事（两端形态不同：菜单项 vs 按钮）。
 */
export type AgentredUpgradePhase =
  | { kind: "idle" }
  /**
   * 调用已经发出、受理判定还没回来。
   *
   * daemon 是把解析发布、下载、校验、替换**全部**跑完才应答的（受理即已装好），这
   * 一段因此能长达分钟级。它必须是一个显式的态：停在 idle 等于界面一声不吭，用户
   * 只会以为没点上、再点一次 —— 而第二次调用撞上那台机器的并发闸门，拿回来的是
   * 「已经有一次升级在跑」，一句我们自己制造出来的失败。
   */
  | { kind: "requesting" }
  /** 活跃轮次拒绝：message 逐字来自 daemon（与 `agentred update` 同一句话，决策 22）。 */
  | {
      kind: "active-turns";
      message: string;
      activeTurns: number;
      confirmOpen: boolean;
    }
  | { kind: "upgrading"; fromVersion: string; targetVersion: string }
  | { kind: "success"; fromVersion: string; toVersion: string }
  | { kind: "timeout" }
  /** 已是最新 / 进行中 / 路径不可写 / 下载校验失败，以及调用本身失败。 */
  | { kind: "failed"; message: string };

/**
 * 一次受理调用的答复，与 agentrewire.AgentredSelfUpdateResponse 一一对应。
 * 宿主负责把自己那条线上的形状（HTTP 的 snake_case / Wails 绑定的结构体）翻成它。
 */
export interface AgentredUpgradeAcceptance {
  accepted: boolean;
  /** 空串或缺省即受理；取值与 daemon 的拒绝原因一一对应。 */
  rejectReason?: string;
  /** daemon 那句人话，原样透传，两端都不重写（决策 22）。 */
  message?: string;
  activeTurns?: number;
  targetVersion?: string;
}

/** 宿主注入的取数：这个 hook 自己不知道有 HTTP 还是 Wails 这回事。 */
export interface AgentredUpgradePorts {
  /** 发起一次受理调用。这次 Promise 可能要几分钟才落地。 */
  requestUpgrade: (force: boolean) => Promise<AgentredUpgradeAcceptance>;
  /**
   * 读那台机器此刻自报的版本；读不到（正在重启、拿不到快照）返回空即可 ——
   * 判据只有「5 分钟内版本变没变」这一条，一次探测失败不算数。
   */
  readVersion: () => Promise<string | null | undefined>;
}

export interface AgentredUpgrade {
  phase: AgentredUpgradePhase;
  /** 点「升级 agentred」：不带 force，机器空闲时这一次就够了。 */
  start: () => void;
  /** 点「仍要升级」：只打开确认，不发出任何调用。 */
  requestForce: () => void;
  /** 确认框的「稍后再说」：退回等待确认前的那个态，不清掉拒绝原因。 */
  cancelForce: () => void;
  /** 确认框的「仍然升级」：这一步之后 force=true 才真的出现在请求里。 */
  confirmForce: () => void;
}

const POLL_INTERVAL_MS = 5_000;
const TIMEOUT_MS = 5 * 60 * 1000;

function messageOf(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * @param currentVersion 那台机器此刻自报的版本，用作「变没变」的基准；每次发起
 *   升级时按当下的值取一次快照。
 * @param ports 宿主的取数；每次渲染新造一个也没关系，这里只在事件与定时器里读它。
 */
export function useAgentredUpgrade(
  currentVersion: string,
  ports: AgentredUpgradePorts,
): AgentredUpgrade {
  const [phase, setPhase] = useState<AgentredUpgradePhase>({ kind: "idle" });
  const pollRef = useRef<number | null>(null);
  const deadlineRef = useRef<number | null>(null);
  // 每次 start/confirmForce 都作废前一次还在飞的调用与轮询：一个迟到的应答不该把
  // 界面从「刚刚重新点了一次」的状态拽回去。卸载时同样加一，此后什么都不再落地。
  const attemptRef = useRef(0);
  // 「这一次调用还在飞」的同步事实。不读 phase：状态更新是异步的，同一个事件循环里
  // 连点两下时第二下读到的还是旧值，而闸门必须在第一下之后立刻关上。
  const inFlightRef = useRef(false);
  // ports 与 currentVersion 由宿主每次渲染重新给，进依赖会让下面那些回调每渲染一次
  // 就换一个身份。同步放在 effect 里而不是渲染期直接写 ref（渲染期改 ref 是被禁的：
  // 那一次修改对本次渲染不可见，读到什么取决于渲染顺序）。
  const portsRef = useRef(ports);
  const versionRef = useRef(currentVersion);
  useEffect(() => {
    portsRef.current = ports;
    versionRef.current = currentVersion;
  }, [currentVersion, ports]);

  const stopTimers = useCallback(() => {
    if (pollRef.current !== null) {
      window.clearInterval(pollRef.current);
      pollRef.current = null;
    }
    if (deadlineRef.current !== null) {
      window.clearTimeout(deadlineRef.current);
      deadlineRef.current = null;
    }
  }, []);

  useEffect(() => {
    return () => {
      attemptRef.current += 1;
      stopTimers();
    };
  }, [stopTimers]);

  const beginPolling = useCallback(
    (attempt: number, fromVersion: string) => {
      pollRef.current = window.setInterval(() => {
        portsRef.current
          .readVersion()
          .then((version) => {
            if (attempt !== attemptRef.current) return;
            if (version && version !== fromVersion) {
              stopTimers();
              setPhase({ kind: "success", fromVersion, toVersion: version });
            }
          })
          .catch(() => {
            // daemon 正在重启：轮询期间取数失败是这段时间的常态，不能拿一次探测
            // 失败就提前判定失败 —— 判据只有「5 分钟内版本变没变」这一条。
          });
      }, POLL_INTERVAL_MS);
      deadlineRef.current = window.setTimeout(() => {
        if (attempt !== attemptRef.current) return;
        stopTimers();
        setPhase({ kind: "timeout" });
      }, TIMEOUT_MS);
    },
    [stopTimers],
  );

  const call = useCallback(
    (force: boolean) => {
      stopTimers();
      attemptRef.current += 1;
      const attempt = attemptRef.current;
      const fromVersion = versionRef.current;
      inFlightRef.current = true;
      setPhase({ kind: "requesting" });
      portsRef.current
        .requestUpgrade(force)
        .then((result) => {
          if (attempt !== attemptRef.current) return;
          inFlightRef.current = false;
          if (result.accepted) {
            setPhase({
              kind: "upgrading",
              fromVersion,
              targetVersion: result.targetVersion ?? "",
            });
            beginPolling(attempt, fromVersion);
            return;
          }
          if (result.rejectReason === "active_turns") {
            setPhase({
              kind: "active-turns",
              message: result.message ?? "",
              activeTurns: result.activeTurns ?? 0,
              confirmOpen: false,
            });
            return;
          }
          setPhase({ kind: "failed", message: result.message ?? "" });
        })
        .catch((e: unknown) => {
          if (attempt !== attemptRef.current) return;
          inFlightRef.current = false;
          setPhase({ kind: "failed", message: messageOf(e) });
        });
    },
    [beginPolling, stopTimers],
  );

  // 调用还在飞的时候再点一次不发第二条：那台机器的并发闸门会立刻拒绝它，而这条
  // 拒绝百分之百是界面自己招来的。呈现层同时会把主动作禁掉，这里是最后一道。
  const guarded = useCallback(
    (force: boolean) => {
      if (inFlightRef.current) return;
      call(force);
    },
    [call],
  );

  return {
    phase,
    start: () => guarded(false),
    requestForce: () =>
      setPhase((p) =>
        p.kind === "active-turns" ? { ...p, confirmOpen: true } : p,
      ),
    cancelForce: () =>
      setPhase((p) =>
        p.kind === "active-turns" ? { ...p, confirmOpen: false } : p,
      ),
    confirmForce: () => guarded(true),
  };
}
