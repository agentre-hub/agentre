/**
 * 「这一轮进行中排着的那几条插话」的状态迁移。
 *
 * 两个宿主共用这一份：桌面端从 chat_svc 的入队应答拿句柄，浏览器控制台从
 * `runtime.steer` 的应答拿。两边要遵守的是同一套规矩——什么时候消费、什么时候
 * 抵消、什么时候干脆不复活——各写一遍必然分叉。
 *
 * 它只认**一条会话此刻的队列**：按 sessionId 存还是按 (设备, 会话) 存、被清空的
 * 那几条去哪儿，都是宿主的事。
 */

/** 队列里的一条。`id` 是撤回时递给对端的句柄。 */
export type QueuedItem = {
  id: string;
  text: string;
  /** 这条链路上撤不撤得掉（对端自报）。降级档与未落定的一律为假。 */
  cancellable: boolean;
  /**
   * 还没拿到**执行端**认的那个句柄：要么应答还在路上，要么对端还没升级到会回传
   * 句柄的那一版。这种条目撤不掉，消费时也只能按文本抵消。
   *
   * 可选：省略等同于「已落定」，桌面端那条路(入队应答即带句柄)因此不必写它。
   */
  pending?: boolean;
  /**
   * 挂在这条上的一句说明（悬停可见），眼下只有一个来源：撤回被对端拒了，而它给出
   * 的那句已本地化的原话。有它时锁图标讲的是**这一条**为什么撤不动，而不是那句
   * 通用的「这个后端不支持撤回」。
   */
  note?: string;
};

/** 一条被后端取走的插话，来自 `steer_consumed`。 */
export type ConsumedSteerRef = {
  queuedId?: string;
  text?: string;
};

export type SteerQueueState = {
  items: QueuedItem[];
  /**
   * 本轮已经被消费掉、却没对上任何一条 chip 的权威句柄。
   *
   * 它只为一件事存在：消费事件可能**先于**发送应答到达（对端取得快）。没有这一笔，
   * 随后那次认领会把一条已经进了转录的消息重新挂回队列，而此后再没有任何东西能
   * 清掉它。别的端排的消息也会落进这里，无害——认领只按自己那条本地句柄查。
   */
  consumed: string[];
};

export const emptySteerQueue: SteerQueueState = { items: [], consumed: [] };

/** 记住的已消费句柄条数上限。够覆盖「应答比事件慢」那一瞬，不无界增长。 */
const CONSUMED_MEMORY = 64;

function remember(consumed: string[], id: string): string[] {
  if (consumed.includes(id)) return consumed;
  const next = [...consumed, id];
  return next.length > CONSUMED_MEMORY ? next.slice(-CONSUMED_MEMORY) : next;
}

/**
 * 排进一条**刚提交、还没拿到执行端句柄**的插话。
 *
 * 提交那一刻就挂上去：输入框在提交时已经被清空，这一段字此刻只存在于队列里。
 */
export function enqueueSteer(
  state: SteerQueueState,
  item: { id: string; text: string },
): SteerQueueState {
  return {
    ...state,
    items: [
      ...state.items,
      { id: item.id, text: item.text, cancellable: false, pending: true },
    ],
  };
}

/** 排进一条**入队时就拿到了句柄**的插话（桌面端走的是这条）。 */
export function enqueueSettledSteer(
  state: SteerQueueState,
  item: { id: string; text: string; cancellable: boolean },
): SteerQueueState {
  return {
    ...state,
    items: [...state.items, { ...item, pending: false }],
  };
}

/**
 * 应答回来了：把本地句柄换成执行端认的那个。
 *
 * 三条去向：
 *   - 这个句柄本轮已经被消费过 → 那条 chip **不复活**（事件跑到了应答前面）；
 *   - 应答里没有句柄（对端还没升级）→ 留在降级档，锁住；
 *   - 否则落定，可撤销与否照对端说的填。
 */
export function adoptSteerHandle(
  state: SteerQueueState,
  localId: string,
  handle: { queuedId: string; cancellable: boolean },
): SteerQueueState {
  const index = state.items.findIndex((q) => q.id === localId && q.pending);
  if (index < 0) return state;
  if (handle.queuedId && state.consumed.includes(handle.queuedId)) {
    return {
      ...state,
      items: state.items.filter((_, i) => i !== index),
    };
  }
  if (!handle.queuedId) return state;
  const items = [...state.items];
  items[index] = {
    ...items[index],
    id: handle.queuedId,
    cancellable: handle.cancellable,
    pending: false,
  };
  return { ...state, items };
}

/**
 * 后端取走了这几条：按句柄消费，句柄对不上就按文本抵消**降级档**里最早那一条。
 *
 * 文本抵消只对没落定的条目开放。落定的条目一律只认句柄——同一条会话上别的端也能
 * 插话，它的消费事件同样扇给这一屏，按文本猜会把自己那条清掉：屏幕上那句话没了，
 * 可它还排在对端的队列里。
 */
export function consumeSteers(
  state: SteerQueueState,
  consumed: ConsumedSteerRef[],
): SteerQueueState {
  let items = state.items;
  let seen = state.consumed;
  for (const ref of consumed) {
    const id = ref.queuedId ?? "";
    if (id) {
      const index = items.findIndex((q) => !q.pending && q.id === id);
      if (index >= 0) {
        items = items.filter((_, i) => i !== index);
        continue;
      }
      seen = remember(seen, id);
    }
    const text = ref.text ?? "";
    if (!text) continue;
    const index = items.findIndex((q) => q.pending && q.text === text);
    if (index >= 0) items = items.filter((_, i) => i !== index);
  }
  return { items, consumed: seen };
}

/** 撤回成功、或这条根本没发出去：按句柄移除。 */
export function dropSteers(
  state: SteerQueueState,
  ids: string[],
): SteerQueueState {
  if (ids.length === 0) return state;
  const drop = new Set(ids);
  return { ...state, items: state.items.filter((q) => !drop.has(q.id)) };
}

/**
 * 撤回被对端拒了：这一条**留在原位**，转成撤不掉，并记下对端那句话。
 *
 * 不乐观清掉、也不弹全局提示：撤不掉最常见的原因就是它刚好被取走了，而那时紧接着
 * 的消费事件自然会把它清掉。清早了的话，一条仍排在对端队列里的消息在这一屏上凭空
 * 消失。
 */
export function markSteerNotCancellable(
  state: SteerQueueState,
  id: string,
  note?: string,
): SteerQueueState {
  const index = state.items.findIndex((q) => q.id === id);
  if (index < 0) return state;
  const items = [...state.items];
  items[index] = { ...items[index], cancellable: false, note };
  return { ...state, items };
}

/** 一轮结束（或换了会话）：整份归零，已消费的记录也随这一轮作废。 */
export function clearSteerQueue(_state: SteerQueueState): SteerQueueState {
  return emptySteerQueue;
}
