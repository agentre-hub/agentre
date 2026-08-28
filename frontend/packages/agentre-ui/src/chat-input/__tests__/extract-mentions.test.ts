import { describe, expect, it } from "vitest";

import { extractPlainText } from "../content";
import type { ProseMirrorLikeNode } from "../types";

// 构造一个最小 ProseMirrorLikeNode:doc → paragraph → [text, mention, text]。
function docWith(children: ProseMirrorLikeNode[]): ProseMirrorLikeNode {
  const para: ProseMirrorLikeNode = {
    type: { name: "paragraph" },
    attrs: {},
    descendants(fn) {
      fn(para);
      for (const c of children) fn(c);
    },
  };
  return {
    type: { name: "doc" },
    attrs: {},
    descendants(fn) {
      para.descendants(fn);
    },
  };
}

function text(v: string): ProseMirrorLikeNode {
  return { type: { name: "text" }, text: v, attrs: {}, descendants() {} };
}

function mention(attrs: Record<string, unknown>): ProseMirrorLikeNode {
  return { type: { name: "mention" }, attrs, descendants() {} };
}

describe("extractPlainText with mention nodes", () => {
  it("serializes an agent mention to XML inline", () => {
    const doc = docWith([
      text("ping "),
      mention({ kind: "agent", refId: 12, label: "Reviewer", path: "" }),
      text(" now"),
    ]);
    expect(extractPlainText(doc)).toBe(
      'ping <agent id="12">Reviewer</agent> now',
    );
  });

  it("Given a colored mention, When it is sent, Then its display color is preserved", () => {
    const doc = docWith([
      mention({
        kind: "agent",
        refId: 12,
        label: "Reviewer",
        path: "",
        color: "agent-3",
      }),
    ]);
    expect(extractPlainText(doc)).toBe(
      '<agent id="12" color="agent-3">Reviewer</agent>',
    );
  });

  it("serializes a project mention with path", () => {
    const doc = docWith([
      mention({ kind: "project", refId: 3, label: "Web", path: "/w" }),
    ]);
    expect(extractPlainText(doc)).toBe(
      '<project id="3" path="/w">Web</project>',
    );
  });
});
