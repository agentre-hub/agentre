import { render } from "@testing-library/react";
import * as React from "react";
import { describe, expect, it, vi } from "vitest";

/**
 * 外壳自己重渲染（宿主重渲染、空/非空切换、附件变化……）时，递给 AIChatInput 的
 * props 必须逐个保持身份 —— AIChatInput 是 memo 的，只要有一个内联回调，memo 就
 * 永远失效，编辑器跟着外壳每次都重渲染。
 */
const inputRenders = vi.hoisted(() => [] as Array<Record<string, unknown>>);

vi.mock("../chat-input", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../chat-input")>();
  const React = await import("react");
  return {
    ...actual,
    AIChatInput: React.forwardRef(function AIChatInputProbe(
      props: Record<string, unknown>,
      _ref: React.Ref<unknown>,
    ) {
      inputRenders.push(props);
      return null;
    }),
  };
});

import { ChatComposer } from "./chat-composer";

describe("ChatComposer 递给输入框的 props", () => {
  it("Given 宿主 props 不变 When 外壳重渲染 Then 输入框收到的每个 prop 身份都不变", () => {
    const onSubmit = vi.fn();
    const onCommandModeChange = vi.fn();
    const onCommandSubmit = vi.fn();
    const { rerender } = render(
      <ChatComposer
        onSubmit={onSubmit}
        onCommandModeChange={onCommandModeChange}
        onCommandSubmit={onCommandSubmit}
      />,
    );
    rerender(
      <ChatComposer
        onSubmit={onSubmit}
        onCommandModeChange={onCommandModeChange}
        onCommandSubmit={onCommandSubmit}
      />,
    );

    expect(inputRenders.length).toBeGreaterThanOrEqual(2);
    const prev = inputRenders[inputRenders.length - 2];
    const last = inputRenders[inputRenders.length - 1];
    const changed = Object.keys({ ...prev, ...last }).filter(
      (key) => !Object.is(prev[key], last[key]),
    );
    expect(changed).toEqual([]);
  });

  it("Given 进出命令模式 When 输入框上报 Then 宿主仍收到最新的 onCommandModeChange", () => {
    const stale = vi.fn();
    const latest = vi.fn();
    const { rerender } = render(
      <ChatComposer onSubmit={vi.fn()} onCommandModeChange={stale} />,
    );
    rerender(<ChatComposer onSubmit={vi.fn()} onCommandModeChange={latest} />);

    const last = inputRenders[inputRenders.length - 1];
    React.act(() => {
      (last.onCommandModeChange as (active: boolean) => void)(true);
    });
    expect(latest).toHaveBeenCalledWith(true);
    expect(stale).not.toHaveBeenCalled();
  });
});
