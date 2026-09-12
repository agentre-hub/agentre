import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";

import { AIChatInput } from "../index";
import type { AIChatInputHandle } from "../types";

function pastePlainText(text: string) {
  fireEvent.paste(screen.getByRole("textbox"), {
    clipboardData: {
      getData: (type: string) => (type === "text/plain" ? text : ""),
      files: [],
      items: [],
    },
  });
}

async function pasteAndSubmit(text: string) {
  const inputRef = createRef<AIChatInputHandle>();
  const onSubmit = vi.fn();
  render(<AIChatInput ref={inputRef} onSubmit={onSubmit} />);

  pastePlainText(text);
  await waitFor(() => expect(inputRef.current?.isEmpty()).toBe(false));
  inputRef.current?.submit();

  return onSubmit;
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
