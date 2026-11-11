import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { MemoryEditForm } from "./memory-edit-form";

function renderForm(
  overrides: Partial<React.ComponentProps<typeof MemoryEditForm>> = {},
) {
  const props: React.ComponentProps<typeof MemoryEditForm> = {
    summary: "Run lint before pushing",
    detail: "",
    onSave: vi.fn(async () => ({ ok: true as const })),
    onCancel: vi.fn(),
    ...overrides,
  };
  const view = render(<MemoryEditForm {...props} />);
  return { ...view, props };
}

describe("MemoryEditForm", () => {
  it("Given an entry, When the user edits both fields and saves, Then onSave gets the typed summary and detail", async () => {
    const { props } = renderForm();
    const summary = screen.getByRole("textbox", { name: "Summary" });
    const detail = screen.getByRole("textbox", { name: "Detail" });

    await userEvent.clear(summary);
    await userEvent.type(summary, "Run lint and tests before pushing");
    await userEvent.type(detail, "CI does not run lint.");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(props.onSave).toHaveBeenCalledWith({
      summary: "Run lint and tests before pushing",
      detail: "CI does not run lint.",
    });
  });

  it("Given the save fails, When the result comes back, Then both inputs keep what was typed and the reason is shown in place", async () => {
    const onSave = vi.fn(async () => ({
      ok: false as const,
      reason: "A memory summary is at most 200 characters",
    }));
    renderForm({ onSave });
    const summary = screen.getByRole("textbox", { name: "Summary" });
    const detail = screen.getByRole("textbox", { name: "Detail" });

    await userEvent.clear(summary);
    await userEvent.type(summary, "edited summary");
    await userEvent.type(detail, "edited detail");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Not saved");
    expect(alert).toHaveTextContent(
      "A memory summary is at most 200 characters",
    );
    expect(summary).toHaveValue("edited summary");
    expect(detail).toHaveValue("edited detail");
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("Given the save fails without a reason, Then a generic sentence is shown instead of nothing", async () => {
    renderForm({ onSave: vi.fn(async () => ({ ok: false as const })) });
    await userEvent.type(screen.getByRole("textbox", { name: "Summary" }), "!");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Not saved. Your text is still here; try again.",
    );
  });

  it("Given a blank summary, Then Save is disabled", async () => {
    renderForm();
    await userEvent.clear(screen.getByRole("textbox", { name: "Summary" }));
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("When the user presses Enter in the summary, Then it saves; Escape cancels", async () => {
    const { props } = renderForm();
    const summary = screen.getByRole("textbox", { name: "Summary" });
    await userEvent.type(summary, "!{Enter}");
    await waitFor(() =>
      expect(props.onSave).toHaveBeenCalledWith({
        summary: "Run lint before pushing!",
        detail: "",
      }),
    );
    await userEvent.type(summary, "{Escape}");
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it("Given the entry changes underneath (another device synced), When the form re-renders, Then the draft being typed is not overwritten", async () => {
    const { rerender, props } = renderForm();
    const summary = screen.getByRole("textbox", { name: "Summary" });
    await userEvent.clear(summary);
    await userEvent.type(summary, "my local edit");

    rerender(
      <MemoryEditForm
        {...props}
        summary="changed on another device"
        detail="remote detail"
      />,
    );

    expect(summary).toHaveValue("my local edit");
    expect(screen.getByRole("textbox", { name: "Detail" })).toHaveValue("");
  });
});
