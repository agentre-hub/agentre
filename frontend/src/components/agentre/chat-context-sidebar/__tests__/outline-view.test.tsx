import "@testing-library/jest-dom/vitest";

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { OutlineView } from "../views/outline-view";

import type { OutlineItem } from "../derive";

const items: OutlineItem[] = [
  {
    messageId: 11,
    turn: 1,
    text: "do thing one",
    time: 1700000000000,
    edits: 2,
    err: false,
  },
  {
    messageId: 22,
    turn: 2,
    text: "do thing two",
    time: 1700000060000,
    edits: 0,
    err: true,
  },
];

describe("OutlineView", () => {
  it("renders each outline item", () => {
    render(
      <OutlineView items={items} activeMessageId={null} onSelect={() => {}} />,
    );
    expect(screen.getByText("do thing one")).toBeInTheDocument();
    expect(screen.getByText("do thing two")).toBeInTheDocument();
  });

  it("Given an item with edits, When rendered, Then the time row ends with ✎ N instead of a separate boxed chip row", () => {
    render(
      <OutlineView items={items} activeMessageId={null} onSelect={() => {}} />,
    );
    const row = screen
      .getByText("do thing one")
      .closest("[data-outline-message-id]")!;
    const timeRow = within(row as HTMLElement).getByTestId("outline-time-row");
    const edits = within(timeRow).getByTestId("outline-edits");
    expect(edits).toHaveTextContent(/^2\s*edits$/);
    expect(edits.querySelector("svg")).not.toBeNull();
    // 「N edits」不再单独占一行带框的小块：条目里只剩时间行与正文两行。
    expect(row.children).toHaveLength(2);
    const edged = Array.from(row.querySelectorAll("*")).filter((el) =>
      Array.from(el.classList).some((c) => /^border(-[tblrxy])?$/.test(c)),
    );
    expect(edged).toEqual([]);
  });

  it("Given an item without edits, When rendered, Then the time row carries no edit count", () => {
    render(
      <OutlineView items={items} activeMessageId={null} onSelect={() => {}} />,
    );
    const row = screen
      .getByText("do thing two")
      .closest("[data-outline-message-id]")!;
    expect(
      within(row as HTMLElement).queryByTestId("outline-edits"),
    ).toBeNull();
    // 错误标记保留。
    expect(within(row as HTMLElement).getByText(/error/i)).toBeInTheDocument();
  });

  it("highlights the active row with the raised surface and no brand-coloured left rule", () => {
    render(
      <OutlineView items={items} activeMessageId={22} onSelect={() => {}} />,
    );
    const active = screen.getByText("do thing two").closest("[data-active]")!;
    expect(active.getAttribute("data-active")).toBe("true");
    expect(active).toHaveClass("bg-raised");
    expect(active.className).not.toMatch(/border-l|bg-primary/);
    const idle = screen.getByText("do thing one").closest("[data-active]")!;
    expect(idle).not.toHaveClass("bg-raised");
    expect(idle.className).not.toMatch(/border-l/);
  });

  it("calls onSelect with messageId when row clicked", async () => {
    const onSelect = vi.fn();
    render(
      <OutlineView items={items} activeMessageId={null} onSelect={onSelect} />,
    );
    await userEvent.click(screen.getByText("do thing one"));
    expect(onSelect).toHaveBeenCalledWith(11);
  });

  it("breaks an unbreakable long message so the clamp keeps two readable lines", () => {
    // 侧栏定宽,行文本没有 overflow-wrap 时,一条没有空格的长路径/长 URL 会撑破行盒,
    // 被 line-clamp 的 overflow:hidden 从中间裁掉 —— 既不折行也不出省略号,
    // 用户看到的是被砍断的半截词。jsdom 不做布局,这里守类名。
    const long =
      "internal/controller/relay_ctr/TestRelayClient_GivenAReservedChannelID_ThenTheClientCannotConnect";
    render(
      <OutlineView
        items={[
          {
            messageId: 33,
            turn: 3,
            text: long,
            time: 1700000120000,
            edits: 0,
            err: false,
          },
        ]}
        activeMessageId={null}
        onSelect={() => {}}
      />,
    );
    expect(screen.getByText(long).className).toContain("break-words");
  });

  it("renders empty state when items is empty", () => {
    render(
      <OutlineView items={[]} activeMessageId={null} onSelect={() => {}} />,
    );
    expect(screen.getByText(/No messages in this session/)).toBeInTheDocument();
  });
});
