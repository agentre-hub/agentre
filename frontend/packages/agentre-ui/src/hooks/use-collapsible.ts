import * as React from "react";

// useCollapsible —— 折叠容器的高度过渡伙伴。CSS 侧 grid-template-rows 的
// 0fr/1fr 过渡只有在内容保持挂载时才双向动画:展开时内容挂载、行高从 0fr 长到
// 1fr;收缩时若内容立即卸载,行高失去内容支撑,收缩动画瞬间归零(用户看到的
// 「展开有动画、收缩没动画」)。这个 hook 把「卸载」推迟到收缩过渡结束之后:
//   - open 变 true:在 paint 前同步挂载(useLayoutEffect),展开动画从
//     「内容已挂载 + 0fr」起步;
//   - open 变 false:mounted 先保持 true,让内容陪着行高一起收到 0,等 grid
//     过渡结束(transitionend)再卸载;transitionend 在 prefers-reduced-motion
//     (transition:none)下不会触发,所以再叠一个 durationMs 兜底超时,保证
//     折叠态最终还是零挂载。
export function useCollapsible(open: boolean, durationMs = 200) {
  const [mounted, setMounted] = React.useState(open);

  // 展开时在 DOM 提交后、浏览器绘制前同步挂载。走 useEffect 会晚一帧:那一帧里
  // 行高已经是 1fr 但内容还没挂载,展开动画被吃掉(内容下一帧才弹出来)。
  React.useLayoutEffect(() => {
    if (open) setMounted(true);
  }, [open]);

  // 收缩过渡结束后的兜底卸载。transitionend 可能因 prefers-reduced-motion 或
  // 其它原因不触发;到点后强制卸载,避免折叠态残留重内容。open 重新变 true 时
  // effect 清理会取消这个定时器。
  React.useEffect(() => {
    if (!open) {
      const timer = window.setTimeout(
        () => setMounted(false),
        durationMs + 100,
      );
      return () => window.clearTimeout(timer);
    }
  }, [open, durationMs]);

  const onTransitionEnd = React.useCallback(
    (event: React.TransitionEvent<HTMLDivElement>) => {
      // 只认 grid 包装器自身的过渡:子元素的 transition(如 transition-colors)
      // 会冒泡上来,target !== currentTarget 一并排除。包装器只过渡
      // grid-template-rows,无需再比对 propertyName。
      if (!open && event.target === event.currentTarget) {
        setMounted(false);
      }
    },
    [open],
  );

  return { mounted, onTransitionEnd };
}
