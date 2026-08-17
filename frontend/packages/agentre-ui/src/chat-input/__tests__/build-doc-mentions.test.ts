import { describe, expect, it } from "vitest";

import { buildEditorDocFromMessage } from "../content";

describe("buildEditorDocFromMessage with mention XML", () => {
  it("parses an inline agent tag into a mention node between text nodes", () => {
    const doc = buildEditorDocFromMessage(
      'ping <agent id="12">Reviewer</agent> now',
    );
    expect(doc.content[0].content).toEqual([
      { type: "text", text: "ping " },
      {
        type: "mention",
        attrs: { kind: "agent", refId: 12, label: "Reviewer", path: "" },
      },
      { type: "text", text: " now" },
    ]);
  });

  it("keeps plain lines as plain text (no mention nodes)", () => {
    const doc = buildEditorDocFromMessage("just text");
    expect(doc.content[0].content).toEqual([
      { type: "text", text: "just text" },
    ]);
  });

  it("handles multiple lines, mention on the second", () => {
    const doc = buildEditorDocFromMessage(
      'a\n<project id="3" path="/w">Web</project>',
    );
    expect(doc.content).toHaveLength(2);
    expect(doc.content[1].content).toEqual([
      {
        type: "mention",
        attrs: { kind: "project", refId: 3, label: "Web", path: "/w" },
      },
    ]);
  });
});
