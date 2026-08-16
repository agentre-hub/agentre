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

function attach(terminalId: string): Fanout {
  const subscribers = new Set<TerminalSubscriber>();
  const dataEvent = `terminal:${terminalId}:data`;
  const exitEvent = `terminal:${terminalId}:exit`;

  // 派发前先快照:订阅者完全可能在 onExit 里退订自己,直接迭代 Set 会边迭代边改。
  const offData: (() => void) | undefined = EventsOn(
    dataEvent,
    (payload: { data: string }) => {
      const bytes = base64ToBytes(payload.data);
      for (const subscriber of [...subscribers]) subscriber.onData(bytes);
    },
  );
  const offExit: (() => void) | undefined = EventsOn(
    exitEvent,
    (payload: TerminalExit) => {
      for (const subscriber of [...subscribers]) subscriber.onExit(payload);
    },
  );

  return {
    subscribers,
    detach() {
      cancelListener(offData, dataEvent);
      cancelListener(offExit, exitEvent);
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
