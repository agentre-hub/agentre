import type {
  TerminalExit,
  TerminalSubscriber,
  TerminalTransport,
  TerminalUnsubscribe,
} from "@agentre-ai/agentre-ui";
import * as App from "@/../wailsjs/go/app/App";
import { EventsOn, EventsOff } from "@/../wailsjs/runtime/runtime";

import { base64ToBytes } from "./base64";

/**
 * 桌面端的终端传输实现 —— 共享包与 Wails PTY 绑定 / 事件流之间唯一的接缝。
 *
 * 这里也是**唯一**允许出现 `EventsOn` / `EventsOff` 与 base64 解码的地方。
 * 包里的调用方只认 `useTerminalTransport()` 拿到的 `subscribe/open/...`。
 *
 * ── 被封在这层里的那个坑 ───────────────────────────────────────────────
 * Wails 的 `EventsOff(event)` 摘的是该事件名下的**全部**监听者，不是你自己那个。
 * 于是「两个视图各自 EventsOn 同一条 PTY，其中一个卸载」= 另一个当场断流。
 * 端口的契约要求退订只影响自己，所以这层自持订阅表：
 *
 *   - 每个 terminalId 只向 Wails 注册**一对**监听（data + exit），扇出给 N 个订阅者；
 *   - 退订只是从表里删自己；
 *   - 最后一个订阅者走人时，才真正撤掉那一对监听，且优先用 `EventsOn` 的返回值
 *     （Wails v2 起它是**只摘自己**的取消函数），而不是会误伤的 `EventsOff`。
 *
 * 顺带的好处：base64 每帧只解一次，N 个订阅者共用同一份字节。
 */

type Fanout = {
  subscribers: Set<TerminalSubscriber>;
  /** 撤掉本模块为这个 terminalId 注册的那**一对** Wails 监听。 */
  detach(): void;
};

const fanouts = new Map<string, Fanout>();

/**
 * `EventsOn` 在 Wails v2 返回只摘自己的取消函数；拿不到函数时（更老的 runtime）
 * 只能退回 `EventsOff`，那一步会连坐同名事件的其它监听者 —— 是明确的下策，
 * 所以放在兜底分支而不是主路径。
 */
function cancelListener(off: (() => void) | undefined, event: string) {
  if (typeof off === "function") {
    off();
    return;
  }
  EventsOff(event);
}

/** 一个已经挂到 Wails 上的监听:`off` 缺失时只能退回会误伤的 `EventsOff`。 */
type Registration = { event: string; off?: () => void };

/**
 * 撤监听是**尽力而为**:退订的行为保证(订阅者不再被回调)由上面的订阅表达成,
 * 与底层撤不撤得掉无关。撤失败最多留一个扇给空集合的空转监听 —— 既不该把异常
 * 甩给调用方(它并不持有那个 Wails 监听,无从补救),也不该因为一个失败就放着
 * 另一个不撤。
 */
function releaseRegistrations(registrations: Registration[]) {
  for (const registration of registrations) {
    try {
      cancelListener(registration.off, registration.event);
    } catch {
      // 唯一的“重试”手段是 EventsOff(event),而它会连坐同名事件的其它监听者 ——
      // 正是这层要根治的那个 bug。宁可留一个空转监听,也不去摘别人的。
    }
  }
}

function attach(terminalId: string): Fanout {
  const subscribers = new Set<TerminalSubscriber>();
  const dataEvent = `terminal:${terminalId}:data`;
  const exitEvent = `terminal:${terminalId}:exit`;
  const registrations: Registration[] = [];

  function listen<P>(event: string, handler: (payload: P) => void) {
    const off: (() => void) | undefined = EventsOn(event, handler);
    // 先注册成功再记账:EventsOn 抛出时这一条压根没挂上,记进去反而会让回滚
    // 退回 EventsOff 去摘别人的监听。
    registrations.push({ event, off });
  }

  try {
    // 派发前先快照:订阅者完全可能在 onExit 里退订自己,直接迭代 Set 会边迭代边改。
    listen(dataEvent, (payload: { data: string }) => {
      const bytes = base64ToBytes(payload.data);
      for (const subscriber of [...subscribers]) subscriber.onData(bytes);
    });
    listen(exitEvent, (payload: TerminalExit) => {
      for (const subscriber of [...subscribers]) subscriber.onExit(payload);
    });
  } catch (err) {
    // 半截注册的那一条没人再持有句柄(fanout 从未进表),留着就是永久泄漏。
    releaseRegistrations(registrations);
    throw err;
  }

  return {
    subscribers,
    detach() {
      releaseRegistrations(registrations);
    },
  };
}

export const desktopTerminalTransport: TerminalTransport = {
  subscribe(terminalId, subscriber): TerminalUnsubscribe {
    let fanout = fanouts.get(terminalId);
    if (!fanout) {
      fanout = attach(terminalId);
      fanouts.set(terminalId, fanout);
    }

    // 捕获这一次订阅所属的那个 fanout:退订可能发生在同名 terminalId 已经被
    // 重建（全员退订后又有人订阅）之后,那时不该去动新的那份。
    const registered = fanout;
    registered.subscribers.add(subscriber);

    let released = false;
    return () => {
      if (released) return; // 幂等:重复退订不得把别人的监听摘掉。
      released = true;
      registered.subscribers.delete(subscriber);
      if (registered.subscribers.size > 0) return;

      if (fanouts.get(terminalId) === registered) fanouts.delete(terminalId);
      registered.detach();
    };
  },

  async open({ terminalId, projectId, deviceId, cols, rows }) {
    await App.TerminalOpen(terminalId, projectId, deviceId, cols, rows);
  },

  async close(terminalId) {
    await App.TerminalClose(terminalId);
  },

  async write(terminalId, data) {
    await App.TerminalWrite(terminalId, data);
  },

  async resize(terminalId, cols, rows) {
    await App.TerminalResize(terminalId, cols, rows);
  },
};
