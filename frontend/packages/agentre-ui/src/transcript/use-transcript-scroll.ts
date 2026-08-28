// use-transcript-scroll 是转录区滚动几何的唯一一份实现:贴底跟随、折叠恢复守卫、
// 快照存取、「回到底部」药丸与「下面还有 N 轮」全在这里。整块是纯 DOM 工作 ——
// 不碰宿主的 Wails / store / 路由;宿主只递 tabKey / active / messages 进来,把返回的
// ref、onScroll 与药丸状态接到自己的转录区上。
//
// 这是仓库里踩坑最密的一块,下面几处多段注释各自记着一次真实回归(跟随意图 vs 位置式
// 贴底、折叠恢复的收尾唯一出口、rAF 饿死时的期限、销账条件),搬动/重构时原样保留。

import * as React from "react";

import {
  COLLAPSED_RESTORE_GUARD_MS,
  TRANSCRIPT_BOTTOM_THRESHOLD,
  computeTopVisibleAnchor,
  isCollapsedBelowGuard,
  isTranscriptAtBottom,
  loadTranscriptScrollState,
  nextAutoFollow,
  readScrollMetrics,
  saveTranscriptScrollState,
  type CollapsedScrollRestoreGuard,
  type ScrollMetrics,
  type TranscriptScrollState,
} from "./chat-panel-scroll-state";
import {
  computeBottomVisibleMessageId,
  countTurnsAfterMessage,
  type TurnMessage,
} from "./transcript-turns";

/**
 * scrollToAnchor 是转录渲染器那一侧的能力(虚拟器才知道某条消息落在哪一行):
 * 命中并钉住返回 true,消息尚未进入可见集返回 false,由 hook 退回像素恢复。
 */
export type TranscriptAnchorScroller = (
  anchorId: number,
  anchorOffset: number,
  anchorRowKey?: string,
) => boolean;

export type UseTranscriptScrollOptions = {
  /** 面板是否是当前活跃 tab。false→true 的那一跳会触发折叠恢复守卫。 */
  active: boolean;
  /** 流式内容的身份。每变一次做一次「意图驱动」的贴底,值本身不被读。 */
  liveRevision?: unknown;
  /** 用来数「下面还有 N 轮」;同时作为结构性变化(首屏/追加/落定)的信号。 */
  messages: readonly TurnMessage[];
  scrollToAnchor?: TranscriptAnchorScroller;
  sessionId: number;
  /** 滚动位置的存档键(tab 维度)。缺省表示不存档。 */
  tabKey: string | undefined;
};

export type UseTranscriptScrollResult = {
  /** 折叠恢复守卫的入口。active 切回时 hook 自己会调,宿主一般不用管。 */
  armCollapsedRestore: (saved: TranscriptScrollState) => void;
  /** 视口下沿那条消息;贴底 / 数不出时为 null。 */
  bottomVisibleId: number | null;
  /** 发送、compact、重跑、编辑这类「无论用户在哪都强制跟随到底」的动作。 */
  followBottom: () => void;
  /** 挂到滚动容器的 onScroll 上。 */
  onScroll: () => void;
  /** 滚动容器的 DOM 节点(state),给需要它做 props 的子组件。 */
  scrollElement: HTMLElement | null;
  /** 滚动容器的 ref(不触发重渲),给 IntersectionObserver 这类消费方。 */
  scrollRef: React.RefObject<HTMLElement | null>;
  /** 用户点「回到底部」。 */
  scrollToBottom: () => void;
  /** 挂到滚动容器的 ref 上。 */
  setScrollElement: (node: HTMLElement | null) => void;
  showBackToBottom: boolean;
  /** 视口下沿之后还开了几轮;贴底时为 0。 */
  turnsBelow: number;
};

