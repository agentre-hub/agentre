import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useVisibleMessageId } from "../use-visible-message-id";

type IOCallback = (
  entries: IntersectionObserverEntry[],
  observer: IntersectionObserver,
) => void;

interface FakeIO {
  callback: IOCallback;
  observed: Set<Element>;
  root: Element | null;
  observe(el: Element): void;
  unobserve(el: Element): void;
  disconnect(): void;
}

let instances: FakeIO[] = [];

class IOMock {
  callback: IOCallback;
  observed = new Set<Element>();
  root: Element | null;
  constructor(cb: IOCallback, opts?: IntersectionObserverInit) {
    this.callback = cb;
    this.root = (opts?.root as Element | undefined) ?? null;
    instances.push(this);
  }
  observe(el: Element) {
    this.observed.add(el);
  }
  unobserve(el: Element) {
    this.observed.delete(el);
  }
  disconnect() {
    this.observed.clear();
  }
  takeRecords() {
    return [];
  }
}

function makeEntry(
  target: Element,
  isIntersecting: boolean,
): IntersectionObserverEntry {
  return { target, isIntersecting } as unknown as IntersectionObserverEntry;
}

function setupContainer(ids: number[]) {
  const container = document.createElement("section");
  document.body.appendChild(container);
  for (const id of ids) {
    const a = document.createElement("article");
    a.setAttribute("data-message-id", String(id));
    container.appendChild(a);
  }
  return container;
}

describe("useVisibleMessageId", () => {
  let originalIO: typeof IntersectionObserver | undefined;

  beforeEach(() => {
    instances = [];
    originalIO = globalThis.IntersectionObserver;
    (
      globalThis as unknown as { IntersectionObserver: unknown }
    ).IntersectionObserver = IOMock;
  });

  afterEach(() => {
    document.body.innerHTML = "";
    if (originalIO) {
      (
        globalThis as unknown as { IntersectionObserver: unknown }
      ).IntersectionObserver = originalIO;
    } else {
      delete (globalThis as unknown as { IntersectionObserver?: unknown })
        .IntersectionObserver;
    }
  });

  it("returns null when no row is intersecting", () => {
    const container = setupContainer([1, 2, 3]);
    const ref = { current: container };
    const { result } = renderHook(() => useVisibleMessageId(ref));
    expect(result.current).toBeNull();
  });

  it("returns the id of the intersecting row", () => {
    const container = setupContainer([10, 20, 30]);
    const ref = { current: container };
    const { result } = renderHook(() => useVisibleMessageId(ref));
    const io = instances[0];
    act(() => {
      io.callback(
        [makeEntry(container.children[1], true)],
        io as unknown as IntersectionObserver,
      );
    });
    expect(result.current).toBe(20);
  });

  it("picks the earliest intersecting row when several intersect", () => {
    const container = setupContainer([10, 20, 30]);
    const ref = { current: container };
    const { result } = renderHook(() => useVisibleMessageId(ref));
    const io = instances[0];
    act(() => {
      io.callback(
        [
          makeEntry(container.children[2], true),
          makeEntry(container.children[0], true),
        ],
        io as unknown as IntersectionObserver,
      );
    });
    expect(result.current).toBe(10);
  });

  it("updates when previously visible row leaves and another enters", () => {
    const container = setupContainer([10, 20]);
    const ref = { current: container };
    const { result } = renderHook(() => useVisibleMessageId(ref));
    const io = instances[0];
    act(() => {
      io.callback(
        [makeEntry(container.children[0], true)],
        io as unknown as IntersectionObserver,
      );
    });
    expect(result.current).toBe(10);
    act(() => {
      io.callback(
        [
          makeEntry(container.children[0], false),
          makeEntry(container.children[1], true),
        ],
        io as unknown as IntersectionObserver,
      );
    });
    expect(result.current).toBe(20);
  });

  it("returns null when container ref is null", () => {
    const ref = { current: null } as React.RefObject<HTMLElement | null>;
    const { result } = renderHook(() => useVisibleMessageId(ref));
    expect(result.current).toBeNull();
    expect(instances).toHaveLength(0);
  });

  it("uses container as observer root", () => {
    const container = setupContainer([1]);
    const ref = { current: container };
    renderHook(() => useVisibleMessageId(ref));
    expect(instances[0].root).toBe(container);
  });
  describe("mutation handling is incremental", () => {
    const flush = () => act(async () => {});

    it("does not re-query rows when text mutates inside an existing row", async () => {
      const container = setupContainer([1, 2, 3]);
      const ref = { current: container };
      renderHook(() => useVisibleMessageId(ref));
      const row = container.children[1] as HTMLElement;
      const spy = vi.spyOn(Element.prototype, "querySelectorAll");
      try {
        for (let i = 0; i < 5; i++) {
          const p = document.createElement("p");
          p.textContent = `chunk ${i}`;
          row.appendChild(p);
          p.firstChild!.textContent += " more";
          await flush();
        }
        const fullScans = spy.mock.contexts.filter(
          (ctx) => ctx === container || ctx === row,
        );
        expect(fullScans).toHaveLength(0);
        expect(instances[0].observed.size).toBe(3);
      } finally {
        spy.mockRestore();
      }
    });

    it("tracks rows added later and reports them as visible", async () => {
      const container = setupContainer([1]);
      const ref = { current: container };
      const { result } = renderHook(() => useVisibleMessageId(ref));
      const wrapper = document.createElement("div");
      const a = document.createElement("article");
      a.setAttribute("data-message-id", "7");
      wrapper.appendChild(a);
      container.appendChild(wrapper);
      await flush();
      const io = instances[0];
      expect(io.observed.has(a)).toBe(true);
      act(() => {
        io.callback(
          [makeEntry(a, true)],
          io as unknown as IntersectionObserver,
        );
      });
      expect(result.current).toBe(7);
    });

    it("stops tracking removed rows and recomputes the visible id", async () => {
      const container = setupContainer([10, 20]);
      const ref = { current: container };
      const { result } = renderHook(() => useVisibleMessageId(ref));
      const io = instances[0];
      const first = container.children[0];
      act(() => {
        io.callback(
          [makeEntry(first, true), makeEntry(container.children[1], true)],
          io as unknown as IntersectionObserver,
        );
      });
      expect(result.current).toBe(10);
      first.remove();
      await flush();
      expect(io.observed.has(first)).toBe(false);
      expect(result.current).toBe(20);
    });
  });
});
