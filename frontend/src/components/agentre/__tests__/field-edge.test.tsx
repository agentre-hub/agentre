import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Input, SearchInput } from "@agentre-hub/agentre-ui";

import i18n from "@/i18n";

import { CapabilityPicker } from "../capability/capability-picker";
import { emptyDraft } from "../hooks-page-model";
import { HooksSidebar } from "../hooks-page-sidebar";
import { ScriptTab } from "../hooks-page/script-tab";
import { IconPicker } from "../icon-picker";

// 宿主里手写的字段：无边、同一个字段填充。
//
// 共享包的四个基础件由它自己的 ui/field-edge.test.tsx 把关。宿主里还有几处字段不是
// 基础件原样画出来的：自己拼的搜索框、自己写的下拉式触发器、给共享 Textarea 传了
// className 的调用点。它们和共享的 Input 出现在同一张表单里，所以也是静止无边、
// 用字段填充；外壳包着输入框的，聚焦时底色回到内容页色并出焦点环。
//
// 不断言 token 的色值（tokens.test.ts 管）：只断言类名族，且拿共享的 Input 当参照。

function restingEdges(el: Element | null): string[] {
  return Array.from(el?.classList ?? []).filter((c) =>
    /^border(?:-|$)/.test(c),
  );
}

function fillOf(el: Element | null): string[] {
  return Array.from(el?.classList ?? [])
    .filter((c) => /(?:^|:)bg-field-fill(?:-hover)?$/.test(c))
    .sort();
}

function sharedInputFill(): string[] {
  render(<Input aria-label="reference field" />);
  const fill = fillOf(screen.getByLabelText("reference field"));
  expect(fill, "the shared Input has no field fill").toHaveLength(2);
  return fill;
}

/** 外壳在「里面的输入框拿到焦点」时换上的类。 */
function focusWithin(el: Element | null): string[] {
  return Array.from(el?.classList ?? [])
    .filter((c) => c.startsWith("focus-within:"))
    .sort();
}

function sharedSearchFocus(): string[] {
  render(<SearchInput value="" onChange={vi.fn()} aria-label="reference" />);
  const focus = focusWithin(
    screen.getByRole("searchbox", { name: "reference" }).parentElement,
  );
  expect(focus, "the shared SearchInput draws no focus").toEqual([
    "focus-within:bg-sheet",
    "focus-within:focus-outline",
  ]);
  return focus;
}

describe("hand-written fields in the desktop host", () => {
  it("Given the Hooks sidebar's hand-assembled search box, When it rests, Then it has the shared Input's fill and no edge", () => {
    render(
      <HooksSidebar
        hooks={[]}
        filtered={[]}
        query=""
        selectedId={null}
        onQueryChange={vi.fn()}
        onSelect={vi.fn()}
        onCreate={vi.fn()}
        t={i18n.t}
      />,
    );

    const frame = screen.getByLabelText("Search Hooks").parentElement;
    expect(restingEdges(frame)).toEqual([]);
    expect(fillOf(frame)).toEqual(sharedInputFill());
  });

  it("Given the capability picker's hand-assembled search box, When it rests, Then it has the shared Input's fill and no edge", () => {
    render(
      <CapabilityPicker
        open
        title="Skills"
        searchPlaceholder="Search skills"
        items={[]}
        onToggle={vi.fn()}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    const frame = screen.getByLabelText("Search skills").parentElement;
    expect(restingEdges(frame)).toEqual([]);
    expect(fillOf(frame)).toEqual(sharedInputFill());
  });

  // 这两处的 <input> 自己写了 outline-none，外壳不接手的话键盘焦点就完全没有画面。
  it("Given the Hooks sidebar's hand-assembled search box, When its input takes focus, Then the frame draws the shared search box's focus", () => {
    render(
      <HooksSidebar
        hooks={[]}
        filtered={[]}
        query=""
        selectedId={null}
        onQueryChange={vi.fn()}
        onSelect={vi.fn()}
        onCreate={vi.fn()}
        t={i18n.t}
      />,
    );

    const frame = screen.getByLabelText("Search Hooks").parentElement;
    expect(focusWithin(frame)).toEqual(sharedSearchFocus());
  });

  it("Given the capability picker's hand-assembled search box, When its input takes focus, Then the frame draws the shared search box's focus", () => {
    render(
      <CapabilityPicker
        open
        title="Skills"
        searchPlaceholder="Search skills"
        items={[]}
        onToggle={vi.fn()}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    const frame = screen.getByLabelText("Search skills").parentElement;
    expect(focusWithin(frame)).toEqual(sharedSearchFocus());
  });

  it("Given the icon picker's dropdown trigger, When it rests, Then it has the shared Input's fill and no edge", () => {
    render(
      <IconPicker
        value="hammer"
        onChange={vi.fn()}
        accentColor="agent-1"
        ariaLabel="Icon"
      />,
    );

    const trigger = screen.getByRole("button", { name: "Icon" });
    expect(restingEdges(trigger)).toEqual([]);
    expect(fillOf(trigger)).toEqual(sharedInputFill());
  });

  it("Given the Hooks script editor below the Name input, When it rests, Then it has the shared Input's fill and no edge", () => {
    render(
      <ScriptTab
        draft={emptyDraft(i18n.t)}
        onChange={vi.fn()}
        interpreters={[]}
        t={i18n.t}
      />,
    );

    const editor = screen.getByLabelText("Script");
    expect(restingEdges(editor)).toEqual([]);
    expect(fillOf(editor)).toEqual(sharedInputFill());
  });
});
