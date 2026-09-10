import type * as React from "react";

export function hasTextSelectionWithin(root: HTMLElement | null): boolean {
  if (
    !root ||
    typeof window === "undefined" ||
    typeof window.getSelection !== "function"
  ) {
    return false;
  }

  const selection = window.getSelection();
  if (!selection || selection.isCollapsed || selection.rangeCount === 0) {
    return false;
  }
  if (selection.toString().length === 0) return false;

  const isInside = (node: Node | null) =>
    !!node && (node === root || root.contains(node));

  if (isInside(selection.anchorNode) || isInside(selection.focusNode)) {
    return true;
  }

  for (let i = 0; i < selection.rangeCount; i++) {
    if (isInside(selection.getRangeAt(i).commonAncestorContainer)) {
      return true;
    }
  }

  return false;
}

export function shouldIgnoreClickForSelection<T extends HTMLElement>(
  event: React.MouseEvent<T>,
): boolean {
  if (!hasTextSelectionWithin(event.currentTarget)) return false;

  event.preventDefault();
  return true;
}
