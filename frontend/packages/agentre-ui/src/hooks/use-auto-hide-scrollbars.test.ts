import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAutoHideScrollbars } from "./use-auto-hide-scrollbars";

/**
 * 滚动条的显隐由**改 CSS 自定义属性的值**驱动，不是切类名。
 *
 * 起因是 WKWebView：它对 `::-webkit-scrollbar` 的类/属性选择器切换不重绘
 * （Apple Dev Forum #759675、WebKit bug #104412），只有自定义属性的值变化才走
 * 「值查找」路径、触发 thumb 重绘。机制本身在普通浏览器里一样成立，所以两端共用。
 *
 * 变量设在**事件 target 自己**身上而不是 <html> 上：让它沿 DOM 级联，
 * 只有正在滚的那个元素的 scrollbar 亮起来，兄弟节点不跟着亮。
 *
 * 与 styles/base.css 里那半边是一对，少任何一半滚动条都不会出现（也不报错）。
 */
describe("useAutoHideScrollbars", () => {
  let element: HTMLDivElement;
  let sibling: HTMLDivElement;

  beforeEach(() => {
    vi.useFakeTimers();
    element = document.createElement("div");
    sibling = document.createElement("div");
    document.body.append(element, sibling);
  });

  afterEach(() => {
    vi.useRealTimers();
    element.remove();
    sibling.remove();
  });

  it("滚动时把 --sb-thumb 设成可见色", () => {
    renderHook(() => useAutoHideScrollbars());

    element.dispatchEvent(new Event("scroll"));

    expect(element.style.getPropertyValue("--sb-thumb")).toContain(
      "--muted-foreground",
    );
  });

  it("只有正在滚的那个元素亮，兄弟节点不跟着亮", () => {
    renderHook(() => useAutoHideScrollbars());

    element.dispatchEvent(new Event("scroll"));

    expect(sibling.style.getPropertyValue("--sb-thumb")).toBe("");
  });

  it("停手 900ms 后清掉行内值，退回样式表里的 transparent", () => {
    renderHook(() => useAutoHideScrollbars());

    element.dispatchEvent(new Event("scroll"));
    vi.advanceTimersByTime(900);

    expect(element.style.getPropertyValue("--sb-thumb")).toBe("");
  });

  it("持续滚动会把淡出推后，不会滚到一半自己消失", () => {
    renderHook(() => useAutoHideScrollbars());

    element.dispatchEvent(new Event("scroll"));
    vi.advanceTimersByTime(800);
    element.dispatchEvent(new Event("scroll"));
    vi.advanceTimersByTime(800);

    expect(element.style.getPropertyValue("--sb-thumb")).toContain(
      "--muted-foreground",
    );
  });

  it("wheel 与 touchmove 也算滚动信号", () => {
    // 已经到边界时不再产生 scroll 事件，虚拟列表也可能把 scroll 吞掉；
    // 少了这两个兜底，用户在边界处继续推的时候滚动条会莫名其妙消失。
    renderHook(() => useAutoHideScrollbars());

    element.dispatchEvent(new Event("wheel"));
    expect(element.style.getPropertyValue("--sb-thumb")).not.toBe("");

    vi.advanceTimersByTime(900);
    element.dispatchEvent(new Event("touchmove"));
    expect(element.style.getPropertyValue("--sb-thumb")).not.toBe("");
  });

  it("卸载后不再响应，也不再往 DOM 上写东西", () => {
    const { unmount } = renderHook(() => useAutoHideScrollbars());

    unmount();
    element.dispatchEvent(new Event("scroll"));

    expect(element.style.getPropertyValue("--sb-thumb")).toBe("");
  });
});

describe("宿主拿得到它", () => {
  it("从包的公开出口导出", () => {
    // 断源码文本而不是 import 包的 index：那会把 xterm / 引擎面板整棵树拖进
    // 一个 hook 的单测里。这里要证的只是「出口那一行在不在」。
    const found = [
      resolve(process.cwd(), "packages/agentre-ui/src/index.ts"),
      resolve(process.cwd(), "src/index.ts"),
    ].find((candidate) => existsSync(candidate));

    if (!found) {
      throw new Error("src/index.ts not found from either workspace root");
    }

    // 刻意不写成「导出关键字 + 引号包住的路径」那种正则：boundary.test.ts 用
    // 文本扫描找模块说明符，而正则里转义过的路径在它眼里不以点号开头，会被当成
    // 一个未声明的裸依赖报出来。连这段注释都不能出现那个形状，否则同样中招。
    // 按行判「这是一条导出、且提到了它」同样够。
    const exported = readFileSync(found, "utf8")
      .split("\n")
      .some(
        (line) =>
          line.startsWith("export") && line.includes("useAutoHideScrollbars"),
      );

    expect(exported).toBe(true);
  });
});
