import { act, render } from "@testing-library/react";
import * as React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useHiddenTabIds } from "../use-hidden-tab-ids";

// happy-dom 没有 IntersectionObserver：装一个只记回调的假实现，由测试按几何喂条目。
type Entry = {
  target: Element;
  isIntersecting: boolean;
  intersectionRatio: number;
};
let fire: (entries: Entry[]) => void = () => {};

class FakeIntersectionObserver {
  constructor(cb: (entries: Entry[]) => void) {
    fire = cb;
  }
  observe() {}
  disconnect() {}
}

function Probe({ onHidden }: { onHidden: (ids: ReadonlySet<string>) => void }) {
  const ref = React.useRef<HTMLDivElement>(null);
  const hidden = useHiddenTabIds(ref, ["a", "b"]);
  onHidden(hidden);
  return (
    <div ref={ref}>
      <span data-tab-id="a" />
      <span data-tab-id="b" />
    </div>
  );
}

describe("useHiddenTabIds", () => {
  beforeEach(() => {
    vi.stubGlobal("IntersectionObserver", FakeIntersectionObserver);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("Given 一个 tab 只露出一半（待关注胶囊在被裁掉的那半边）, When 观察器回报, Then 它算看不见", () => {
    let hidden: ReadonlySet<string> = new Set();
    const { container } = render(<Probe onHidden={(h) => (hidden = h)} />);
    const [a, b] = container.querySelectorAll("[data-tab-id]");

    act(() => {
      fire([
        { target: a, isIntersecting: true, intersectionRatio: 1 },
        { target: b, isIntersecting: true, intersectionRatio: 0.5 },
      ]);
    });

    expect([...hidden]).toEqual(["b"]);
  });

  it("Given 被挤出的 tab 又整格滚回可视区, When 观察器回报, Then 不再算看不见", () => {
    let hidden: ReadonlySet<string> = new Set();
    const { container } = render(<Probe onHidden={(h) => (hidden = h)} />);
    const b = container.querySelectorAll("[data-tab-id]")[1];

    act(() => {
      fire([{ target: b, isIntersecting: false, intersectionRatio: 0 }]);
    });
    expect([...hidden]).toEqual(["b"]);

    act(() => {
      fire([{ target: b, isIntersecting: true, intersectionRatio: 1 }]);
    });
    expect(hidden.size).toBe(0);
  });
});
