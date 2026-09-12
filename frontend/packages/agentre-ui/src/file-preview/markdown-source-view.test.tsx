import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { MarkdownSourceView } from "./markdown-source-view";
import type { MonacoNS } from "./monaco";

function createFakeMonaco() {
  const editor = {
    setTheme: vi.fn(),
    create: vi.fn(
      (_container: HTMLElement, options: Record<string, unknown>) => ({
        options,
        setValue: vi.fn(),
        dispose: vi.fn(),
      }),
    ),
  };
  const monaco = { editor } as unknown as MonacoNS;
  return { monaco, editor };
}

describe("MarkdownSourceView", () => {
  it("renders raw markdown source through monaco with the markdown language", () => {
    const { monaco, editor } = createFakeMonaco();

    render(<MarkdownSourceView value={"# Title\n\nbody"} monaco={monaco} />);

    expect(editor.create).toHaveBeenCalledTimes(1);
    const [, options] = editor.create.mock.calls[0];
    expect(options).toMatchObject({
      readOnly: true,
      language: "markdown",
      value: "# Title\n\nbody",
    });
  });

  // 源码档锁 markdown：路径的扩展名（.mdx / .txt…）不许把语言抢回去。
  it("keeps the markdown language even when the path suggests another one", () => {
    const { monaco, editor } = createFakeMonaco();

    render(<MarkdownSourceView value="# t" path="notes.txt" monaco={monaco} />);

    expect(editor.create.mock.calls[0][1].language).toBe("markdown");
  });
});
