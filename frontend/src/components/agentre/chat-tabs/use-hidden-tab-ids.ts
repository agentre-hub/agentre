import * as React from "react";

// useHiddenTabIds: 哪些 tab 被挤出了 tab 条的可视区（横向滚走了）。
// 用 IntersectionObserver 以 scroller 为 root 观察每个带 data-tab-id 的元素；
// 环境没有 IntersectionObserver（测试）时恒为空集，宿主改走 TabStrip 的 hiddenTabIds 注入。
export function useHiddenTabIds(
  scrollerRef: React.RefObject<HTMLElement | null>,
  tabIds: readonly string[],
): ReadonlySet<string> {
  const [hidden, setHidden] = React.useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const key = tabIds.join("\u0000");

  React.useEffect(() => {
    const root = scrollerRef.current;
    if (!root || typeof IntersectionObserver === "undefined") return;
    const live = new Set(tabIds);
    const observer = new IntersectionObserver(
      (entries) => {
        setHidden((prev) => {
          const next = new Set([...prev].filter((id) => live.has(id)));
          for (const e of entries) {
            const id = (e.target as HTMLElement).dataset.tabId;
            if (!id) continue;
            if (e.isIntersecting) next.delete(id);
            else next.add(id);
          }
          if (
            next.size === prev.size &&
            [...next].every((id) => prev.has(id))
          ) {
            return prev;
          }
          return next;
        });
      },
      { root, threshold: 0.99 },
    );
    root
      .querySelectorAll<HTMLElement>("[data-tab-id]")
      .forEach((el) => observer.observe(el));
    return () => observer.disconnect();
    // key 代表 tabIds 的内容；tabIds 引用每次渲染都新，不能直接当依赖。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollerRef, key]);

  return hidden;
}
