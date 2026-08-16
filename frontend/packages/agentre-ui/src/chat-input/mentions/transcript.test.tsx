import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { MarkdownText } from "../../transcript/markdown-text";
import {
  makeTestPorts,
  renderWithPorts as render,
} from "../../transcript/__testing__/ports";
import { makeMentionDecorator, prepareMentionText } from "./transcript";

describe("prepareMentionText", () => {
  it("replaces tags with sentinels and collects refs", () => {
    const { text, refs } = prepareMentionText(
      'hi <agent id="12">Reviewer</agent> and <project id="3" path="/w">Web</project>',
    );
    expect(text).toBe("hi 0 and 1");
    expect(refs).toEqual([
      { kind: "agent", refId: 12, label: "Reviewer" },
      { kind: "project", refId: 3, label: "Web", path: "/w" },
    ]);
  });

  it("leaves plain text untouched with empty refs", () => {
    expect(prepareMentionText("just text")).toEqual({
      text: "just text",
      refs: [],
    });
  });
});

describe("MarkdownText + mention decorator", () => {
  it("renders a chip for the mention sentinel", () => {
    const { text, refs } = prepareMentionText('yo <agent id="1">Bob</agent>');
    render(<MarkdownText text={text} decorator={makeMentionDecorator(refs)} />);
    expect(screen.getByText("@Bob")).toBeInTheDocument();
  });

  it("Given a mention and an allowed path in the same text, when the transcript renders them, then both own only their matching range", () => {
    const { text, refs } = prepareMentionText(
      'ask <agent id="1">Bob</agent> to inspect frontend/src/chat.tsx',
    );
    render(
      <MarkdownText
        text={text}
        cwd="/work/proj"
        decorator={makeMentionDecorator(refs)}
      />,
    );

    const mention = screen.getByText("@Bob");
    const link = screen.getByRole("link", { name: /frontend\/src\/chat\.tsx/ });
    expect(mention.closest("a")).toBeNull();
    expect(link.querySelector("button")).toBeNull();
    expect(document.body.textContent).toContain(
      "ask @Bob to inspect frontend/src/chat.tsx",
    );
  });

  it("Given a colored sent mention, When the transcript renders it, Then it keeps the mention background color", () => {
    const { text, refs } = prepareMentionText(
      '<agent id="1" color="agent-3">Bob</agent>',
    );
    expect(refs).toEqual([
      { kind: "agent", refId: 1, label: "Bob", color: "agent-3" },
    ]);
    render(<MarkdownText text={text} decorator={makeMentionDecorator(refs)} />);
    expect(
      screen.getByText("@Bob").style.getPropertyValue("--mention-color"),
    ).toBe("var(--agent-3)");
  });
});

// 跳转是外壳能力：包里没有 react-router，chip 点下去要去哪由宿主的 openMention
// 端口决定。端口是**可选**的（ports.ts 的能力探测语义），所以两条分支都要有用例 ——
// 缺端口时必须退成非交互文本，而不是渲染出一个点了没反应的按钮。
describe("MentionChip 的 openMention 端口", () => {
  function renderChip(ports: ReturnType<typeof makeTestPorts>) {
    const { text, refs } = prepareMentionText(
      '<agent id="7" color="agent-3">Bob</agent>',
    );
    render(
      <MarkdownText text={text} decorator={makeMentionDecorator(refs)} />,
      {
        ports,
      },
    );
  }

  it("Given a host that provides openMention, When the chip is clicked, Then the port receives the mention ref", () => {
    const openMention = vi.fn();
    renderChip(makeTestPorts({ openMention }));

    const chip = screen.getByRole("button", { name: "@Bob" });
    fireEvent.click(chip);

    expect(openMention).toHaveBeenCalledTimes(1);
    expect(openMention).toHaveBeenCalledWith({
      kind: "agent",
      refId: 7,
      label: "Bob",
      color: "agent-3",
    });
  });

  it("Given a host without openMention, When the chip renders, Then it stays non-interactive text but keeps the mention color", () => {
    renderChip(makeTestPorts({ openMention: undefined }));

    expect(screen.queryByRole("button", { name: "@Bob" })).toBeNull();
    const chip = screen.getByText("@Bob");
    expect(chip.tagName).toBe("SPAN");
    // @label 与颜色本身是有信息的：没有去处不等于没有内容。
    expect(chip.className).toContain("agentre-mention");
    expect(chip.className).not.toContain("cursor-pointer");
    expect(chip.style.getPropertyValue("--mention-color")).toBe(
      "var(--agent-3)",
    );
  });
});
