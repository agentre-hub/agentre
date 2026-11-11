import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ModelTargetPicker } from "../../index";

// A2（spec 2026-11-10「输入框药丸切换失败」）：切换失败不顶替供应商列表，失败句在
// 列表下方一行、destructive 色；只有目录拉取失败才顶替列表，同样 destructive 色。
const CATALOG = [
  {
    providerKey: "k-anthropic",
    id: 1,
    name: "Anthropic",
    type: "anthropic",
    enabled: true,
    defaultModel: {
      modelKey: "mk-1",
      modelId: "claude-sonnet-4-6",
      name: "Sonnet",
      enabled: true,
    },
    models: [
      {
        modelKey: "mk-1",
        modelId: "claude-sonnet-4-6",
        name: "Sonnet",
        enabled: true,
      },
    ],
  },
];

async function openPicker(props: {
  switchError?: string;
  error?: boolean;
  errorText?: string;
}) {
  const user = userEvent.setup();
  render(
    <ModelTargetPicker
      scenario="chat"
      aria-label="LLM Provider"
      backendType="claudecode"
      selected={{ providerKey: "", modelKey: "" }}
      onChange={vi.fn()}
      catalog={CATALOG}
      {...props}
    />,
  );
  await user.click(screen.getByRole("button", { name: "LLM Provider" }));
  return screen.findByTestId("model-target-popover");
}

describe("ModelTargetPicker · switchError", () => {
  it("Given a switch failed, When the picker opens again, Then the provider list stays and the failure sentence sits below it in destructive colour", async () => {
    const popover = await openPicker({
      switchError: "Didn't switch — still Sonnet",
    });

    const line = within(popover).getByText("Didn't switch — still Sonnet");
    expect(line).toHaveClass("text-destructive-text");
    expect(line).toHaveAttribute("role", "alert");
    // 供应商列表仍在，没有被失败句顶替。
    const list = within(popover).getByRole("listbox");
    expect(within(list).getByText("Anthropic")).toBeInTheDocument();
    // 失败句排在列表之后。
    expect(
      list.compareDocumentPosition(line) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("Given no switchError, When the picker opens, Then no failure line is rendered", async () => {
    const popover = await openPicker({});
    expect(within(popover).queryByRole("alert")).toBeNull();
    expect(within(popover).getByText("Anthropic")).toBeInTheDocument();
  });

  it("Given the catalog failed to load, When the picker opens, Then the catalog error replaces the list in destructive colour", async () => {
    const popover = await openPicker({ error: true });
    const line = within(popover).getByText(/Failed to load model catalog/);
    expect(line).toHaveClass("text-destructive-text");
    expect(within(popover).queryByText("Anthropic")).toBeNull();
  });
});
