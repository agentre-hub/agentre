import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";

import { FileBlock } from "./hunk-renderer";
import type { FileEditPatch } from "../types";

// hunk-renderer 是 file.edit 的**正文**渲染器，活动行的就地展开体直接用它
// （spec 决策 13）。这里的用例原本挂在已删除的 FileEditCard 上 —— 卡壳没了，
// 但正文还活着，覆盖跟着正文走。
describe("FileBlock", () => {
  it("renders the file header with path and +/- counts", () => {
    const file = {
      path: "/root/app/x.ts",
      kind: "modified",
      hunks: [],
      plus: 3,
      minus: 1,
    } as unknown as FileEditPatch;
    render(<FileBlock file={file} showHeader />);
    expect(screen.getByText("/root/app/x.ts")).toBeDefined();
    expect(screen.getByText("+3")).toBeDefined();
    expect(screen.getByText("−1")).toBeDefined();
  });

  // Given 一行超出容器宽度的 diff，When 渲染，Then diff 区可横向滚动
  // 且行盒撑到内容宽度（否则 +/- 背景条在滚动后断在容器边缘）。
  it("scrolls long diff lines horizontally instead of clipping them", () => {
    const longText = `const x = "${"y".repeat(400)}";`;
    const file = {
      path: "/root/app/x.ts",
      kind: "modified",
      plus: 1,
      minus: 0,
      hunks: [
        {
          oldStart: 1,
          oldLines: 0,
          newStart: 1,
          newLines: 1,
          lines: [{ op: "+", new: 1, text: longText }],
        },
      ],
    } as unknown as FileEditPatch;
    render(<FileBlock file={file} showHeader={false} />);

    const scroller = screen.getByTestId("file-edit-diff-scroll");
    expect(scroller.className).toContain("overflow-x-auto");

    const row = screen.getByText(longText).parentElement as HTMLElement;
    expect(scroller).toContainElement(row);
    expect(row.className).toContain("w-max");
    expect(row.className).toContain("min-w-full");
  });

  it("says so when a file carries no hunks", () => {
    const file = {
      path: "/a.ts",
      kind: "modified",
      hunks: [],
      plus: 0,
      minus: 0,
    } as unknown as FileEditPatch;
    render(<FileBlock file={file} showHeader={false} />);
    expect(screen.getByText("No actual changes")).toBeDefined();
  });
});
