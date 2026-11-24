import type { KeyboardEvent } from "react";

/**
 * 工具窗里手写 tablist 的键盘模型，与共享 `SegmentedControl` 相同：←/→ 回绕移动、
 * Home/End 到两端，焦点与选中一起走；配合每个 tab 的 roving tabindex（只有当前
 * 项是 0），Tab 键一次就离开整组。这里不直接换成 `SegmentedControl`——它的基础
 * 类（h-7 / px-3 / text-sm / 悬停配色）与这里的外观不同，换了视觉就变了。
 *
 * 每个 tab 用 `data-value` 携带自己的取值。
 */
export function handleTablistKeyDown<T extends string>(
  event: KeyboardEvent<HTMLElement>,
  onSelect: (value: T) => void,
) {
  const tabs = Array.from(
    event.currentTarget.querySelectorAll<HTMLElement>(
      '[role="tab"]:not(:disabled)',
    ),
  );
  if (tabs.length === 0) return;
  const current = tabs.indexOf(document.activeElement as HTMLElement);
  let next: number;
  switch (event.key) {
    case "ArrowRight":
      next = current < 0 ? 0 : (current + 1) % tabs.length;
      break;
    case "ArrowLeft":
      next =
        current < 0
          ? tabs.length - 1
          : (current - 1 + tabs.length) % tabs.length;
      break;
    case "Home":
      next = 0;
      break;
    case "End":
      next = tabs.length - 1;
      break;
    default:
      return;
  }
  event.preventDefault();
  const target = tabs[next];
  target.focus();
  const value = target.dataset.value;
  if (value !== undefined) onSelect(value as T);
}
