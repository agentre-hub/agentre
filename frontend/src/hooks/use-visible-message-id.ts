import * as React from "react";

// useVisibleMessageId 在 transcript 滚动容器上挂 IntersectionObserver，
// 实时算出"当前用户视野焦点对应的消息 id"，供右侧 outline 高亮联动。
//
// 契约：
//   - root = containerRef.current；rootMargin 把命中区裁到容器顶部约 1/3 范围，
//     模拟"读到这条"的直觉位置；
//   - 同一时刻多条命中 → 取 DOM 顺序最靠前的那条（最贴近顶部）；
//   - 容器内消息行增删 → MutationObserver 仅按增删的子树增量订阅/退订，
//     行内部的流式文本变动不触发任何全量扫描（开销与 transcript 总长度无关）；
//   - 容器 ref 为 null 或环境不支持 IntersectionObserver → 返回 null，
//     不报错也不订阅，便于 SSR / 测试。
export function useVisibleMessageId(
  containerRef: React.RefObject<HTMLElement | null>,
): number | null {
  const [active, setActive] = React.useState<number | null>(null);

  React.useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    if (typeof IntersectionObserver === "undefined") return;

    const intersecting = new Set<Element>();
    const observed = new Set<Element>();

    // 只遍历命中集合（视口内的少量行），按 DOM 顺序取最靠前的一条。
    const recompute = () => {
      let first: Element | null = null;
      for (const el of intersecting) {
        const v = Number(el.getAttribute("data-message-id"));
        if (!Number.isFinite(v) || v <= 0) continue;
        if (
          !first ||
          first.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_PRECEDING
        ) {
          first = el;
        }
      }
      const next = first ? Number(first.getAttribute("data-message-id")) : null;
      setActive((prev) => (prev === next ? prev : next));
    };

    const observer = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (e.isIntersecting) intersecting.add(e.target);
          else intersecting.delete(e.target);
        }
        recompute();
      },
      {
        root: container,
        rootMargin: "0px 0px -66% 0px",
        threshold: [0, 0.01],
      },
    );

    // 只在新增/移除的子树里找消息行。
    const rowsIn = (node: Node): HTMLElement[] => {
      if (!(node instanceof HTMLElement)) return [];
      const out: HTMLElement[] = [];
      if (node.hasAttribute("data-message-id")) out.push(node);
      out.push(
        ...Array.from(node.querySelectorAll<HTMLElement>("[data-message-id]")),
      );
      return out;
    };
    const track = (row: Element) => {
      if (observed.has(row)) return;
      observed.add(row);
      observer.observe(row);
    };
    const untrack = (row: Element) => {
      if (!observed.delete(row)) return;
      observer.unobserve(row);
      intersecting.delete(row);
    };

    for (const row of container.querySelectorAll("[data-message-id]")) {
      track(row);
    }
    recompute();

    const mut = new MutationObserver((records) => {
      let changed = false;
      for (const rec of records) {
        for (const n of rec.removedNodes) {
          for (const row of rowsIn(n)) {
            // 被移走又挂回容器内（重排）的行不退订。
            if (!container.contains(row) && observed.has(row)) {
              untrack(row);
              changed = true;
            }
          }
        }
        for (const n of rec.addedNodes) {
          for (const row of rowsIn(n)) {
            if (container.contains(row) && !observed.has(row)) {
              track(row);
              changed = true;
            }
          }
        }
      }
      if (changed) recompute();
    });
    mut.observe(container, { childList: true, subtree: true });

    return () => {
      observer.disconnect();
      mut.disconnect();
    };
  }, [containerRef]);

  return active;
}
