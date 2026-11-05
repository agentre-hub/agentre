import "@testing-library/jest-dom/vitest";

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  ExportData: vi.fn().mockResolvedValue({
    path: "/tmp/x.json",
    canceled: false,
    summary: { "llm-providers": 1 },
  }),
}));

vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);

const sonnerMocks = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("sonner", () => sonnerMocks);

import { ExportSection } from "../export-section";

describe("ExportSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("默认全选 4 个 scope", () => {
    render(<ExportSection />);
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes).toHaveLength(4);
    boxes.forEach((b) => expect(b).toBeChecked());
  });

  it("勾选包含凭证后显示警告", () => {
    render(<ExportSection />);
    const sw = screen.getByRole("switch");
    fireEvent.click(sw);
    expect(screen.getByText(/plain-text secrets/)).toBeInTheDocument();
  });
});

describe("ExportSection failures (S5)", () => {
  it("Given a coded export failure, When it fails, Then the backend sentence is shown and the raw text is not the body", async () => {
    appMocks.ExportData.mockRejectedValueOnce(
      "agentre-code:20702 文件含未知的导入范围",
    );
    render(<ExportSection />);
    fireEvent.click(screen.getByRole("button", { name: /Export/ }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const [title, options] = sonnerMocks.toast.error.mock.calls[0];
    expect(title).toBe("Export failed");
    expect(options.description).not.toBe(
      "agentre-code:20702 文件含未知的导入范围",
    );
    const { container } = render(<>{options.description}</>);
    expect(container).toHaveTextContent("文件含未知的导入范围");
    expect(container).not.toHaveTextContent("agentre-code");
    expect(options.action.label).toBe("Retry");
  });
});
