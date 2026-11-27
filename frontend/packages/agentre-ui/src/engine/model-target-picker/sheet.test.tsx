import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ModelTargetPicker } from "../../index";

const catalog = [
  {
    providerKey: "k-anthropic",
    id: 1,
    name: "Anthropic",
    type: "anthropic",
    enabled: true,
    defaultModel: {
      modelKey: "mk-1",
      modelId: "claude-sonnet-4-6",
      enabled: true,
    },
    models: [{ modelKey: "mk-1", modelId: "claude-sonnet-4-6", enabled: true }],
  },
];

function renderPicker(presentation?: "sheet") {
  const onChange = vi.fn();
  render(
    <ModelTargetPicker
      scenario="chat"
      backendType="claudecode"
      selected={null}
      onChange={onChange}
      catalog={catalog}
      presentation={presentation}
      aria-label="model"
    />,
  );
  return { onChange };
}

describe("ModelTargetPicker presentation=sheet", () => {
  it("Given sheet, When opened, Then it is a bottom sheet dialog and the search box is not focused", async () => {
    const user = userEvent.setup();
    renderPicker("sheet");
    await user.click(screen.getByRole("button", { name: "model" }));
    const sheet = await screen.findByTestId("model-target-sheet");
    expect(sheet).toHaveAttribute("role", "dialog");
    const search = screen.getByPlaceholderText("Search providers or models…");
    await new Promise((r) => setTimeout(r, 20));
    expect(search).not.toHaveFocus();
    expect(screen.queryByTestId("model-target-popover")).toBeNull();
  });

  it("Given sheet, When an option is picked, Then onChange fires and the sheet closes", async () => {
    const user = userEvent.setup();
    const { onChange } = renderPicker("sheet");
    await user.click(screen.getByRole("button", { name: "model" }));
    await user.click(
      await screen.findByRole("option", {
        name: /Follow this provider's default/,
      }),
    );
    expect(onChange).toHaveBeenCalled();
    await waitFor(() =>
      expect(screen.queryByTestId("model-target-sheet")).toBeNull(),
    );
  });

  it("Given no presentation, When opened, Then desktop popover keeps focusing search", async () => {
    const user = userEvent.setup();
    renderPicker();
    await user.click(screen.getByRole("button", { name: "model" }));
    const search = await screen.findByPlaceholderText(
      "Search providers or models…",
    );
    await waitFor(() => expect(search).toHaveFocus());
    expect(screen.getByTestId("model-target-popover")).toBeInTheDocument();
  });
});
