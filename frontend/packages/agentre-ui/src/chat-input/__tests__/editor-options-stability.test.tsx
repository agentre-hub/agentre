import { act, render, waitFor } from "@testing-library/react";
import { Editor } from "@tiptap/core";
import { createRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AIChatInput } from "../index";

/**
 * 编辑器选项只在挂载时定一次；宿主的一次普通重渲染不许把整份选项重新灌给 TipTap。
 *
 * useEditor 每次渲染都会拿新选项与编辑器手里那份逐键比身份，不同就 `setOptions`
 * —— 那是 `view.setProps` 级别的重配置。选项若是每次渲染新建的对象 / 新 configure
 * 的扩展，流式输出期间宿主每重渲染一次，编辑器就被重配一次。
 */
describe("AIChatInput 的编辑器选项", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("Given 已挂载的输入框 When 宿主带着新的回调重渲染 Then 不重设编辑器选项，回车仍走最新的回调", async () => {
    const setOptions = vi.spyOn(Editor.prototype, "setOptions");
    const editorRef = createRef<Editor | null>();
    const first = vi.fn();
    const { rerender } = render(
      <AIChatInput onSubmit={first} editorRef={editorRef} />,
    );
    await waitFor(() => expect(editorRef.current).not.toBeNull());
    const baseline = setOptions.mock.calls.length;

    // 新的 onSubmit 身份越过 memo，强制 AIChatInput 真的重渲染五次。
    const latest = vi.fn();
    for (let i = 0; i < 4; i++) {
      rerender(<AIChatInput onSubmit={vi.fn()} editorRef={editorRef} />);
    }
    rerender(<AIChatInput onSubmit={latest} editorRef={editorRef} />);

    expect(setOptions.mock.calls.length).toBe(baseline);

    // 按键处理器不随选项重灌了，它仍得拿到活的编辑器：回车照样提交给最新的回调。
    const editor = editorRef.current!;
    act(() => {
      editor.commands.setContent("<p>hello</p>");
    });
    act(() => {
      editor.view.dom.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
      );
    });
    expect(latest).toHaveBeenCalledWith("hello");
    expect(first).not.toHaveBeenCalled();
  });

  it("Given 已挂载的输入框 When className 真的变了 Then 编辑器 DOM 换上新类名", async () => {
    const editorRef = createRef<Editor | null>();
    const { rerender } = render(
      <AIChatInput
        onSubmit={() => {}}
        editorRef={editorRef}
        className="first-class"
      />,
    );
    await waitFor(() => expect(editorRef.current).not.toBeNull());
    expect(editorRef.current!.view.dom.className).toContain("first-class");

    rerender(
      <AIChatInput
        onSubmit={() => {}}
        editorRef={editorRef}
        className="second-class"
      />,
    );

    await waitFor(() =>
      expect(editorRef.current!.view.dom.className).toContain("second-class"),
    );
    expect(editorRef.current!.view.dom.className).not.toContain("first-class");
  });
});
