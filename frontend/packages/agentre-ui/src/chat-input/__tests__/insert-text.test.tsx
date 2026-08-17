import { act, render } from "@testing-library/react";
import { createRef, type RefObject } from "react";
import { describe, expect, it } from "vitest";

import type { Editor } from "@tiptap/react";

import { AIChatInput } from "../index";
import type { AIChatInputHandle } from "../types";

describe("AIChatInput.insertText", () => {
  it("把文本插入编辑器当前位置", () => {
    const handleRef = createRef<AIChatInputHandle>();
    const editorRef: RefObject<Editor | null> = { current: null };
    render(
      <AIChatInput
        ref={handleRef}
        editorRef={editorRef}
        onSubmit={() => {}}
        autoFocus
      />,
    );

    act(() => {
      handleRef.current!.insertText(`/Users/a/b.txt `);
    });
    expect(editorRef.current!.getText()).toBe("/Users/a/b.txt ");
  });
});
