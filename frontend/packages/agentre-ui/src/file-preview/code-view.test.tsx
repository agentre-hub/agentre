import { act, render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { CodePreview } from "./code-view";
import type { MonacoNS } from "./monaco";

// 真实 Monaco 在 happy-dom 里不可运行（worker / canvas），而装载器按设计留在宿主
// （Vite `?worker` 进不了本包的纯 tsc 构建）。包里的唯一接缝因此是 `monaco` prop：
// 用例注入一个只实现被用到的那几个 API 的 fake 命名空间，断言组件把正确的选项
// 转发给 Monaco。
type FakeEditor = {
  options: Record<string, unknown>;
  setValue: ReturnType<typeof vi.fn>;
  dispose: ReturnType<typeof vi.fn>;
  getModel: ReturnType<typeof vi.fn>;
  revealLineInCenter: ReturnType<typeof vi.fn>;
  setSelection: ReturnType<typeof vi.fn>;
};

// 定位要问模型「这个文件有多少行」「这一行到第几列」，fake 因此带一个按当前正文
// 算出来的极简模型（行数 = 换行数 + 1，行尾列 = 该行长度 + 1，与 Monaco 的
// 1-based 列语义一致）。
function createFakeMonaco() {
  const editors: FakeEditor[] = [];
  const editor = {
    setTheme: vi.fn(),
    create: vi.fn(
      (
        _container: HTMLElement,
        options: Record<string, unknown>,
      ): FakeEditor => {
        let text = String(options.value ?? "");
        const model = {
          getLineCount: () => text.split("\n").length,
          getLineMaxColumn: (line: number) =>
            (text.split("\n")[line - 1]?.length ?? 0) + 1,
        };
        const e: FakeEditor = {
          options,
          setValue: vi.fn((next: string) => {
            text = next;
          }),
          dispose: vi.fn(),
          getModel: vi.fn(() => model),
          revealLineInCenter: vi.fn(),
          setSelection: vi.fn(),
        };
        editors.push(e);
        return e;
      },
    ),
  };
  const monaco = { editor } as unknown as MonacoNS;
  return { monaco, editor, editors };
}

/** 20 行文本，行内容各不相同，便于断言选区落在哪一行的哪一列。 */
const TWENTY_LINES = Array.from({ length: 20 }, (_, i) => `line ${i + 1}`).join(
  "\n",
);

describe("CodePreview", () => {
  it("creates a readOnly monaco editor with the language inferred from path", () => {
    const { monaco, editor } = createFakeMonaco();

    render(<CodePreview value="const a = 1;" path="a.ts" monaco={monaco} />);

    expect(editor.create).toHaveBeenCalledTimes(1);
    const [container, options] = editor.create.mock.calls[0];
    expect(container).toBeInstanceOf(HTMLElement);
    expect(options).toMatchObject({
      readOnly: true,
      language: "typescript",
      value: "const a = 1;",
    });
  });

  it("defaults to plaintext for unknown extensions and honors an explicit language", () => {
    const { monaco, editor } = createFakeMonaco();

    const { rerender } = render(
      <CodePreview value="x" path="a.weird" monaco={monaco} />,
    );
    expect(editor.create.mock.calls[0][1].language).toBe("plaintext");

    rerender(
      <CodePreview value="x" path="a.ts" language="json" monaco={monaco} />,
    );
    expect(editor.create).toHaveBeenCalledTimes(2);
    expect(editor.create.mock.calls[1][1].language).toBe("json");
  });

  it("updates value on the same editor instance instead of recreating it", () => {
    const { monaco, editor, editors } = createFakeMonaco();

    const { rerender } = render(
      <CodePreview value="v1" path="notes.txt" monaco={monaco} />,
    );
    rerender(<CodePreview value="v2" path="notes.txt" monaco={monaco} />);

    expect(editor.create).toHaveBeenCalledTimes(1);
    expect(editors[0].setValue).toHaveBeenCalledWith("v2");
  });

  it("disposes the editor on unmount", () => {
    const { monaco, editors } = createFakeMonaco();

    const { unmount } = render(
      <CodePreview value="x" path="a.go" monaco={monaco} />,
    );
    unmount();

    expect(editors[0].dispose).toHaveBeenCalledTimes(1);
  });

  // 宿主的装载是异步的（懒加载一个独立 chunk）：命名空间到位之前 prop 是 null，
  // 那一段时间里容器必须只是空的，不能崩、也不能自己去 import monaco。
  it("renders an empty container until the host injects a namespace", () => {
    const { monaco, editor } = createFakeMonaco();

    const { container, rerender } = render(
      <CodePreview value="x" path="a.go" monaco={null} className="h-full" />,
    );
    expect(editor.create).not.toHaveBeenCalled();
    expect(container.querySelector("div")).not.toBeNull();

    rerender(
      <CodePreview value="x" path="a.go" monaco={monaco} className="h-full" />,
    );
    expect(editor.create).toHaveBeenCalledTimes(1);
  });
});

describe("CodePreview reveal target", () => {
  it("Given a range, when the editor is created, then it centers the start line and selects through the end line", () => {
    const { monaco, editors } = createFakeMonaco();

    render(
      <CodePreview
        value={TWENTY_LINES}
        path="a.ts"
        monaco={monaco}
        revealTarget={{ line: 3, endLine: 5, nonce: 1 }}
      />,
    );

    expect(editors[0].revealLineInCenter).toHaveBeenCalledWith(3);
    expect(editors[0].setSelection).toHaveBeenCalledWith({
      startLineNumber: 3,
      startColumn: 1,
      endLineNumber: 5,
      endColumn: "line 5".length + 1,
    });
  });

  it("Given no reveal target, when the editor is created, then it neither scrolls nor selects", () => {
    const { monaco, editors } = createFakeMonaco();

    render(<CodePreview value={TWENTY_LINES} path="a.ts" monaco={monaco} />);

    expect(editors[0].revealLineInCenter).not.toHaveBeenCalled();
    expect(editors[0].setSelection).not.toHaveBeenCalled();
  });

  it("Given the content is re-read while the reveal target is unchanged, when it re-renders, then it does not yank the user back", () => {
    const { monaco, editors } = createFakeMonaco();
    const target = { line: 3, endLine: 5, nonce: 1 };

    const { rerender } = render(
      <CodePreview
        value={TWENTY_LINES}
        path="a.ts"
        monaco={monaco}
        revealTarget={target}
      />,
    );
    rerender(
      <CodePreview
        value={`${TWENTY_LINES}\nline 21`}
        path="a.ts"
        monaco={monaco}
        revealTarget={target}
      />,
    );

    expect(editors[0].revealLineInCenter).toHaveBeenCalledTimes(1);
  });

  it("Given the same link is clicked again, when only the nonce changes, then it reveals once more", () => {
    const { monaco, editors } = createFakeMonaco();

    const { rerender } = render(
      <CodePreview
        value={TWENTY_LINES}
        path="a.ts"
        monaco={monaco}
        revealTarget={{ line: 3, endLine: 5, nonce: 1 }}
      />,
    );
    rerender(
      <CodePreview
        value={TWENTY_LINES}
        path="a.ts"
        monaco={monaco}
        revealTarget={{ line: 3, endLine: 5, nonce: 2 }}
      />,
    );

    expect(editors[0].revealLineInCenter).toHaveBeenCalledTimes(2);
    expect(editors[0].revealLineInCenter).toHaveBeenLastCalledWith(3);
  });

  it("Given the content has not arrived yet, when it lands later, then the reveal happens on the real text instead of the empty model", () => {
    const { monaco, editors } = createFakeMonaco();
    const target = { line: 3, endLine: 5, nonce: 1 };

    const { rerender } = render(
      <CodePreview
        value=""
        path="a.ts"
        monaco={monaco}
        revealTarget={target}
      />,
    );
    expect(editors[0].revealLineInCenter).not.toHaveBeenCalled();

    rerender(
      <CodePreview
        value={TWENTY_LINES}
        path="a.ts"
        monaco={monaco}
        revealTarget={target}
      />,
    );
    expect(editors[0].revealLineInCenter).toHaveBeenCalledWith(3);
  });

  it("Given a range past the end of the file, when revealed, then it lands on the last line instead of failing", () => {
    const { monaco, editors } = createFakeMonaco();

    render(
      <CodePreview
        value={TWENTY_LINES}
        path="a.ts"
        monaco={monaco}
        revealTarget={{ line: 311, endLine: 330, nonce: 1 }}
      />,
    );

    expect(editors[0].revealLineInCenter).toHaveBeenCalledWith(20);
    expect(editors[0].setSelection).toHaveBeenCalledWith({
      startLineNumber: 20,
      startColumn: 1,
      endLineNumber: 20,
      endColumn: "line 20".length + 1,
    });
  });
});

describe("CodePreview theme following", () => {
  it("sets the monaco theme on mount and re-themes when the app flips .dark", () => {
    document.documentElement.classList.remove("dark");
    const { monaco, editor } = createFakeMonaco();
    const observe = vi.fn<MutationObserver["observe"]>();
    const disconnect = vi.fn<MutationObserver["disconnect"]>();

    class ControlledMutationObserver implements MutationObserver {
      constructor(readonly callback: MutationCallback) {
        mutationObservers.push(this);
      }

      observe = observe;
      disconnect = disconnect;
      takeRecords = vi.fn<MutationObserver["takeRecords"]>(() => []);
    }

    const mutationObservers: ControlledMutationObserver[] = [];

    vi.stubGlobal("MutationObserver", ControlledMutationObserver);
    try {
      const { unmount } = render(
        <CodePreview value="x" path="a.go" monaco={monaco} />,
      );

      expect(editor.create).toHaveBeenCalledTimes(1);
      expect(editor.setTheme).toHaveBeenLastCalledWith("vs");
      expect(observe).toHaveBeenCalledWith(document.documentElement, {
        attributes: true,
        attributeFilter: ["class"],
      });

      act(() => {
        document.documentElement.classList.add("dark");
        mutationObservers[0].callback([], mutationObservers[0]);
      });
      expect(editor.setTheme).toHaveBeenLastCalledWith("vs-dark");

      act(() => {
        document.documentElement.classList.remove("dark");
        mutationObservers[0].callback([], mutationObservers[0]);
      });
      expect(editor.setTheme).toHaveBeenLastCalledWith("vs");

      unmount();
      expect(disconnect).toHaveBeenCalledTimes(1);
    } finally {
      document.documentElement.classList.remove("dark");
      vi.unstubAllGlobals();
    }
  });
});
