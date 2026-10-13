import { act, fireEvent, render, screen } from "@testing-library/react";
import { createRef, type RefObject } from "react";
import { describe, expect, it, vi } from "vitest";

import type { Editor } from "@tiptap/react";

import { AIChatInput } from "../index";
import type { AIChatInputHandle } from "../types";

function renderComposer() {
  const inputRef = createRef<AIChatInputHandle>();
  const editorRef: RefObject<Editor | null> = { current: null };
  const onSubmit = vi.fn();
  render(
    <AIChatInput ref={inputRef} editorRef={editorRef} onSubmit={onSubmit} />,
  );
  return { input: inputRef.current!, editor: editorRef.current!, onSubmit };
}

/** 在编辑器上派发一次 copy / cut，收下 ProseMirror 写进剪贴板的各种 flavor。 */
function captureClipboard(type: "copy" | "cut" = "copy") {
  const data = new Map<string, string>();
  fireEvent[type](screen.getByRole("textbox"), {
    clipboardData: {
      clearData: () => data.clear(),
      setData: (flavor: string, value: string) => data.set(flavor, value),
    },
  });
  return data;
}

function selectAllAndCopy(editor: Editor, type: "copy" | "cut" = "copy") {
  act(() => {
    editor.commands.selectAll();
  });
  return captureClipboard(type);
}

describe("AIChatInput copy", () => {
  it("Given several lines including a blank one, When everything is copied, Then the plain text keeps exactly one newline per line break", () => {
    const { input, editor } = renderComposer();
    act(() => input.loadDraft("first\nsecond\n\nfourth"));

    const clipboard = selectAllAndCopy(editor);

    expect(clipboard.get("text/plain")).toBe("first\nsecond\n\nfourth");
  });

  it("Given lines pasted as plain text, When they are copied back out, Then the plain text equals what was pasted", () => {
    const { editor } = renderComposer();
    const text = "line one\nline two\nline three";
    fireEvent.paste(screen.getByRole("textbox"), {
      clipboardData: {
        getData: (flavor: string) => (flavor === "text/plain" ? text : ""),
        files: [],
        items: [],
      },
    });

    const clipboard = selectAllAndCopy(editor);

    expect(clipboard.get("text/plain")).toBe(text);
  });

  it("Given a message with @ mentions, When it is copied, Then each mention appears in the plain text as @label", () => {
    const { input, editor } = renderComposer();
    act(() =>
      input.loadDraft(
        'ask <agent id="3" color="blue">Bob</agent> about <project id="7" path="/work/app">app</project>\nand <device fp="ab12">studio</device>',
      ),
    );

    const clipboard = selectAllAndCopy(editor);

    expect(clipboard.get("text/plain")).toBe(
      "ask @Bob about @app\nand @studio",
    );
  });

  it("Given several lines, When everything is cut, Then the plain text matches a copy and the editor is emptied", () => {
    const { input, editor } = renderComposer();
    act(() => input.loadDraft("first\nsecond"));

    const clipboard = selectAllAndCopy(editor, "cut");

    expect(clipboard.get("text/plain")).toBe("first\nsecond");
    expect(input.isEmpty()).toBe(true);
  });

  it("Given a copied message with @ mentions, When it is pasted back into the composer, Then the mentions survive as structured references", () => {
    const { input, editor, onSubmit } = renderComposer();
    const message =
      'ask <agent id="3" color="blue">Bob</agent> about <project id="7" path="/work/app">app</project>\nthanks';
    act(() => input.loadDraft(message));
    const clipboard = selectAllAndCopy(editor);
    act(() => input.clear());

    fireEvent.paste(screen.getByRole("textbox"), {
      clipboardData: {
        getData: (flavor: string) => clipboard.get(flavor) ?? "",
        files: [],
        items: [],
      },
    });
    act(() => input.submit());

    expect(onSubmit).toHaveBeenCalledWith(message);
  });
});
