import { describe, expect, it } from "vitest";

import {
  mentionsToDisplayText,
  parseMentionXml,
  serializeMentionXml,
} from "./xml";

describe("serializeMentionXml", () => {
  it("agent → id + label element text", () => {
    expect(
      serializeMentionXml({ kind: "agent", refId: 12, label: "Reviewer" }),
    ).toBe('<agent id="12">Reviewer</agent>');
  });

  it("project → id + path + label", () => {
    expect(
      serializeMentionXml({
        kind: "project",
        refId: 3,
        label: "proj",
        path: "/Users/me/proj",
      }),
    ).toBe('<project id="3" path="/Users/me/proj">proj</project>');
  });

  it("escapes XML-special chars in label and path", () => {
    expect(
      serializeMentionXml({
        kind: "project",
        refId: 4,
        label: "a & b <x>",
        path: '/p/"q"',
      }),
    ).toBe(
      '<project id="4" path="/p/&quot;q&quot;">a &amp; b &lt;x&gt;</project>',
    );
  });
});

describe("parseMentionXml", () => {
  it("splits text around an agent tag", () => {
    expect(parseMentionXml('hi <agent id="12">Reviewer</agent> there')).toEqual(
      [
        { type: "text", value: "hi " },
        {
          type: "mention",
          ref: { kind: "agent", refId: 12, label: "Reviewer" },
        },
        { type: "text", value: " there" },
      ],
    );
  });

  it("parses a project tag with path and unescapes entities", () => {
    expect(
      parseMentionXml(
        '<project id="3" path="/p/&quot;q&quot;">a &amp; b</project>',
      ),
    ).toEqual([
      {
        type: "mention",
        ref: { kind: "project", refId: 3, label: "a & b", path: '/p/"q"' },
      },
    ]);
  });

  it("plain text with no tags → single text segment", () => {
    expect(parseMentionXml("just text")).toEqual([
      { type: "text", value: "just text" },
    ]);
  });

  it("serialize → parse round-trips", () => {
    const ref = {
      kind: "project" as const,
      refId: 9,
      label: "My <Proj>",
      path: "/a/b c",
    };
    expect(parseMentionXml(serializeMentionXml(ref))).toEqual([
      { type: "mention", ref },
    ]);
  });
});

describe("mentionsToDisplayText", () => {
  it("renders an agent tag as @label", () => {
    expect(mentionsToDisplayText('<agent id="1">CEO 助手</agent>')).toBe(
      "@CEO 助手",
    );
  });

  it("renders a project tag as @label, dropping the path attr", () => {
    expect(
      mentionsToDisplayText(
        '<project id="2" path="/Users/me/web">Web</project>',
      ),
    ).toBe("@Web");
  });

  it("preserves surrounding text", () => {
    expect(
      mentionsToDisplayText('ping <agent id="1">CEO 助手</agent> now'),
    ).toBe("ping @CEO 助手 now");
  });

  it("unescapes XML-escaped labels", () => {
    expect(mentionsToDisplayText('<agent id="1">a &amp; b</agent>')).toBe(
      "@a & b",
    );
  });

  it("leaves plain text with no tags unchanged", () => {
    expect(mentionsToDisplayText("just text")).toBe("just text");
  });
});
