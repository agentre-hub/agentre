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

  it("device → fingerprint + label, no numeric id", () => {
    expect(
      serializeMentionXml({
        kind: "device",
        refId: 0,
        label: "工作站",
        fp: "sha256:ab12",
      }),
    ).toBe('<device fp="sha256:ab12">工作站</device>');
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

  it("parses a device tag into a fingerprint-keyed ref", () => {
    expect(
      parseMentionXml('run on <device fp="sha256:ab12">工作站</device>'),
    ).toEqual([
      { type: "text", value: "run on " },
      {
        type: "mention",
        ref: { kind: "device", refId: 0, label: "工作站", fp: "sha256:ab12" },
      },
    ]);
  });

  // 指纹缺失的 <device> 只可能来自脏输入(粘贴 / 旧草稿),标签本身仍要认 ——
  // 否则整段 XML 会原样漏进正文,用户看见的是尖括号而不是 chip。
  it("parses a device tag missing its fp attribute as an empty fingerprint", () => {
    expect(parseMentionXml("<device>NAS</device>")).toEqual([
      {
        type: "mention",
        ref: { kind: "device", refId: 0, label: "NAS", fp: "" },
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
