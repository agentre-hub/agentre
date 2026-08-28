import Document from "@tiptap/extension-document";
import Paragraph from "@tiptap/extension-paragraph";
import Text from "@tiptap/extension-text";
import { Editor } from "@tiptap/core";
import type { JSONContent } from "@tiptap/core";
import { describe, expect, it } from "vitest";

import { Mention } from "../mention-node";

function makeEditor() {
  return new Editor({ extensions: [Document, Paragraph, Text, Mention] });
}

describe("Mention node", () => {
  it("renderHTML emits data-* attributes and @label text", () => {
    const editor = makeEditor();
    editor.commands.setContent({
      type: "doc",
      content: [
        {
          type: "paragraph",
          content: [
            {
              type: "mention",
              attrs: {
                kind: "agent",
                refId: 12,
                label: "Reviewer",
                path: "",
                color: "agent-3",
              },
            },
          ],
        },
      ],
    });
    const html = editor.getHTML();
    expect(html).toContain('data-mention-kind="agent"');
    expect(html).toContain('data-ref-id="12"');
    expect(html).toContain('data-label="Reviewer"');
    expect(html).toContain("@Reviewer");
    editor.destroy();
  });

  it("parseHTML round-trips the attributes", () => {
    const editor = makeEditor();
    editor.commands.setContent(
      '<p><span data-mention-kind="project" data-ref-id="3" data-label="Web" data-path="/w">@Web</span></p>',
    );
    const json = editor.getJSON();
    const node = json.content?.[0]?.content?.[0] as JSONContent | undefined;
    expect(node?.type).toBe("mention");
    expect(node?.attrs).toMatchObject({
      kind: "project",
      refId: 3,
      label: "Web",
      path: "/w",
    });
    editor.destroy();
  });

  it("falls back to kind=agent for an unknown data-mention-kind", () => {
    const editor = makeEditor();
    editor.commands.setContent(
      '<p><span data-mention-kind="garbage" data-ref-id="1" data-label="X">@X</span></p>',
    );
    const node = editor.getJSON().content?.[0]?.content?.[0] as
      | JSONContent
      | undefined;
    expect(node?.attrs?.kind).toBe("agent");
    editor.destroy();
  });

  it("falls back to refId=0 for a non-numeric data-ref-id", () => {
    const editor = makeEditor();
    editor.commands.setContent(
      '<p><span data-mention-kind="agent" data-ref-id="abc" data-label="X">@X</span></p>',
    );
    const node = editor.getJSON().content?.[0]?.content?.[0] as
      | JSONContent
      | undefined;
    expect(node?.attrs?.refId).toBe(0);
    editor.destroy();
  });
});
