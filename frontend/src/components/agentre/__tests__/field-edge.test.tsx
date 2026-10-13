import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { Input, SearchInput } from "@agentre-hub/agentre-ui";

import i18n from "@/i18n";

import { CapabilityPicker } from "../capability/capability-picker";
import { emptyDraft } from "../hooks-page-model";
import { HooksSidebar } from "../hooks-page-sidebar";
import { ScriptTab } from "../hooks-page/script-tab";
import { IconPicker } from "../icon-picker";

// 宿主里手写的字段静止时的边。
//
// 字段（输入框 / 文本域 / 下拉 / 搜索框）的边是单独一档（docs/design.md §3.4），
// 共享包的四个基础件由它自己的 ui/field-edge.test.tsx 把关。宿主里还有几处字段的边
// 不是基础件原样画出来的：自己拼的搜索框、自己写的下拉式触发器、给共享 Textarea
// 传了 className 换底色的调用点（底色可以换，边不许跟着覆写）。它们和共享的 Input
// 出现在同一张表单里，边深一档浅一档就是两套规则。
//
// 不断言类名叫什么：把共享的 Input 渲染在旁边，比较两者静止态的边色类。按类比而不是
// 像包内用例那样用 edgeTokenOf 反查 token：那个公共件在共享包不对外的 `__testing__`
// 里，宿主的测试只走包的公开出口。

/** 元素静止态的边色类：不带变体前缀的 `border-<色>`；宽度、线型、方向那几类不算。 */
function restingEdge(el: Element | null): string[] {
  return Array.from(el?.classList ?? []).filter(
    (c) =>
      /^border-[a-z]+(?:-[a-z]+)*$/.test(c) &&
      !/^border-(?:dashed|dotted|solid|none|[trblxyse])$/.test(c),
  );
}

function sharedInputEdge(): string[] {
  render(<Input aria-label="reference field" />);
  const edge = restingEdge(screen.getByLabelText("reference field"));
  expect(edge, "the shared Input has no resting edge class").toHaveLength(1);
  return edge;
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
  expect(focus, "the shared SearchInput draws no focus").not.toHaveLength(0);
  return focus;
}

describe("hand-written fields in the desktop host", () => {
  it("Given the Hooks sidebar's hand-assembled search box, When it rests, Then its edge is the shared Input's", () => {
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
    expect(restingEdge(frame)).toEqual(sharedInputEdge());
  });

  it("Given the capability picker's hand-assembled search box, When it rests, Then its edge is the shared Input's", () => {
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
    expect(restingEdge(frame)).toEqual(sharedInputEdge());
  });

  // 字段静止态的边低于 3:1，靠的是「聚焦时换成实色环」（docs/design.md §3.4）。
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

  it("Given the icon picker's dropdown trigger, When it rests, Then its edge is the shared Input's", () => {
    render(
      <IconPicker
        value="hammer"
        onChange={vi.fn()}
        accentColor="agent-1"
        ariaLabel="Icon"
      />,
    );

    const trigger = screen.getByRole("button", { name: "Icon" });
    expect(restingEdge(trigger)).toEqual(sharedInputEdge());
  });

  it("Given the Hooks script editor below the Name input, When it rests, Then its edge is the shared Input's", () => {
    render(
      <ScriptTab
        draft={emptyDraft(i18n.t)}
        onChange={vi.fn()}
        interpreters={[]}
        t={i18n.t}
      />,
    );

    const editor = screen.getByLabelText("Script");
    expect(restingEdge(editor)).toEqual(sharedInputEdge());
  });
});
