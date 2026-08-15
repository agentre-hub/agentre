type TranscriptScrollState = {
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
