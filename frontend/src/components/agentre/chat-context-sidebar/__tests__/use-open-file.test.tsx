import "@testing-library/jest-dom/vitest";

import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const openPathMock = vi.fn();
const revealPathMock = vi.fn();
vi.mock("@/../wailsjs/go/app/App", () => ({
  OpenPath: (p: string) => openPathMock(p),
  RevealPath: (p: string) => revealPathMock(p),
}));

import { useOpenFile, useRevealFile } from "../views/use-open-file";

describe("open / reveal failure toasts", () => {
  beforeEach(() => {
    sonnerMocks.toast.error.mockReset();
    openPathMock.mockReset();
    revealPathMock.mockReset();
  });

  it("opening a file that fails puts the localized fact in the title and the raw text in the description", async () => {
    openPathMock.mockRejectedValue(new Error("exec: no application"));
    const { result } = renderHook(() => useOpenFile("/work"));
    result.current("a.txt");
    await vi.waitFor(() =>
      expect(sonnerMocks.toast.error).toHaveBeenCalledTimes(1),
    );
    const [title, opts] = sonnerMocks.toast.error.mock.calls[0];
    expect(title).not.toContain("exec: no application");
    expect(opts).toEqual({ description: "exec: no application" });
  });

  it("strips the agentre-code prefix from the description", async () => {
    revealPathMock.mockRejectedValue("agentre-code:10002 Not found");
    const { result } = renderHook(() => useRevealFile("/work"));
    result.current("a.txt");
    await vi.waitFor(() =>
      expect(sonnerMocks.toast.error).toHaveBeenCalledTimes(1),
    );
    const [title, opts] = sonnerMocks.toast.error.mock.calls[0];
    expect(title).not.toContain("Not found");
    expect(title).not.toContain("agentre-code");
    expect(opts).toEqual({ description: "Not found" });
  });
});
