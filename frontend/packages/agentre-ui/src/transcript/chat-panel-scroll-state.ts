export type TranscriptScrollState = {
  atBottom: boolean;
  scrollTop: number;
  // 非贴底时额外保存"视口顶部那条消息"的锚点:anchorId=消息 id,
  // anchorOffset=该消息顶边在视口顶上方的 px。路由重挂载后虚拟器无测量(整列 estimate),
  // 仅凭 scrollTop 像素会落到错的消息;有锚点时改用 scrollToAnchor 钉到该消息并随测量收敛。
  anchorId?: number;
  anchorOffset?: number;
  // 行级虚拟化下长消息拆成多行;anchorRowKey(data-row-key)让恢复精确钉回
  // 视口顶那一行,而不是塌到消息首行。可选:旧快照/无行时按 anchorId 回退。
  anchorRowKey?: string;
};

/** 滚动容器的一次同步测量。读 el 会触发 reflow,所以一次读齐、别分散读。 */
export type ScrollMetrics = {
  clientHeight: number;
  maxScrollTop: number;
  scrollHeight: number;
  scrollTop: number;
};

/**
 * 折叠恢复守卫:tab 切回时虚拟器的总高会先塌陷一瞬,此期间的滚动事件与跟随都
 * 不算数。minMaxScrollTop 是"高度算恢复了"的门槛,until 是这次抑制的期限。
 */
export type CollapsedScrollRestoreGuard = TranscriptScrollState & {
  key: string;
  minMaxScrollTop: number;
  until: number;
};

/** 距底 ≤32px 视为贴底。 */
export const TRANSCRIPT_BOTTOM_THRESHOLD = 32;
export const COLLAPSED_RESTORE_GUARD_MS = 3_000;

export function readScrollMetrics(el: HTMLElement): ScrollMetrics {
  const { clientHeight, scrollHeight, scrollTop } = el;
  return {
    clientHeight,
    maxScrollTop: Math.max(0, scrollHeight - clientHeight),
    scrollHeight,
    scrollTop,
  };
}

export function isTranscriptAtBottom(metrics: ScrollMetrics): boolean {
  return (
    metrics.scrollHeight - metrics.scrollTop - metrics.clientHeight <=
    TRANSCRIPT_BOTTOM_THRESHOLD
  );
}

export function isCollapsedBelowGuard(
  metrics: ScrollMetrics,
  guard: CollapsedScrollRestoreGuard,
): boolean {
  return (
    Date.now() <= guard.until && metrics.maxScrollTop < guard.minMaxScrollTop
  );
}

// computeTopVisibleAnchor 找滚动容器内"视口顶部那条消息"——即第一条底边已越过视口顶
// 的 [data-message-id] 行(虚拟列表里 DOM 顺序≈消息顺序,故首条命中即视口顶那条),
// 返回其 id 与顶边在视口顶上方的 px。非贴底保存时记下它,路由重挂后据此 scrollToAnchor
// 钉回该消息,避免仅凭像素 scrollTop 在"整列还是 estimate 高度"时落到错消息的漂移。
// 找不到(无消息行 / 容器未布局)返回 null,调用方退回纯像素快照。
export function computeTopVisibleAnchor(el: HTMLElement): {
  anchorId: number;
  anchorOffset: number;
  anchorRowKey?: string;
} | null {
  const containerTop = el.getBoundingClientRect().top;
  const rows = el.querySelectorAll<HTMLElement>("[data-message-id]");
  for (const row of rows) {
    const rect = row.getBoundingClientRect();
    if (rect.bottom <= containerTop) continue; // 完全在视口上方,跳过
    const id = Number(row.getAttribute("data-message-id"));
    if (!Number.isFinite(id)) continue;
    // 行级虚拟化下一条消息拆成多行,offset 相对的是「这一行」的顶边 —— 把行身份
    // (data-row-key)一并记下,恢复端才能钉回同一行而不是塌到消息首行。
    const rowKey = row.getAttribute("data-row-key");
    return {
      anchorId: id,
      anchorOffset: Math.max(0, containerTop - rect.top),
      ...(rowKey ? { anchorRowKey: rowKey } : {}),
    };
  }
  return null;
}

// nextAutoFollow 维护「贴底跟随意图」(autoFollow),与「位置式是否在底部容差内」
// (atBottom)是两回事。流式逐 chunk 输出时内容增长会快过滚动,使 scrollTop 暂时落后
// 底部 >32px;若按位置直接判 atBottom=false 关掉跟随,转录区就会冻结、输出沉到折叠线下
// (回归 bug)。所以这里让 autoFollow 对「内容增长把底部推远」免疫:
//   - 回到底部容差内 → 跟随(true);
//   - 用户主动上滚(scrollTop 明显变小)且不在底部 → 解除(false);
//   - 其余(内容增长 / 程序化贴底,scrollTop 不变或变大)→ 保持原值(sticky)。
export function nextAutoFollow(args: {
  prev: boolean;
  prevScrollTop: number;
  scrollTop: number;
  atBottom: boolean;
}): boolean {
  const { prev, prevScrollTop, scrollTop, atBottom } = args;
  if (atBottom) return true;
  if (scrollTop < prevScrollTop - 1) return false;
  return prev;
}

const transcriptScrollStates = new Map<string, TranscriptScrollState>();
const transcriptDraftStates = new Map<string, unknown>();

export function saveTranscriptScrollState(
  key: string | undefined,
  state: TranscriptScrollState,
): void {
  if (!key) return;
  transcriptScrollStates.set(key, state);
}

export function loadTranscriptScrollState(
  key: string | undefined,
): TranscriptScrollState | null {
  if (!key) return null;
  return transcriptScrollStates.get(key) ?? null;
}

export function pruneChatPanelScrollState(activeKeys: Set<string>): void {
  for (const key of transcriptScrollStates.keys()) {
    if (!activeKeys.has(key)) transcriptScrollStates.delete(key);
  }
  for (const key of transcriptDraftStates.keys()) {
    const tabKey = key.slice(0, key.indexOf(":"));
    if (!activeKeys.has(tabKey)) transcriptDraftStates.delete(key);
  }
}

export function saveTranscriptDraftState<T>(
  tabKey: string | undefined,
  draftKey: string | undefined,
  state: T,
): void {
  if (!tabKey || !draftKey) return;
  transcriptDraftStates.set(`${tabKey}:${draftKey}`, state);
}

export function loadTranscriptDraftState<T>(
  tabKey: string | undefined,
  draftKey: string | undefined,
): T | null {
  if (!tabKey || !draftKey) return null;
  return (transcriptDraftStates.get(`${tabKey}:${draftKey}`) as T) ?? null;
}

export function clearTranscriptDraftState(
  tabKey: string | undefined,
  draftKey: string | undefined,
): void {
  if (!tabKey || !draftKey) return;
  transcriptDraftStates.delete(`${tabKey}:${draftKey}`);
}

export function __resetChatPanelScrollStateForTesting(): void {
  transcriptScrollStates.clear();
  transcriptDraftStates.clear();
}
