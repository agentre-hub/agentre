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

  // 草稿回填:重开一条没发出去的消息时,设备 chip 要连指纹一起回到编辑器里,
  // 否则再发一次就变成一个没有身份的 <device fp="">。
  it("parses a device tag back into a mention node carrying its fingerprint", () => {
    const doc = buildEditorDocFromMessage(
      '<device fp="sha256:ab12">工作站</device>',
    );
    expect(doc.content[0].content).toEqual([
      {
        type: "mention",
        attrs: {
          kind: "device",
          refId: 0,
          label: "工作站",
          path: "",
          fp: "sha256:ab12",
        },
      },
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
