import { useEffect } from "react";

/** 停止滚动多久之后让滑块淡出。 */
const HIDE_DELAY_MS = 900;

/**
 * 滚动时才显示滚动条滑块，停手 900ms 后淡出。
 *
 * 与 `@agentre-hub/agentre-ui/base.css` 里的滚动条规则是**一对**：那半边把滑块的
 * 颜色绑到 `--sb-thumb` 并默认设为 transparent，这半边在滚动时把变量改成可见色。
 * 少任何一半都不报错——只 import CSS 不调这个 hook，滚动条恒为透明；只调 hook
 * 不 import CSS，变量没人消费。
 *
 * **为什么是改变量的值，而不是切类名**：WKWebView 对 `::-webkit-scrollbar` 的类 /
 * 属性选择器切换不重绘（Apple Dev Forum #759675、WebKit bug #104412），只有 CSS
 * 自定义属性的值变化才走「值查找」路径、触发 thumb 重绘。机制在普通浏览器里一样
 * 成立，所以两端共用同一份。
 *
 * **为什么设在事件 target 上而不是 <html> 上**：让变量沿 DOM 级联，只有正在滚的
 * 那个元素的滚动条亮起来。设在根上会让页面里所有滚动区域一起亮。
 */
export function useAutoHideScrollbars(): void {
  useEffect(() => {
    if (typeof document === "undefined") {
      return;
    }

    const visibleColor =
      "color-mix(in oklab, var(--muted-foreground) 30%, transparent)";
    // WeakMap 而不是 Map：滚动过的元素被卸载后不该因为我们还攥着引用而留在内存里。
    const timers = new WeakMap<HTMLElement, number>();

    const resolveTarget = (event: Event): HTMLElement | null => {
      if (event.target instanceof HTMLElement) {
        return event.target;
      }
      // 页面本身滚动时 target 是 Document，落到根元素上。
      if (event.target instanceof Document) {
        return event.target.documentElement;
      }
      return null;
    };

    const markScrolling = (event: Event) => {
      const target = resolveTarget(event);
      if (!target) {
        return;
      }

      target.style.setProperty("--sb-thumb", visibleColor);

      const previous = timers.get(target);
      if (previous !== undefined) {
        window.clearTimeout(previous);
      }

      const timer = window.setTimeout(() => {
        // 清掉行内值而不是设成 transparent：让它退回样式表里 :root 的那个默认，
        // 于是「默认长什么样」只有一处定义。
        target.style.removeProperty("--sb-thumb");
        timers.delete(target);
      }, HIDE_DELAY_MS);

      timers.set(target, timer);
    };

    // capture 让事件在到达 target 前就被看到——scroll 不冒泡，挂在 document 上
    // 只有捕获阶段收得到。passive 声明我们不会 preventDefault，滚动不被阻塞。
    const listenerOptions: AddEventListenerOptions = {
      capture: true,
      passive: true,
    };

    // scroll 是主信号；wheel / touchmove 兜底处理「已经到边界、不再产生 scroll
    // 事件」以及虚拟列表吞掉 scroll 的情况——少了它们，用户在边界处继续推的时候
    // 滚动条会莫名其妙消失。
    document.addEventListener("scroll", markScrolling, listenerOptions);
    document.addEventListener("wheel", markScrolling, listenerOptions);
    document.addEventListener("touchmove", markScrolling, listenerOptions);

    return () => {
      document.removeEventListener("scroll", markScrolling, { capture: true });
      document.removeEventListener("wheel", markScrolling, { capture: true });
      document.removeEventListener("touchmove", markScrolling, {
        capture: true,
      });
    };
  }, []);
}
