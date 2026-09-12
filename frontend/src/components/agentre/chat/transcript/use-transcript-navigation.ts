// 跳转与锚点:转录对外的两条定位 API,以及它们各自那条延迟生效的路。
//
// `scrollToMessage` —— 按 messageId 跳。三条路:目标已有行 → 直接滚;被压缩折叠挡住
// → 先展开、把意图挂起;正文还没取回(大纲列的是整条会话的轮次,点它跳转是常规动作)
// → 先记意图再去取,由那个 effect 在行真正出现后落位。直接滚是滚不动的:此刻它还不在
// 渲染的行里。挂起的意图还可能落了空(目标被编辑/重跑截断),那时收手。
//
// `scrollToAnchor` —— 把"保存时位于视口顶部的那条消息"钉回距视口顶 offset px 处。
// 路由重挂时虚拟器整列只有 estimate 高度,`getOffsetForIndex` 会随可见窗口逐行复测而
// 变化;这里用 rAF 循环重算 target 直到稳定(连续 2 帧不变)或封顶,从而消除"仅凭像素
// scrollTop 会落到错消息"的冷启动漂移 —— 锚点钉的是消息身份,不是像素值。
//
// 两条都写进 `useImperativeHandle`,所以这个 hook **没有返回值**:调用方(组件)不需要
// 拿任何东西,`ref` 就是出口。
//
// 另外两件挂着它的活:滚回顶自动取更早正文(带 240px 余量,让用户在真正撞到顶之前就
// 开始取),以及组件卸载时掐掉在飞的 rAF 收敛循环。
import * as React from "react";

import { chat_svc } from "../../../../../wailsjs/go/models";
import type { ChatTranscriptHandle } from "../transcript";
import type { TranscriptVirtualizer } from "./use-transcript-virtualizer";

// EARLIER_MESSAGES_SCROLL_THRESHOLD_PX:滚到距顶多近就去取更早的正文。留一段余量,
// 让用户在真正撞到顶之前就开始取,而不是先看见一段空白。
const EARLIER_MESSAGES_SCROLL_THRESHOLD_PX = 240;

