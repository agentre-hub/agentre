import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";

import { AIChatInput } from "../index";
import type { AIChatInputHandle } from "../types";

function pasteClipboard(flavors: Record<string, string>) {
  fireEvent.paste(screen.getByRole("textbox"), {
    clipboardData: {
      getData: (type: string) => flavors[type] ?? "",
      files: [],
      items: [],
    },
  });
}

async function pasteFlavorsAndSubmit(flavors: Record<string, string>) {
  const inputRef = createRef<AIChatInputHandle>();
  const onSubmit = vi.fn();
  render(<AIChatInput ref={inputRef} onSubmit={onSubmit} />);

  pasteClipboard(flavors);
  await waitFor(() => expect(inputRef.current?.isEmpty()).toBe(false));
  inputRef.current?.submit();

  return onSubmit;
}

function pasteAndSubmit(text: string) {
  return pasteFlavorsAndSubmit({ "text/plain": text });
}

describe("AIChatInput plain-text paste", () => {
  it("Given text containing a blank line, When it is pasted, Then both newlines are preserved", async () => {
    const text = "first paragraph\n\nsecond paragraph";

    const onSubmit = await pasteAndSubmit(text);

    expect(onSubmit).toHaveBeenCalledWith(text);
  });

  it("Given Windows line endings and consecutive blank lines, When they are pasted, Then each line ending is preserved as a newline", async () => {
    const onSubmit = await pasteAndSubmit("first\r\n\r\n\r\nsecond");

    expect(onSubmit).toHaveBeenCalledWith("first\n\n\nsecond");
  });
});

describe("AIChatInput rich clipboard paste", () => {
  it("Given code copied from an editor that also offers highlighted HTML, When it is pasted, Then the text arrives exactly as the plain flavor has it", async () => {
    const text = "def f():\n    return 1\n\nx = 2";

    const onSubmit = await pasteFlavorsAndSubmit({
      "text/plain": text,
      // VS Code 的形状:一行一个 div,空行是一个 <br>。
      "text/html":
        '<meta charset="utf-8"><div style="white-space: pre;"><div><span>def f():</span></div><div><span>    return 1</span></div><br><div><span>x = 2</span></div></div>',
    });

    expect(onSubmit).toHaveBeenCalledWith(text);
  });

  it("Given paragraphs copied from a web page, When they are pasted, Then the blank lines between them are kept", async () => {
    const text = "Title\n\nSome bold text.\n\none\ntwo";

    const onSubmit = await pasteFlavorsAndSubmit({
      "text/plain": text,
      "text/html":
        "<h2>Title</h2><p>Some <b>bold</b> text.</p><ul><li>one</li><li>two</li></ul>",
    });

    expect(onSubmit).toHaveBeenCalledWith(text);
  });

  it("Given a clipboard that only carries HTML, When it is pasted, Then its text is still inserted line by line", async () => {
    const onSubmit = await pasteFlavorsAndSubmit({
      "text/html": "<p>first</p><p>second</p>",
    });

    expect(onSubmit).toHaveBeenCalledWith("first\nsecond");
  });
});