export function useTranscriptScroll(
  options: UseTranscriptScrollOptions,
): UseTranscriptScrollResult {
  const { active, liveRevision, messages, sessionId, tabKey } = options;
  const scrollStateKey = tabKey;

  // ── Transcript 滚动跟随 ──
  // atBottomRef = 用户上次滚动后是否停在底部附近（32px 容差）。
  // 新内容到达时只有"在底部"才自动跟随，否则保持当前位置不打扰用户阅读。
  const transcriptRef = React.useRef<HTMLElement | null>(null);
  const [transcriptElement, setTranscriptElement] =
    React.useState<HTMLElement | null>(null);
  // 锚点恢复要问转录渲染器,但那是宿主传进来的端口:放 ref 里,免得它的身份变化
  // 把恢复效果的依赖搅动一遍。
  const scrollToAnchorRef = React.useRef(options.scrollToAnchor);
  // 刻意排在下面所有 layout effect 之前:同一次 commit 里 layout effect 按声明顺序
  // 跑,恢复效果读到的必须是这一轮的端口(首挂时尤其 —— 那正是锚点恢复发生的时刻)。
  React.useLayoutEffect(() => {
    scrollToAnchorRef.current = options.scrollToAnchor;
  });
  const atBottomRef = React.useRef(true);
  // autoFollowRef = 「贴底跟随意图」,与 atBottomRef(纯位置:距底 ≤32px)不同 ——
  // 它对「内容增长把底部推远」免疫,只有用户主动上滚才置 false、滚回底部再置 true。
  // 流式逐 chunk 的贴底必须用它做闸:位置式 atBottomRef 在内容增长快过滚动时(正是
  // 流式)会被误判成"用户离开了底部"而永久关掉跟随,导致转录区冻结、输出沉到折叠线下。
  const autoFollowRef = React.useRef(true);
  // 记录上一次滚动后的 scrollTop,用来区分「用户上滚(scrollTop 变小)」与「内容增长/
  // 程序化贴底(scrollTop 不变或变大)」—— 只有前者才解除 autoFollow。
  const lastScrollTopRef = React.useRef(0);
  const [showBackToBottom, setShowBackToBottom] = React.useState(() => {
    const saved = loadTranscriptScrollState(scrollStateKey);
    return Boolean(saved && !saved.atBottom);
  });
  // bottomVisibleId:视口下沿那条消息。「下面还有 N 轮」从它之后开始数;数不出
  // (贴底 / 容器未布局 / 转录里一条消息行都没有)时为 null,药丸退回「回到底部」。
  const [bottomVisibleId, setBottomVisibleId] = React.useState<number | null>(
    null,
  );
  const restoredScrollStateKeyRef = React.useRef<string | null>(null);
  const pendingScrollRestoreRef = React.useRef<{
    key: string;
    scrollTop: number;
  } | null>(null);
  const collapsedScrollSaveGuardRef =
    React.useRef<CollapsedScrollRestoreGuard | null>(null);
  const collapsedRestoreFrameRef = React.useRef<number | null>(null);
  const collapsedRestoreTimerRef = React.useRef<number | null>(null);

  const cancelCollapsedRestoreFrame = React.useCallback(() => {
    if (collapsedRestoreFrameRef.current == null) return;
    window.cancelAnimationFrame(collapsedRestoreFrameRef.current);
    collapsedRestoreFrameRef.current = null;
  }, []);

  const setTranscriptPaintSuppressed = React.useCallback(
    (suppressed: boolean) => {
      const el = transcriptRef.current;
      if (!el) return;
      el.style.visibility = suppressed ? "hidden" : "";
    },
    [],
  );

  const cancelCollapsedRestoreTimer = React.useCallback(() => {
    if (collapsedRestoreTimerRef.current == null) return;
    window.clearTimeout(collapsedRestoreTimerRef.current);
    collapsedRestoreTimerRef.current = null;
  }, []);

  // releaseCollapsedGuard 是「结束这次抑制」的唯一出口:清 guard、让转录区恢复可见、
  // 停掉 rAF 收敛循环与期限定时器。收尾以前在两个分支里逐行重复,漏一行就会把
  // 转录区留在 visibility:hidden 上。
  const releaseCollapsedGuard = React.useCallback(() => {
    collapsedScrollSaveGuardRef.current = null;
    setTranscriptPaintSuppressed(false);
    cancelCollapsedRestoreFrame();
    cancelCollapsedRestoreTimer();
  }, [
    cancelCollapsedRestoreFrame,
    cancelCollapsedRestoreTimer,
    setTranscriptPaintSuppressed,
  ]);

  const saveScrollSnapshot = React.useCallback(
    (snapshot: TranscriptScrollState) => {
      atBottomRef.current = snapshot.atBottom;
      setShowBackToBottom(!snapshot.atBottom);
      // 药丸报的「下面还有 N 轮」从视口下沿那条消息之后数起 —— 边界只随用户滚动
      // 而变,轮数则随 messages 增长自然重算(见下面的 turnsBelow)。贴底时没有
      // 「下面」可言,清掉边界。
      const el = transcriptRef.current;
      setBottomVisibleId(
        snapshot.atBottom || !el ? null : computeBottomVisibleMessageId(el),
      );
      saveTranscriptScrollState(scrollStateKey, snapshot);
    },
    [scrollStateKey],
  );

  const restoreCollapsedScrollPosition = React.useCallback(() => {
    const guard = collapsedScrollSaveGuardRef.current;
    if (!guard || guard.key !== scrollStateKey) return false;
    const el = transcriptRef.current;
    if (!el) return false;
    const metrics = readScrollMetrics(el);
    if (metrics.maxScrollTop >= guard.minMaxScrollTop) {
      const nextScrollTop = guard.atBottom
        ? metrics.maxScrollTop
        : guard.scrollTop;
      el.scrollTop = nextScrollTop;
      saveScrollSnapshot({
        atBottom: guard.atBottom,
        scrollTop: nextScrollTop,
      });
      releaseCollapsedGuard();
      return true;
    }
    if (Date.now() <= guard.until) return false;
    releaseCollapsedGuard();
    return false;
  }, [releaseCollapsedGuard, scrollStateKey, saveScrollSnapshot]);

  const startCollapsedRestoreLoop = React.useCallback(() => {
    cancelCollapsedRestoreFrame();
    const tick = () => {
      collapsedRestoreFrameRef.current = null;
      const guard = collapsedScrollSaveGuardRef.current;
      if (!guard || guard.key !== scrollStateKey) return;
      if (restoreCollapsedScrollPosition()) return;
      if (collapsedScrollSaveGuardRef.current?.key !== scrollStateKey) return;
      collapsedRestoreFrameRef.current = window.requestAnimationFrame(tick);
    };
    collapsedRestoreFrameRef.current = window.requestAnimationFrame(tick);
  }, [
    cancelCollapsedRestoreFrame,
    scrollStateKey,
    restoreCollapsedScrollPosition,
  ]);

  React.useEffect(
    () => () => {
      cancelCollapsedRestoreFrame();
      cancelCollapsedRestoreTimer();
    },
    [cancelCollapsedRestoreFrame, cancelCollapsedRestoreTimer],
  );

  const saveBottomScrollPosition = React.useCallback(
    (metrics: ScrollMetrics) => {
      const el = transcriptRef.current;
      if (!el) return;
      el.scrollTop = metrics.maxScrollTop;
      saveScrollSnapshot({ atBottom: true, scrollTop: el.scrollTop });
    },
    [saveScrollSnapshot],
  );

  const skipWhileCollapsedHeight = React.useCallback(
    (metrics: ScrollMetrics) => {
      const guard = collapsedScrollSaveGuardRef.current;
      if (!guard || guard.key !== scrollStateKey) return false;
      return isCollapsedBelowGuard(metrics, guard);
    },
    [scrollStateKey],
  );

  const armCollapsedScrollRestore = React.useCallback(
    (saved: TranscriptScrollState) => {
      const guard: CollapsedScrollRestoreGuard = {
        atBottom: saved.atBottom,
        key: scrollStateKey ?? "",
        minMaxScrollTop: Math.max(
          0,
          saved.scrollTop - TRANSCRIPT_BOTTOM_THRESHOLD,
        ),
        scrollTop: saved.scrollTop,
        until: Date.now() + COLLAPSED_RESTORE_GUARD_MS,
      };
      collapsedScrollSaveGuardRef.current = guard;
      setTranscriptPaintSuppressed(true);
      startCollapsedRestoreLoop();
      // guard.until 只在 restoreCollapsedScrollPosition 里被比较,而那个函数只有两个
      // 调用方:rAF 收敛循环,和用户滚动。rAF 在窗口被遮挡 / 不在前台时会整段停摆
      // (本地实测 Chromium 停过 6.4s,同期 longtask 最长 143ms —— 是节流不是阻塞),
      // 于是期限永远等不到被检查:转录区无限期停在 visibility:hidden,只有用户滚一下
      // 才解除。所以期限得有自己的定时器 —— setTimeout 同样会被节流,但只是被钳到
      // ~1s 量级,不会停摆。
      cancelCollapsedRestoreTimer();
      collapsedRestoreTimerRef.current = window.setTimeout(() => {
        collapsedRestoreTimerRef.current = null;
        // 期间又 arm 过一次(切走再切回)→ 那次有自己的定时器,这枚过期的不许收尾。
        if (collapsedScrollSaveGuardRef.current !== guard) return;
        releaseCollapsedGuard();
      }, COLLAPSED_RESTORE_GUARD_MS);
    },
    [
      cancelCollapsedRestoreTimer,
      releaseCollapsedGuard,
      scrollStateKey,
      setTranscriptPaintSuppressed,
      startCollapsedRestoreLoop,
    ],
  );

  const handleTranscriptScroll = React.useCallback(() => {
    const el = transcriptRef.current;
    if (!el) return;
    const metrics = readScrollMetrics(el);
    // prevScrollTop 留给 nextAutoFollow 区分「用户上滚」与「内容增长/程序化贴底」。
    const prevScrollTop = lastScrollTopRef.current;
    lastScrollTopRef.current = metrics.scrollTop;
    const guard = collapsedScrollSaveGuardRef.current;
    if (guard && guard.key === scrollStateKey) {
      if (restoreCollapsedScrollPosition()) return;
      if (skipWhileCollapsedHeight(metrics)) return;
    }
    const saved = loadTranscriptScrollState(scrollStateKey);
    if (
      saved?.atBottom &&
      metrics.maxScrollTop > saved.scrollTop + TRANSCRIPT_BOTTOM_THRESHOLD &&
      metrics.scrollTop <= saved.scrollTop + TRANSCRIPT_BOTTOM_THRESHOLD
    ) {
      el.scrollTop = metrics.maxScrollTop;
      saveScrollSnapshot({ atBottom: true, scrollTop: metrics.maxScrollTop });
      autoFollowRef.current = true;
      lastScrollTopRef.current = metrics.maxScrollTop;
      return;
    }
    const atBottom = isTranscriptAtBottom(metrics);
    autoFollowRef.current = nextAutoFollow({
      prev: autoFollowRef.current,
      prevScrollTop,
      scrollTop: metrics.scrollTop,
      atBottom,
    });
    // 非贴底才记锚点(贴底走 followOnAppend / 结构性 follow 还原,不需要)。
    const anchor = atBottom ? null : computeTopVisibleAnchor(el);
    saveScrollSnapshot({
      atBottom,
      scrollTop: metrics.scrollTop,
      ...(anchor ?? {}),
    });
  }, [
    restoreCollapsedScrollPosition,
    saveScrollSnapshot,
    scrollStateKey,
    skipWhileCollapsedHeight,
  ]);

  const setTranscriptNode = React.useCallback((node: HTMLElement | null) => {
    transcriptRef.current = node;
    setTranscriptElement(node);
  }, []);

  React.useLayoutEffect(() => {
    const el = transcriptRef.current;
    if (!el || !scrollStateKey) {
      return;
    }
    if (
      restoredScrollStateKeyRef.current === scrollStateKey &&
      pendingScrollRestoreRef.current?.key !== scrollStateKey
    ) {
      return;
    }
    const saved = loadTranscriptScrollState(scrollStateKey);
    if (!saved || saved.atBottom) {
      pendingScrollRestoreRef.current = null;
      restoredScrollStateKeyRef.current = scrollStateKey;
      return;
    }
    atBottomRef.current = false;
    // 优先锚点恢复:让虚拟器把保存时视口顶那条消息钉回原处,并随逐行复测收敛——
    // 不受"路由重挂时整列还是 estimate 高度→像素 scrollTop 落到错消息"的冷启动漂移。
    if (saved.anchorId != null) {
      if (
        scrollToAnchorRef.current?.(
          saved.anchorId,
          saved.anchorOffset ?? 0,
          saved.anchorRowKey,
        )
      ) {
        pendingScrollRestoreRef.current = null;
        restoredScrollStateKeyRef.current = scrollStateKey;
        return;
      }
      // 锚点消息尚未加载进 displayMessages:先用 scrollTop 占位(避免顶部闪一下),
      // 留 pending 等下次 messages 变化(消息到位)再由 scrollToAnchor 精确钉回。
      pendingScrollRestoreRef.current = {
        key: scrollStateKey,
        scrollTop: saved.scrollTop,
      };
      el.scrollTop = saved.scrollTop;
      return;
    }
    // 回退:旧快照无锚点(贴底时存的 / 保存时无消息行)时,沿用像素恢复 + 逐渲染重试。
    pendingScrollRestoreRef.current = {
      key: scrollStateKey,
      scrollTop: saved.scrollTop,
    };
    el.scrollTop = saved.scrollTop;
    const metrics = readScrollMetrics(el);
    if (metrics.maxScrollTop < saved.scrollTop) {
      return;
    }
    pendingScrollRestoreRef.current = null;
    restoredScrollStateKeyRef.current = scrollStateKey;
  }, [messages, liveRevision, scrollStateKey, sessionId, transcriptElement]);

  React.useLayoutEffect(() => {
    if (pendingScrollRestoreRef.current?.key === scrollStateKey) {
      return;
    }
    if (!atBottomRef.current) {
      return;
    }
    const el = transcriptRef.current;
    if (!el) {
      return;
    }
    // 整个 chat 区被 display:none 收起时(App.tsx 在非 /chat·/projects 路由上这么做)
    // clientHeight=0、scrollHeight 也是 0;此时设 scrollTop=0 会让回来时停在顶部。
    // 跳过,等回到 /chat 后由 active 切换恢复逻辑兜底滚到底部。
    // 注意这挡不住「隐藏 tab」—— 非活跃面板是 visibility:hidden + absolute inset-0
    // (chat-panel-host.tsx:panelFrameClassName),照常参与布局,clientHeight 不为 0。
    if (el.clientHeight === 0) {
      return;
    }
    saveBottomScrollPosition(readScrollMetrics(el));
    // 依赖里只留 messages(结构性变化:首屏加载 / 发送乐观追加 / turn 落定 reload)。
    // 流式逐 chunk 的贴底由下面单独的 effect 接管(挂 liveDelta/liveThinking/...)。
  }, [messages, scrollStateKey, saveBottomScrollPosition]);

  // 流式逐 chunk 贴底。曾经把这件事完全交给虚拟器的 anchorTo:"end"(见 chat.tsx),
  // 但那条路只在「距底 ≤ 32px 钉底容差」时才钉:turn 开头结构性 follow 滚到的是
  // 占位行 estimate 高度的底部,真实流式文本测量出来更高 → 首帧就落后 >32px →
  // anchorTo:"end" 再也咬不回来,整轮转录区冻结、最新输出沉到折叠线下面(回归 bug)。
  // 这里改成「意图驱动」:只要 autoFollowRef(贴底跟随意图,对内容增长免疫、仅用户上滚
  // 才解除,见 nextAutoFollow)为真,就随每个流式增量把滚动钉到真实底部。读的是已提交 DOM
  // 的实时 scrollHeight(readScrollMetrics 同步触发 reflow),不是慢一帧的虚拟器 getTotalSize
  // 估值,故不掉队;钉到真实底部后虚拟器的 anchorTo:"end" 也回到容差内、自然协同而非互抢。
  React.useLayoutEffect(() => {
    if (pendingScrollRestoreRef.current?.key === scrollStateKey) {
      return;
    }
    if (!autoFollowRef.current) {
      return;
    }
    const el = transcriptRef.current;
    if (!el || el.clientHeight === 0) {
      return;
    }
    saveBottomScrollPosition(readScrollMetrics(el));
  }, [liveRevision, scrollStateKey, saveBottomScrollPosition]);

  // 面板重新变成活跃 tab 时，由父层 HostedPanel 传入的 active 信号驱动恢复：
  // 若用户切走前停在底部，就补一次 scrollTop=scrollHeight。上面那个 useLayoutEffect
  // 在整个 chat 区被路由 display:none 收起期间会被 clientHeight===0 跳过，回来时靠这里补。
  const prevActiveRef = React.useRef(active);
  React.useLayoutEffect(() => {
    const prev = prevActiveRef.current;
    prevActiveRef.current = active;
    if (!active || prev) return;
    const saved = loadTranscriptScrollState(scrollStateKey);
    if (saved && saved.scrollTop > 0) {
      armCollapsedScrollRestore(saved);
    }
    if (!atBottomRef.current) {
      return;
    }
    const el = transcriptRef.current;
    if (!el) {
      return;
    }
    const metrics = readScrollMetrics(el);
    if (skipWhileCollapsedHeight(metrics)) {
      return;
    }
    saveBottomScrollPosition(metrics);
  }, [
    active,
    armCollapsedScrollRestore,
    saveBottomScrollPosition,
    scrollStateKey,
    skipWhileCollapsedHeight,
  ]);

  // 切换会话时回到底部
  React.useEffect(() => {
    const saved = loadTranscriptScrollState(scrollStateKey);
    if (saved && !saved.atBottom) {
      atBottomRef.current = false;
      autoFollowRef.current = false;
      return;
    }
    atBottomRef.current = true;
    autoFollowRef.current = true;
    restoredScrollStateKeyRef.current = null;
    pendingScrollRestoreRef.current = null;
  }, [scrollStateKey, sessionId]);

  const handleBackToBottom = React.useCallback(() => {
    const el = transcriptRef.current;
    if (!el) return;
    // 用户主动点「回到底部」= 明确想跟随,恢复 autoFollow(上滚解除后由此重新咬合)。
    autoFollowRef.current = true;
    saveBottomScrollPosition(readScrollMetrics(el));
  }, [saveBottomScrollPosition]);

  // 发送 / compact / 重跑 / 编辑:强制跟随到底部,无论用户当前在哪里。
  const followBottom = React.useCallback(() => {
    atBottomRef.current = true;
    setShowBackToBottom(false);
  }, []);

  // turnsBelow:视口下沿之后还开了几轮。边界只在用户滚动时更新,而轮数挂在 messages
  // 上 —— 用户停在原地不动、agent 又连出几轮时,这个数照样往上走(那正是它要说的事)。
  // 贴底时药丸根本不渲染,不必白算。
  const turnsBelow = React.useMemo(
    () =>
      showBackToBottom ? countTurnsAfterMessage(messages, bottomVisibleId) : 0,
    [bottomVisibleId, messages, showBackToBottom],
  );

  return {
    armCollapsedRestore: armCollapsedScrollRestore,
    bottomVisibleId,
    followBottom,
    onScroll: handleTranscriptScroll,
    scrollElement: transcriptElement,
    scrollRef: transcriptRef,
    scrollToBottom: handleBackToBottom,
    setScrollElement: setTranscriptNode,
    showBackToBottom,
    turnsBelow,
  };
}