export function useTranscriptNavigation({
  ref,
  virtualizer,
  scrollElement,
  firstRowIndexByMessageId,
  rowIndexByKey,
  folding,
  messages,
  allMessages,
  setExpanded,
  hasEarlierMessages,
  onLoadEarlier,
}: {
  /** 组件转发来的 imperative handle 出口;两条 API 由本 hook 写进去。 */
  ref: React.ForwardedRef<ChatTranscriptHandle>;
  virtualizer: TranscriptVirtualizer;
  scrollElement?: HTMLElement | null;
  firstRowIndexByMessageId: ReadonlyMap<number, number>;
  rowIndexByKey: ReadonlyMap<string, number>;
  /** 正文在取回中,或更早那段还没取。 */
  folding: boolean;
  /** 切掉未取正文前缀、且折叠后的消息表。 */
  messages: chat_svc.ChatMessage[];
  /** 会话的完整消息表(含正文尚未取回的那段前缀)。 */
  allMessages: chat_svc.ChatMessage[];
  /** 展开被折叠的那一段。 */
  setExpanded: React.Dispatch<React.SetStateAction<boolean>>;
  /** 还有更早的消息只拿到了元数据。 */
  hasEarlierMessages?: boolean;
  /** 取回更早那一段正文;未传 = 不给入口(只读调用方)。 */
  onLoadEarlier?: () => void;
}): void {
  // onLoadEarlierRef 转发「取回更早正文」的回调,免得 chat-panel 每次重渲的 inline
  // lambda 把滚动监听器反复摘挂;重复触发由调用方的在飞守卫吸收。
  const onLoadEarlierRef = React.useRef(onLoadEarlier);
  React.useEffect(() => {
    onLoadEarlierRef.current = onLoadEarlier;
  }, [onLoadEarlier]);

  const [pendingScrollMessageId, setPendingScrollMessageId] = React.useState<
    number | null
  >(null);

  const scrollToMessage = React.useCallback(
    (messageId: number) => {
      // 消息首行 = 消息顶,align:"start" 视觉等价于旧 message 级行为。
      const index = firstRowIndexByMessageId.get(messageId);
      if (index != null) {
        virtualizer.scrollToIndex(index, { align: "start" });
        return;
      }
      if (folding && messages.some((m) => m.id === messageId)) {
        setExpanded(true);
        setPendingScrollMessageId(messageId);
        return;
      }
      // 目标是一条正文还没取回来的旧消息(大纲列的是**整条会话**的轮次,点它跳转是
      // 常规动作)。先记下意图再去取,由下面那个 effect 在行真正出现后落位 ——
      // 直接滚是滚不动的:此刻它还不在渲染的行里。
      if (allMessages.some((m) => m.id === messageId)) {
        setPendingScrollMessageId(messageId);
        onLoadEarlierRef.current?.();
      }
    },
    // setExpanded 由 useTranscriptRows 交回:它仍是 useState 的原始 setter(引用恒定),
    // 列进来只是让 lint 认得出这一点 —— 不列的话规则已不把它当 state setter。
    [
      allMessages,
      firstRowIndexByMessageId,
      folding,
      messages,
      setExpanded,
      virtualizer,
    ],
  );

  React.useEffect(() => {
    if (pendingScrollMessageId == null) return;
    const index = firstRowIndexByMessageId.get(pendingScrollMessageId);
    if (index == null) {
      const target = allMessages.find((m) => m.id === pendingScrollMessageId);
      // 目标已经不在表里(被编辑/重跑截断了):这次跳转没有落点,收手。
      if (!target) {
        setPendingScrollMessageId(null);
        return;
      }
      // 正文还没取到就继续往前取;取到头(没有更早的)时留着意图不动 ——
      // 折叠展开那条路正是在这个窗口里等下一次重渲的。
      if (target.blocksLoaded === false && hasEarlierMessages) {
        onLoadEarlierRef.current?.();
      }
      return;
    }
    virtualizer.scrollToIndex(index, { align: "start" });
    setPendingScrollMessageId(null);
  }, [
    allMessages,
    firstRowIndexByMessageId,
    hasEarlierMessages,
    pendingScrollMessageId,
    virtualizer,
  ]);

  const anchorRestoreFrameRef = React.useRef<number | null>(null);
  const cancelAnchorRestore = React.useCallback(() => {
    if (anchorRestoreFrameRef.current != null) {
      window.cancelAnimationFrame(anchorRestoreFrameRef.current);
      anchorRestoreFrameRef.current = null;
    }
  }, []);
  // scrollToAnchor:把"保存时位于视口顶部的那条消息"(messageId)重新钉到距视口顶
  // offset px 处。路由重挂时虚拟器整列只有 estimate 高度,getOffsetForIndex 会随可见
  // 窗口逐行复测而变化;这里用 rAF 循环重算 target 直到稳定(连续 2 帧不变)或封顶,
  // 从而消除"仅凭像素 scrollTop 会落到错消息"的冷启动漂移——锚点钉的是消息身份,
  // 不是像素值。返回 false=该消息不在 displayMessages(被折叠/未加载),交回调用方
  // 回退像素恢复。
  const scrollToAnchor = React.useCallback(
    (messageId: number, offset: number, rowKey?: string): boolean => {
      const index =
        (rowKey != null ? rowIndexByKey.get(rowKey) : undefined) ??
        firstRowIndexByMessageId.get(messageId) ??
        -1;
      const el = scrollElement;
      if (index < 0 || !el) return false;
      cancelAnchorRestore();
      let prevTarget = -1;
      let stableFrames = 0;
      let frames = 0;
      const settle = () => {
        anchorRestoreFrameRef.current = null;
        const info = virtualizer.getOffsetForIndex(index, "start");
        if (!info) return;
        const target = Math.max(0, info[0] + offset);
        if (Math.abs(el.scrollTop - target) > 1) el.scrollTop = target;
        stableFrames =
          Math.abs(target - prevTarget) <= 1 ? stableFrames + 1 : 0;
        prevTarget = target;
        frames += 1;
        if (stableFrames < 2 && frames < 30) {
          anchorRestoreFrameRef.current = window.requestAnimationFrame(settle);
        }
      };
      // 同步先钉一帧(调用点是 chat-panel 的 useLayoutEffect,paint 前生效),
      // 避免路由重挂首帧闪在顶部;后续逐帧由 settle 自己挂 rAF 收敛。
      settle();
      return true;
    },
    [
      cancelAnchorRestore,
      firstRowIndexByMessageId,
      rowIndexByKey,
      scrollElement,
      virtualizer,
    ],
  );
  React.useEffect(() => () => cancelAnchorRestore(), [cancelAnchorRestore]);

  React.useEffect(() => {
    const el = scrollElement;
    if (!el || !hasEarlierMessages || !onLoadEarlier) return;
    const handler = () => {
      if (el.scrollTop > EARLIER_MESSAGES_SCROLL_THRESHOLD_PX) return;
      onLoadEarlierRef.current?.();
    };
    el.addEventListener("scroll", handler, { passive: true });
    return () => el.removeEventListener("scroll", handler);
  }, [hasEarlierMessages, onLoadEarlier, scrollElement]);

  React.useImperativeHandle(
    ref,
    () => ({
      scrollToMessage,
      scrollToAnchor,
    }),
    [scrollToAnchor, scrollToMessage],
  );
}
