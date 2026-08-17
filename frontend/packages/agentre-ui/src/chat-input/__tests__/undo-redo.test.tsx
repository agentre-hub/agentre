import { act, render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { type RefObject } from "react";

import type { Editor } from "@tiptap/react";

import { AIChatInput } from "../index";

// 跟 ProseMirror-keymap 一致的平台判定：mac 上 `Mod` = Meta(⌘)，其余 = Ctrl。
// 直接照搬它的检测逻辑，避免 happy-dom 报告的 navigator.platform 与真实绑定不一致。
const isMac = /Mac|iP(hone|[oa]d)/.test(
  typeof navigator !== "undefined" ? navigator.platform : "",
);

/** 在编辑器的 contentEditable DOM 上派发一次 keydown，驱动 ProseMirror 的 keymap。 */
function pressShortcut(editor: Editor, key: string) {
  const event = new KeyboardEvent("keydown", {
    key,
    bubbles: true,
    cancelable: true,
    ...(isMac ? { metaKey: true } : { ctrlKey: true }),
  });
  editor.view.dom.dispatchEvent(event);
}

function Harness({ editorRef }: { editorRef: RefObject<Editor | null> }) {
  return <AIChatInput onSubmit={() => {}} editorRef={editorRef} autoFocus />;
}

describe("AIChatInput 撤销/重做快捷键", () => {
  it("Mod+Z 撤销最近一次输入", () => {
    const editorRef: RefObject<Editor | null> = { current: null };
    render(<Harness editorRef={editorRef} />);
    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("hello world");
    });
    expect(editor.getText()).toBe("hello world");

    act(() => {
      pressShortcut(editor, "z");
    });
    expect(editor.getText()).toBe("");
  });

  it("Mod+Y 重做被撤销的输入", () => {
    const editorRef: RefObject<Editor | null> = { current: null };
    render(<Harness editorRef={editorRef} />);
    const editor = editorRef.current!;

    act(() => {
      editor.commands.insertContent("redo me");
    });
    act(() => {
      pressShortcut(editor, "z");
    });
    expect(editor.getText()).toBe("");

    act(() => {
      pressShortcut(editor, "y");
    });
    expect(editor.getText()).toBe("redo me");
  });
});
