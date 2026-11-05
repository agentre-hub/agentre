import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { lazyView } from "./lazy-boundary";

function Loaded() {
  return <div>loaded view</div>;
}

describe("lazyView", () => {
  it("renders the loaded component once the chunk resolves", async () => {
    const View = lazyView(() => Promise.resolve({ default: Loaded }));
    render(<View />);
    expect(await screen.findByText("loaded view")).toBeInTheDocument();
  });

  it("shows the error boundary when the chunk fails, and retry re-imports", async () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    const loader = vi
      .fn<() => Promise<{ default: React.ComponentType }>>()
      .mockRejectedValueOnce(new Error("chunk failed"))
      .mockResolvedValue({ default: Loaded });
    const View = lazyView(loader);
    render(<View />);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong");

    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("loaded view")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
    expect(loader).toHaveBeenCalledTimes(2);
    spy.mockRestore();
  });
});
