// 虚拟化与滚动位置:行模型交出来的那一列,在这里变成虚拟列表。
//
// 三件事都归这一处,因为它们共用同一批 ref,而且顺序耦合:
//   1. 虚拟器本体 —— 估算高度(按消息末行补间距)、测量回调、overscan、以及
//      `anchorTo:"end"` 的流式贴底(理由见下面配置里的注释,逐字搬来);
//   2. 两个滚动位置 ref(`lastScrollOffsetRef` / `restoreScrollOffsetRef`)——
//      tab 隐藏期间记下位置、切回来还原,并在隐藏期把滚动位置判定冻结;
//   3. 行级尾部跟随 —— anchorTo:"end" 只管「行 resize」,而新 tool 卡 / 指示器是
//      「行追加」,那条路由这里补。
// 加上 `renderVirtualRows` / `virtualSpacerSize` 两个渲染期派生。
//
// 调用方(组件)只拿虚拟器、以及那三个渲染期用的值;`scrollToMessage` /
// `scrollToAnchor` 那条跳转链路消费同一个虚拟器,但归 useTranscriptNavigation。
import * as React from "react";
import { useVirtualizer } from "@tanstack/react-virtual";

import {
  estimateRowSizeWithSpacing,
  type TranscriptRow,
} from "@agentre-hub/agentre-ui";

// 距底 ≤32px 视为"贴底",与 chat-panel 的 TRANSCRIPT_BOTTOM_THRESHOLD 同义:
// 它是 anchorTo:"end" 在 live 行流式增长时"是否继续钉底"的容差;
// 用户上滑超过它就不再钉底,保住阅读历史的位置。
const STICK_TO_BOTTOM_THRESHOLD_PX = 32;
const TRANSCRIPT_VIRTUAL_OVERSCAN = 6;

/** 虚拟器类型:交给跳转那一簇做入参,不手抄 TanStack 的泛型。 */
export type TranscriptVirtualizer = ReturnType<
  typeof useTranscriptVirtualizer
>["virtualizer"];

