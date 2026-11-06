import "@testing-library/jest-dom/vitest";

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  PreviewImportData: vi.fn(),
  ApplyImportData: vi.fn(),
}));
vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);

const sonnerMocks = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

import { ImportSection } from "../import-section";

type ToastOptions = {
  description?: React.ReactNode;
  action?: { label: string; onClick: () => void };
};

function lastToast(): { title: string; options: ToastOptions } {
  const calls = sonnerMocks.toast.error.mock.calls;
  const [title, options] = calls[calls.length - 1];
  return { title, options: options ?? {} };
}

describe("ImportSection failures (S5)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("Given a file that is not a backup, When it is picked, Then the toast says so and offers to pick another file", async () => {
    appMocks.PreviewImportData.mockRejectedValueOnce(
      "agentre-code:20700 文件格式不识别,请选择 Agentre 导出的 JSON",
    );
    render(<ImportSection onResult={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /Choose File/ }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, options } = lastToast();
    expect(title).toBe("This file is not an Agentre backup");
    expect(options.action?.label).toBe("Choose another file");

    appMocks.PreviewImportData.mockResolvedValueOnce(null);
    options.action?.onClick();
    await waitFor(() =>
      expect(appMocks.PreviewImportData).toHaveBeenCalledTimes(2),
    );
  });

  it("Given an uncoded apply failure, When it is shown, Then the raw text is only inside collapsed details", async () => {
    appMocks.PreviewImportData.mockResolvedValueOnce({
      secretsIncluded: false,
      items: [],
    });
    appMocks.ApplyImportData.mockRejectedValueOnce(
      new Error("json: cannot unmarshal string into Go struct field"),
    );
    render(<ImportSection onResult={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /Choose File/ }));
    const apply = await screen.findByRole("button", { name: "Apply" });
    fireEvent.click(apply);

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, options } = lastToast();
    expect(title).toBe("Import failed");
    const { container } = render(<>{options.description}</>);
    const details = container.querySelector("details");
    expect(details).not.toBeNull();
    expect(details).not.toHaveAttribute("open");
    expect(details).toHaveTextContent("json: cannot unmarshal");
    expect(container.querySelector("p")).toBeNull();
  });
});
