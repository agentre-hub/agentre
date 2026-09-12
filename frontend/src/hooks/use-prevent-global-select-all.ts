// ⌘A / Ctrl+A 的让路逻辑:焦点在输入框里时交给输入框自己,否则才拦下来全选全文。
//
// DOM 侧的判据在 lib/text-selection.ts。

import { useEffect } from "react";

import { type DesktopPlatform } from "@/components/agentre";

import {
  getElementFromEventTarget,
  closestSelectableTextElement,
  isEditableSelectAllTarget,
  isSelectAllShortcut,
  getSelectedTextContainer,
  selectTextContainer,
} from "../lib/text-selection";

export function usePreventGlobalSelectAll(platform: DesktopPlatform) {
  useEffect(() => {
    if (typeof document === "undefined") {
      return;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (!isSelectAllShortcut(event, platform)) {
        return;
      }

      if (isEditableSelectAllTarget(event.target)) {
        return;
      }

      const targetSelectableText = closestSelectableTextElement(
        getElementFromEventTarget(event.target),
      );
      const selectedTextContainer = getSelectedTextContainer();
      const textContainer = targetSelectableText ?? selectedTextContainer;

      event.preventDefault();

      if (textContainer) {
        selectTextContainer(textContainer);
      }
    };

    document.addEventListener("keydown", handleKeyDown);

    return () => {
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [platform]);
}
