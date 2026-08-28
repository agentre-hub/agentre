/**
 * `!` 历史菜单打开时，把编辑器那个 contentEditable 变成一个 combobox。
 *
 * 属性直接写在 ProseMirror 的 DOM 上而不是包一层：读屏要的是「**这个**输入框
 * 展开了一个列表、当前落在第几项」，套一层 div 会让 activedescendant 指向的
 * 选项不再属于获得焦点的那个元素。菜单一关就整套摘掉——留着的 aria-expanded
 * 会让读屏一直报「已展开」。
 */
import { useLayoutEffect } from "react";
import type { Editor } from "@tiptap/react";

import { localCommandHistoryOptionId } from "./history-popover";

export interface UseLocalCommandHistoryComboboxArgs {
  editor: Editor | null | undefined;
  listboxId: string;
  open: boolean;
  clearFocused: boolean;
  selectedIndex: number;
}

export function useLocalCommandHistoryCombobox({
  editor,
  listboxId,
  open,
  clearFocused,
  selectedIndex,
}: UseLocalCommandHistoryComboboxArgs): void {
  useLayoutEffect(() => {
    if (!editor) return;
    const editorDom = editor.view.dom;
    const resetCombobox = () => {
      editorDom.setAttribute("role", "textbox");
      editorDom.removeAttribute("aria-expanded");
      editorDom.removeAttribute("aria-controls");
      editorDom.removeAttribute("aria-haspopup");
      editorDom.removeAttribute("aria-activedescendant");
    };

    if (!open) {
      resetCombobox();
      return;
    }

    editorDom.setAttribute("role", "combobox");
    editorDom.setAttribute("aria-expanded", "true");
    editorDom.setAttribute("aria-controls", listboxId);
    editorDom.setAttribute("aria-haspopup", "listbox");
    if (clearFocused) {
      editorDom.removeAttribute("aria-activedescendant");
    } else {
      editorDom.setAttribute(
        "aria-activedescendant",
        localCommandHistoryOptionId(listboxId, selectedIndex),
      );
    }

    return resetCombobox;
  }, [clearFocused, open, selectedIndex, editor, listboxId]);
}