export function useTranscriptVirtualizer({
  rows,
  scrollElement,
  active,
  virtualize,
}: {
  /** 行模型交来的那一列(构建与缓存归 useTranscriptRows)。 */
  rows: TranscriptRow[];
  /** 滚动容器;没挂上前走估算高度。 */
  scrollElement?: HTMLElement | null;
  /** 当前 tab 是否可见;从隐藏切回时还原滚动位置。 */
  active?: boolean;
  /** scrollElement 挂上前也保持虚拟化路径,避免长对话首帧全量 mount。 */
  virtualize?: boolean;
}) {
  const shouldVirtualize = virtualize || scrollElement != null;
  const lastVirtualTotalSizeRef = React.useRef(0);
  const lastScrollRectRef = React.useRef({ height: 0, width: 0 });
  const lastScrollOffsetRef = React.useRef(0);
  const restoreScrollOffsetRef = React.useRef(false);
  const [, forceRestoreRender] = React.useState(0);

  const observeScrollRect = React.useCallback(
    (
      el: HTMLElement | null,
      cb: (rect: { height: number; width: number }) => void,
    ) => {
      const next = {
        height: el?.clientHeight ?? 0,
        width: el?.clientWidth ?? 0,
      };
      if (next.height > 0 || next.width > 0) {
        lastScrollRectRef.current = next;
        cb(next);
        return;
      }
      cb(active ? next : lastScrollRectRef.current);
    },
    [active],
  );

  // eslint-disable-next-line react-hooks/incompatible-library -- TanStack Virtual intentionally owns mutable measurement callbacks.
  const virtualizer = useVirtualizer({
    count: rows.length,
    estimateSize: (index) =>
      // estimateRowSize(内容高度,按 132→148 等同源校准比例缩放)之上,再按
      // isLastRowOfMessage 补上 transcriptRowPadClass 的间距增量(消息末行 pb-7=28px /
      // 块内行 pb-2.5=10px,与纯乘法缩放旧 padding 得到的 ≈22.4px/≈8.97px 有
      // ≈5.6px/≈1px 缺口——两处 padding 打在同一个 measureElement div 上,详见
      // transcript-rows.ts:estimateRowSizeWithSpacing 的注释)。
      estimateRowSizeWithSpacing(rows, index),
    getItemKey: (index) => rows[index]?.key ?? index,
    getScrollElement: () => scrollElement ?? null,
    initialRect: {
      height: scrollElement?.clientHeight ?? 0,
      width: scrollElement?.clientWidth ?? 0,
    },
    observeElementOffset: (_instance, cb) => {
      const el = scrollElement;
      const readOffset = () => {
        const offset = el?.scrollTop ?? 0;
        if (active && !restoreScrollOffsetRef.current) {
          lastScrollOffsetRef.current = offset;
          return offset;
        }
        if (offset > 0) {
          lastScrollOffsetRef.current = offset;
          return offset;
        }
        return lastScrollOffsetRef.current;
      };
      cb(readOffset(), false);
      if (!el) return;
      let scrollEndTimer: number | null = null;
      const handler = () => {
        const offset = readOffset();
        cb(offset, true);
        if (scrollEndTimer != null) window.clearTimeout(scrollEndTimer);
        scrollEndTimer = window.setTimeout(() => {
          cb(readOffset(), false);
          scrollEndTimer = null;
        }, 150);
      };
      el.addEventListener("scroll", handler, { passive: true });
      return () => {
        if (scrollEndTimer != null) window.clearTimeout(scrollEndTimer);
        el.removeEventListener("scroll", handler);
      };
    },
    observeElementRect: (_instance, cb) => {
      const el = scrollElement ?? null;
      observeScrollRect(el, cb);
      if (!el || typeof ResizeObserver === "undefined") return;
      const observer = new ResizeObserver(() => {
        observeScrollRect(el, cb);
      });
      observer.observe(el);
      return () => observer.disconnect();
    },
    overscan: TRANSCRIPT_VIRTUAL_OVERSCAN,
    // 流式贴底交给虚拟器自己的测量回路,而不是 chat-panel 在每个 chunk 用
    // scrollTop=maxScrollTop 手动追(那条路读的是异步复测前的旧 getTotalSize,
    // 永远慢一帧→最新输出被压到折叠线以下)。anchorTo:"end" 在 live 行被
    // ResizeObserver 复测变高、且当前距底 ≤ 阈值时,于 resizeItem 测量回路内
    // 同步把滚动钉回底部,天然消除"慢一帧";上滑超过阈值则不钉,保住阅读位置。
    //
    // 刻意不开 followOnAppend:追"新追加整条消息"已由 chat-panel 的结构性 follow
    //(atBottom 时随 messages 变化滚到底)覆盖;而 followOnAppend 会在会话打开、
    // messages 从 0→N 时把空列表判定为"在末尾"抢先 scrollToEnd,覆盖掉
    //「恢复到上次上滑位置」的还原(正是要修的 wrong-restore),故不启用。
    anchorTo: "end",
    scrollEndThreshold: STICK_TO_BOTTOM_THRESHOLD_PX,
  });
  React.useLayoutEffect(() => {
    if (!scrollElement) return;
    virtualizer.measure();
  }, [scrollElement, virtualizer]);
  React.useLayoutEffect(() => {
    if (!active) {
      restoreScrollOffsetRef.current = true;
      return;
    }
    if (!restoreScrollOffsetRef.current) return;
    restoreScrollOffsetRef.current = false;
    const el = scrollElement;
    if (!el) return;
    const offset = lastScrollOffsetRef.current;
    if (el.scrollTop !== offset) el.scrollTop = offset;
    el.dispatchEvent(new Event("scroll"));
    forceRestoreRender((version) => version + 1);
  }, [active, scrollElement, virtualizer]);
  // 注意:这里不能在 active 翻成 true 时再调 virtualizer.measure()。
  // measure() 会 itemSizeCache.clear() 把所有行的真实测量值丢弃、整列瞬间塌回
  // estimateSize(132px),切回 tab 时引发可见的塌缩 / 闪烁 reflow。隐藏期间行
  // 根本不在 DOM 里(renderVirtualRows 在 !active 时为 false,故无从复测),重新
  // 可见时 measureElement 的 ResizeObserver 会自然对可见窗口逐行复测,无需整列清缓存。

  // 行级贴底跟随:anchorTo:"end" 只在「行 resize」时钉底(流式文本生长走那条路),
  // 而行模型下新 tool 卡 / indicator 是「行追加」—— followOnAppend 因 wrong-restore
  // (见 virtualizer 配置注释)刻意不开,这里自己补:仅当 ①tab 可见且不在恢复期
  // ②非首载(0→N 是打开会话回放,要让位给滚动恢复)③确实是尾部追加 ④追加前
  // 用户贴底(按追加前的 totalSize 判定)时,把滚动钉到新的末尾。
  const followTailRef = React.useRef({
    count: 0,
    tailKey: null as string | null,
    totalSize: 0,
  });
  React.useLayoutEffect(() => {
    const el = scrollElement;
    const prev = followTailRef.current;
    const tailKey = rows.at(-1)?.key ?? null;
    followTailRef.current = {
      count: rows.length,
      tailKey,
      totalSize: virtualizer.getTotalSize(),
    };
    if (!el || !active || restoreScrollOffsetRef.current) return;
    if (prev.count === 0) return;
    if (rows.length <= prev.count || tailKey === prev.tailKey) return;
    const wasAtEnd =
      prev.totalSize <= el.clientHeight ||
      el.scrollTop + el.clientHeight >=
        prev.totalSize - STICK_TO_BOTTOM_THRESHOLD_PX;
    if (!wasAtEnd) return;
    virtualizer.scrollToOffset(virtualizer.getTotalSize(), { align: "end" });
  }, [rows, active, scrollElement, virtualizer]);

  const renderVirtualRows =
    shouldVirtualize && active && !restoreScrollOffsetRef.current;
  const virtualTotalSize = virtualizer.getTotalSize();
  if (virtualTotalSize > 0) {
    lastVirtualTotalSizeRef.current = virtualTotalSize;
  }
  const virtualSpacerSize =
    virtualTotalSize > 0
      ? virtualTotalSize
      : // 48→54:同上,per-row 兜底估值随字号/间距校准同步调整,避免首帧 spacer
        // 高度系统性偏矮。
        lastVirtualTotalSizeRef.current || rows.length * 54;

  return {
    /** 是否走虚拟列表(`virtualize` 或已挂上 `scrollElement`)。外层容器与它一致。 */
    shouldVirtualize,
    virtualizer,
    renderVirtualRows,
    virtualSpacerSize,
  };
}
