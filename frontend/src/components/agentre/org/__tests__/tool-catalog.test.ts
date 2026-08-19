import { describe, expect, it } from "vitest";
import i18n from "@/i18n";
import { APPROVAL_TOOLS, buildToolList } from "../tool-catalog";

const t = i18n.getFixedT("en");

describe("buildToolList", () => {
  it("maps tool keys with localized names and descriptions", () => {
    const items = buildToolList(["org"], [{ key: "org", enabled: true }], t);
    expect(items[0].key).toBe("org");
    expect(items[0].name).toBe(t("org.agent.tools.names.org"));
    expect(items[0].description).toBe(t("org.agent.tools.descriptions.org"));
    expect(items[0].granted).toBe(true);
  });

  it("puts granted tools first and keeps the given order inside each group", () => {
    const items = buildToolList(
      ["org", "subagent", "hook"],
      [
        { key: "org", enabled: false },
        { key: "subagent", enabled: false },
        { key: "hook", enabled: true },
      ],
      t,
    );
    expect(items.map((it) => it.key)).toEqual(["hook", "org", "subagent"]);
  });

  it("marks unknown agent tools as not granted", () => {
    const items = buildToolList(["org"], [], t);
    expect(items[0].granted).toBe(false);
  });

  it("marks approval only on granted tools, and on both org and hook", () => {
    expect([...APPROVAL_TOOLS].sort()).toEqual(["hook", "org"]);
    const items = buildToolList(
      ["org", "hook", "subagent"],
      [
        { key: "org", enabled: true },
        { key: "hook", enabled: false },
        { key: "subagent", enabled: true },
      ],
      t,
    );
    const byKey = new Map(items.map((it) => [it.key, it]));
    expect(byKey.get("org")?.approval).toBe(true);
    // 未授权的「脚本 Hook」不标：它属于需审批集合，但还没有写操作可言。
    expect(byKey.get("hook")?.approval).toBe(false);
    expect(byKey.get("subagent")?.approval).toBe(false);
    expect(
      buildToolList(["hook"], [{ key: "hook", enabled: true }], t)[0].approval,
    ).toBe(true);
  });
});
