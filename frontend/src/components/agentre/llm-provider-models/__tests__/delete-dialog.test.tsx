import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  DeleteLLMProvider: vi.fn(),
  DeleteLLMModel: vi.fn(),
  LLMProviderRefCounts: vi.fn(),
  LLMModelRefCounts: vi.fn(),
  SetLLMProviderEnabled: vi.fn(),
  SetLLMModelEnabled: vi.fn(),
}));

vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);

import { DeleteDialog } from "../delete-dialog";
import type { Model, Provider } from "../index";

const provider = { id: 7, name: "Anthropic", providerKey: "pk-7" } as Provider;
const model = { id: 3, modelKey: "mk-3", modelId: "claude-x" } as Model;

describe("DeleteDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    appMocks.DeleteLLMProvider.mockResolvedValue({});
    appMocks.DeleteLLMModel.mockResolvedValue({});
    appMocks.LLMProviderRefCounts.mockResolvedValue({
      counts: { backends: 1, sessions: 2, routes: 3 },
    });
    appMocks.LLMModelRefCounts.mockResolvedValue({
      counts: { backends: 1, sessions: 0, routes: 0 },
    });
  });

  it("Given a referenced provider, when the dialog settles, then it discloses the impact and the delete button is usable", async () => {
    render(
      <DeleteDialog
        target={{ kind: "provider", provider }}
        onClose={vi.fn()}
        onDeleted={vi.fn()}
      />,
    );

    await screen.findByText(/1 backends/);
    expect(screen.getByText(/2 sessions/)).toBeInTheDocument();
    expect(screen.getByText(/3 routes/)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Delete provider" }),
    ).toBeEnabled();
  });

  it("Given a referenced provider, when delete is confirmed, then it deletes with an explicit reference confirmation", async () => {
    const onDeleted = vi.fn();
    render(
      <DeleteDialog
        target={{ kind: "provider", provider }}
        onClose={vi.fn()}
        onDeleted={onDeleted}
      />,
    );

    const button = await screen.findByRole("button", {
      name: "Delete provider",
    });
    await waitFor(() => expect(button).toBeEnabled());
    button.click();

    await waitFor(() => expect(onDeleted).toHaveBeenCalledTimes(1));
    expect(appMocks.DeleteLLMProvider).toHaveBeenCalledWith(
      expect.objectContaining({ id: 7, confirmReference: true }),
    );
  });

  it("Given a referenced provider, when the dialog settles, then disabling stays available as the recoverable way out", async () => {
    render(
      <DeleteDialog
        target={{ kind: "provider", provider }}
        onClose={vi.fn()}
        onDeleted={vi.fn()}
        onDisabled={vi.fn()}
      />,
    );

    expect(
      await screen.findByRole("button", { name: "Disable instead" }),
    ).toBeEnabled();
  });

  it("Given reference counts that fail to load, when the dialog settles, then deletion is still offered and the gap is stated", async () => {
    appMocks.LLMModelRefCounts.mockRejectedValue(new Error("boom"));
    render(
      <DeleteDialog
        target={{ kind: "model", model }}
        onClose={vi.fn()}
        onDeleted={vi.fn()}
      />,
    );

    expect(
      await screen.findByText(/Reference impact could not be loaded/),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete model" })).toBeEnabled();
  });

  it("Given a referenced model, when delete is confirmed, then it deletes with an explicit reference confirmation", async () => {
    render(
      <DeleteDialog
        target={{ kind: "model", model }}
        onClose={vi.fn()}
        onDeleted={vi.fn()}
      />,
    );

    const button = await screen.findByRole("button", { name: "Delete model" });
    await waitFor(() => expect(button).toBeEnabled());
    button.click();

    await waitFor(() =>
      expect(appMocks.DeleteLLMModel).toHaveBeenCalledWith(
        expect.objectContaining({ id: 3, confirmReference: true }),
      ),
    );
  });
});
