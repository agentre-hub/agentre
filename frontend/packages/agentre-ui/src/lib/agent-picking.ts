/**
 * 「挑一个 Agent 开新对话」的分组与排序。
 *
 * 这条规则在两端本来各有一份实现，判据与顺序完全一致、呈现不一致：
 *   - 桌面端 `command-palette/sources/new-chat-source.tsx` 把「上次选过的」冒泡到
 *     可对话组的最前，不可对话的挂一个「需要先配置」次级标题；
 *   - agentre-server 的新对话列表把「最近用过」单列成一组，三组各带标题与计数。
 *
 * 进包的只有**判据与顺序**这一层。标题、容器、计数、点击去向仍在各自宿主：
 * 冒泡进同一组还是单列一组，是两个产品面的决定，不是同一条规则的两种写法。
 *
 * 泛型而不是收一个固定形状：两端的 Agent 根本不是同一个类型（桌面端是数字 `id`
 * 的 wails 生成对象，server 是字符串 `sync_id` 的 REST 载荷），而这只函数只需要
 * 「怎么取 key」「能不能开」「置不置顶」三件事，其余一概不看。
 */

export interface AgentPickingInput<T> {
  agents: T[];
  /** 怎么从一个 Agent 取它的稳定标识。要与 `recentKeys` 里的取值同源。 */
  key: (agent: T) => string;
  /**
   * 现在能不能用它开一条对话。
   *
   * 两端的判据不同（桌面端是 `chattable`，server 是 `has_available_target`），
   * 但结论是同一件事，所以由宿主给谓词、不由本函数认识那些字段。
   */
  available: (agent: T) => boolean;
  /** 用户置顶的。不传 = 这个宿主没有「置顶」这回事，顺序原样保留。 */
  pinned?: (agent: T) => boolean;
  /** 最近用过 / 上次选过的 key，**最近在前**。 */
  recentKeys?: string[];
}

export interface AgentPickingGroups<T> {
  /**
   * 最近用过、且现在开得了的，按 `recentKeys` 的顺序。
   *
   * 不重排成账号顺序 —— 「最近用过」的价值就是那个顺序。
   */
  recent: T[];
  /** 其余开得了的：置顶在前，其余保持原顺序。**不含** `recent` 里那些。 */
  available: T[];
  /**
   * 现在开不了的：置顶在前，其余保持原顺序。
   *
   * **它们不该被隐藏。** 藏起来会让人以为 Agent 丢了；宿主应当渲染成不可点、
   * 并说明原因，而不是不渲染，也不是可点、点完才告诉人「现在选不了」。
   */
  unavailable: T[];
}

/** 置顶提前，其余保持原顺序（稳定）。不传谓词时原样返回。 */
function pinnedFirst<T>(items: T[], pinned?: (agent: T) => boolean): T[] {
  if (!pinned) return items;
  return [...items.filter(pinned), ...items.filter((a) => !pinned(a))];
}

export function groupAgentsForPicking<T>({
  agents,
  key,
  available,
  pinned,
  recentKeys = [],
}: AgentPickingInput<T>): AgentPickingGroups<T> {
  const byKey = new Map(agents.map((a) => [key(a), a]));

  const recent: T[] = [];
  const recentKeysTaken = new Set<string>();
  for (const k of recentKeys) {
    // 同一个 key 记了两次时只算一次：同一个 Agent 在同一屏出现两次，读者会以为
    // 是两个。
    if (recentKeysTaken.has(k)) continue;
    const agent = byKey.get(k);
    // 「最近用过」是本地记的，那个 Agent 可能已经被删了 —— 取不到就跳过。
    // 「现在开不了」也不进这一组：把一个点不动的 Agent 摆在最显眼的第一组，
    // 等于把死路放在最前面；它照常出现在 unavailable 里。
    if (!agent || !available(agent)) continue;
    recentKeysTaken.add(k);
    recent.push(agent);
  }

  const rest = agents.filter((a) => !recentKeysTaken.has(key(a)));

  return {
    recent,
    available: pinnedFirst(rest.filter(available), pinned),
    unavailable: pinnedFirst(
      rest.filter((a) => !available(a)),
      pinned,
    ),
  };
}
