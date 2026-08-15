import { screen } from "@testing-library/react";
import { renderWithPorts as render } from "@/__tests__/helpers/transcript-ports";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { MarkdownText } from "@agentre-ai/agentre-ui";
import { makeMentionDecorator, prepareMentionText } from "../transcript";

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
    render(
      <MemoryRouter>
        <MarkdownText text={text} decorator={makeMentionDecorator(refs)} />
      </MemoryRouter>,
    );
    expect(screen.getByText("@Bob")).toBeInTheDocument();
  });

  it("Given a mention and an allowed path in the same text, when the transcript renders them, then both own only their matching range", () => {
    const { text, refs } = prepareMentionText(
      'ask <agent id="1">Bob</agent> to inspect frontend/src/chat.tsx',
    );
    render(
      <MemoryRouter>
        <MarkdownText
          text={text}
          cwd="/work/proj"
          decorator={makeMentionDecorator(refs)}
        />
      </MemoryRouter>,
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
    render(
      <MemoryRouter>
        <MarkdownText text={text} decorator={makeMentionDecorator(refs)} />
      </MemoryRouter>,
    );
    expect(
      screen.getByText("@Bob").style.getPropertyValue("--mention-color"),
    ).toBe("var(--agent-3)");
  });
});
