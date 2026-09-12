// 文本选区的 DOM 助手:命中可选中元素、判断全选快捷键该不该让路。
//
// 全选拦截那条路的组件级封装在 use-prevent-global-select-all(它只是这些助手的
// 一层事件绑定)。

import { isPrimaryShortcut, type DesktopPlatform } from "@/components/agentre";

export const selectableTextSelector = "[data-selectable-text='true']";

export function getElementFromEventTarget(target: EventTarget | null) {
  return target instanceof Element ? target : null;
}

export function getElementFromNode(node: Node | null) {
  if (!node) {
    return null;
  }

  return node instanceof Element ? node : node.parentElement;
}

export function closestSelectableTextElement(element: Element | null) {
  return element?.closest(selectableTextSelector) ?? null;
}

export function isEditableSelectAllTarget(target: EventTarget | null) {
  const element = getElementFromEventTarget(target);

  return Boolean(
    element?.closest(
      "input, textarea, select, [contenteditable='true'], [role='combobox']",
    ),
  );
}

export function isSelectAllShortcut(
  event: KeyboardEvent,
  platform: DesktopPlatform,
) {
  if (event.defaultPrevented || event.altKey || event.shiftKey) {
    return false;
  }

  if (event.key.toLowerCase() !== "a") {
    return false;
  }

  return isPrimaryShortcut(event, platform);
}

export function getSelectedTextContainer() {
  const selection = document.getSelection();

  if (!selection || selection.rangeCount === 0 || selection.isCollapsed) {
    return null;
  }

  return (
    closestSelectableTextElement(getElementFromNode(selection.anchorNode)) ??
    closestSelectableTextElement(getElementFromNode(selection.focusNode))
  );
}

export function selectTextContainer(element: Element) {
  const selection = document.getSelection();

  if (!selection) {
    return;
  }

  const range = document.createRange();
  range.selectNodeContents(element);
  selection.removeAllRanges();
  selection.addRange(range);
}
