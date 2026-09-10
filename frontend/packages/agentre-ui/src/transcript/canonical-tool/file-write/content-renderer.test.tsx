import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

vi.mock("sonner", () => sonnerMocks);

import { FileWriteContent } from "./content-renderer";
import type { FileWriteDTO } from "../types";

// content-renderer 是 file.write 的**正文**渲染器，活动行的就地展开体直接用它
// （spec 决策 13）。这些用例原本挂在已删除的 FileWriteCard 上 —— 卡壳没了，
// 正文还活着，覆盖跟着正文走。
const originalClipboard = navigator.clipboard;

function mockClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  return writeText;
}

afterEach(() => {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: originalClipboard,
  });
  vi.clearAllMocks();
});

describe("FileWriteContent", () => {
  it("renders numbered content lines", () => {
    const write = {
      path: "/root/app/foo.ts",
      content: "hello\nworld",
      lines: 2,
      bytes: 11,
    } as unknown as FileWriteDTO;
    render(<FileWriteContent write={write} />);
    expect(screen.getByText("hello")).toBeDefined();
    expect(screen.getByText("world")).toBeDefined();
    expect(screen.getByText("2")).toBeDefined();
  });

  it("says so when the write has empty content", () => {
    const write = {
      path: "/x.ts",
      content: "",
      lines: 0,
      bytes: 0,
    } as unknown as FileWriteDTO;
    render(<FileWriteContent write={write} />);
    expect(screen.getByText("Created empty file")).toBeDefined();
  });

  // Given 一行超出容器宽度的内容，When 渲染，Then 内容区可横向滚动
  // 且行盒撑到内容宽度（容器本身 overflow-hidden，否则长行被裁掉且拖不出来）。
  it("scrolls long content lines horizontally instead of clipping them", () => {
    const longText = `const x = "${"y".repeat(400)}";`;
    const write = {
      path: "/root/app/foo.ts",
      content: `first\n${longText}`,
      lines: 2,
      bytes: longText.length + 6,
    } as unknown as FileWriteDTO;
    render(<FileWriteContent write={write} />);

    const scroller = screen.getByTestId("file-write-content-scroll");
    expect(scroller.className).toContain("overflow-x-auto");

    const row = screen.getByText(longText).parentElement as HTMLElement;
    expect(scroller).toContainElement(row);
    expect(row.className).toContain("w-max");
    expect(row.className).toContain("min-w-full");
  });

  it("Given a truncated write, When full content is copied, Then Sonner shows a timed success toast", async () => {
    const writeText = mockClipboard();
    const write = {
      path: "/x.ts",
      content: "hello\nworld",
      lines: 2,
      bytes: 11,
      truncated: true,
    } as unknown as FileWriteDTO;

    render(<FileWriteContent write={write} />);
    fireEvent.click(screen.getByRole("button", { name: "Copy Full Content" }));

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith("hello\nworld");
    });
    expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
      "Full content copied",
      expect.objectContaining({
        duration: 5000,
        position: "bottom-right",
      }),
    );
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });
});
