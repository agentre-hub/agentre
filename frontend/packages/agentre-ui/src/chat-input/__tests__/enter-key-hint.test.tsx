import "@testing-library/jest-dom/vitest";
import { render, waitFor } from "@testing-library/react";
import { Editor } from "@tiptap/core";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AIChatInput } from "../index";

/**
 * 软键盘右下角那颗键写什么，由可编辑区的 `enterkeyhint` 属性决定。触屏宿主让回车
 * 只换行时，要把它标成「换行」而不是「发送」，否则键面上写着发送、按下去却换了行。
 */
describe("AIChatInput · enterKeyHint", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("Given the host passes enterKeyHint, When the editor mounts, Then the editable carries it", async () => {
    const editorRef = createRef<Editor | null>();
    render(
      <AIChatInput
        onSubmit={() => {}}
        editorRef={editorRef}
        enterKeyHint="enter"
      />,
    );
    await waitFor(() => expect(editorRef.current).not.toBeNull());

    expect(editorRef.current!.view.dom).toHaveAttribute(
      "enterkeyhint",
      "enter",
    );
  });

  it("Given no enterKeyHint, When the editor mounts, Then the editable has no such attribute (as before)", async () => {
    const editorRef = createRef<Editor | null>();
    render(<AIChatInput onSubmit={() => {}} editorRef={editorRef} />);
    await waitFor(() => expect(editorRef.current).not.toBeNull());

    expect(editorRef.current!.view.dom).not.toHaveAttribute("enterkeyhint");
  });

  it("Given an unchanged enterKeyHint, When the host re-renders, Then the editor options are not reset", async () => {
    const setOptions = vi.spyOn(Editor.prototype, "setOptions");
    const editorRef = createRef<Editor | null>();
    const { rerender } = render(
      <AIChatInput
        onSubmit={vi.fn()}
        editorRef={editorRef}
        enterKeyHint="enter"
      />,
    );
    await waitFor(() => expect(editorRef.current).not.toBeNull());
    const baseline = setOptions.mock.calls.length;

    for (let i = 0; i < 3; i++) {
      rerender(
        <AIChatInput
          onSubmit={vi.fn()}
          editorRef={editorRef}
          enterKeyHint="enter"
        />,
      );
    }

    expect(setOptions.mock.calls.length).toBe(baseline);
  });
});
