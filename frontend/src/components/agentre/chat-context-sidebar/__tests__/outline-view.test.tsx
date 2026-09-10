import "@testing-library/jest-dom/vitest";

import { render, screen } from "@testing-library/react";
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

  it("renders edits badge and error badge", () => {
    render(
      <OutlineView items={items} activeMessageId={null} onSelect={() => {}} />,
    );
    expect(screen.getByText(/2 edits/)).toBeInTheDocument();
    expect(screen.getByText(/error/i)).toBeInTheDocument();
  });

  it("highlights active row", () => {
    render(
      <OutlineView items={items} activeMessageId={22} onSelect={() => {}} />,
    );
    const active = screen.getByText("do thing two").closest("[data-active]")!;
    expect(active.getAttribute("data-active")).toBe("true");
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
