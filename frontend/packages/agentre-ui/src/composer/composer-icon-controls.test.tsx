import "@testing-library/jest-dom/vitest";
import { render, screen, waitFor, act } from "@testing-library/react";
import { createRef } from "react";
import type { Editor } from "@tiptap/react";
import { describe, expect, it, vi } from "vitest";

import { ChatComposer } from "./chat-composer";
import { ContextMeter } from "./context-meter";
import { ReasoningEffortPicker } from "./reasoning-effort-picker";
import { PermissionModePill } from "../permission-mode/permission-mode-pill";
import { PickerTrigger } from "../engine/model-target-picker/picker-trigger";
import {
  ProviderPillTrigger,
  type ProviderPillState,
} from "../engine/model-target-picker/provider-pill-trigger";

/**
 * 触屏卡片底栏「只留图标」档：手机上一行要放 ＋、权限、模型、上下文、思考力度、■、↑，
 * 文字一多模型那格就被挤到 6px。卡片底栏打上 `data-controls="icons"`，各控件按这个
 * 祖先标记把文字收进 sr-only、箭头隐藏、自身做成 32px 方格（::before 把点按区撑高到 44px）。
 * jsdom 没有布局，这里只能守住「标记在哪、谁认标记」这份类名契约。
 */
const ICONS = "in-data-[controls=icons]:";

function bar(container: HTMLElement): Element {
  return container.querySelector('[data-slot="composer-bar"]')!;
}

describe("composer icon-only controls (touch card)", () => {
  it("Given touch mode with a draft (card), Then the bar is marked icons-only; pill and desktop are not", async () => {
    const editorRef = createRef<Editor | null>();
    const { container } = render(
      <ChatComposer
        inputMode="touch"
        editorRef={editorRef}
        onSubmit={vi.fn()}
      />,
    );
    expect(bar(container)).not.toHaveAttribute("data-controls");
    act(() => {
      editorRef.current!.commands.insertContent("hi");
    });
    await waitFor(() =>
      expect(bar(container)).toHaveAttribute("data-controls", "icons"),
    );

    const desktop = render(<ChatComposer onSubmit={vi.fn()} />);
    expect(bar(desktop.container)).not.toHaveAttribute("data-controls");
  });

  it("ContextMeter: percent goes sr-only, the cell becomes a 32px square that does not clip its hit area", () => {
    render(<ContextMeter used={24} max={100} dataTestId="meter" />);
    const btn = screen.getByTestId("meter");
    expect(btn.className).toContain(`${ICONS}h-8`);
    expect(btn.className).toContain(`${ICONS}overflow-visible`);
    expect(btn.className).toContain(`${ICONS}before:-inset-y-1.5`);
    expect(screen.getByText("24%").className).toContain(`${ICONS}sr-only`);
    // 文字收起后读数仍在无障碍名里。
    expect(btn).toHaveAccessibleName(/24/);
  });

  it("ReasoningEffortPicker: chevron hidden, cell 32px", () => {
    render(
      <ReasoningEffortPicker
        value="high"
        backendValue=""
        onChange={vi.fn()}
        dataTestId="effort"
      />,
    );
    expect(screen.getByTestId("effort").className).toContain(`${ICONS}h-8`);
    expect(
      screen.getByTestId("effort-chevron").getAttribute("class"),
    ).toContain(`${ICONS}hidden`);
  });

  it("PermissionModePill: label sr-only, chevron hidden, cell 32px", () => {
    render(
      <PermissionModePill
        mode="default"
        modes={["default", "plan"]}
        onSelect={vi.fn()}
      />,
    );
    const btn = screen.getByRole("button");
    expect(btn.className).toContain(`${ICONS}h-8`);
    const svgs = btn.querySelectorAll("svg");
    expect(svgs[svgs.length - 1].getAttribute("class")).toContain(
      `${ICONS}hidden`,
    );
  });

  it("Model picker trigger + pill label: name sr-only, chevron hidden, cell 32px", () => {
    const state: ProviderPillState = {
      mode: "fixed",
      providerLabel: "Anthropic",
      providerType: "anthropic",
      modelLabel: "Sonnet 4.6",
      modelId: "claude-sonnet-4-6",
      resolutionLabel: "",
      dynamic: false,
      cliLogin: false,
    };
    render(
      <PickerTrigger
        open={false}
        disabled={false}
        invalid={false}
        compact={false}
        testId="model"
        ariaLabel="Model"
        selectedLabel=""
        triggerText={<ProviderPillTrigger state={state} />}
      />,
    );
    const btn = screen.getByTestId("model");
    expect(btn.className).toContain(`${ICONS}h-8`);
    const svgs = btn.querySelectorAll(":scope > svg");
    expect(svgs[svgs.length - 1].getAttribute("class")).toContain(
      `${ICONS}hidden`,
    );
    expect(screen.getByText(/Sonnet 4\.6/).className).toContain(
      `${ICONS}sr-only`,
    );
  });
});
