import { describe, expect, it } from "vitest";
import i18n from "i18next";

import { AGENTRE_UI_NAMESPACE } from "../i18n";
import { buildOrgToolList, ORG_APPROVAL_TOOLS } from "./tool-catalog";

// 文案随清单一起进了包的 namespace，所以取的是包那棵树上的 t。
const t = i18n.getFixedT("en", AGENTRE_UI_NAMESPACE);

describe("buildOrgToolList", () => {
  it("maps tool keys with localized names and descriptions", () => {
    const items = buildOrgToolList(["org"], [{ key: "org", enabled: true }], t);
    expect(items[0].key).toBe("org");
    expect(items[0].name).toBe(t("org.agent.tools.names.org"));
    expect(items[0].description).toBe(t("org.agent.tools.descriptions.org"));
    expect(items[0].granted).toBe(true);
  });

  it("puts granted tools first and keeps the given order inside each group", () => {
    const items = buildOrgToolList(
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
    const items = buildOrgToolList(["org"], [], t);
    expect(items[0].granted).toBe(false);
  });

  it("marks approval only on granted tools, and on both org and hook", () => {
    expect([...ORG_APPROVAL_TOOLS].sort()).toEqual(["hook", "org"]);
    const items = buildOrgToolList(
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
      buildOrgToolList(["hook"], [{ key: "hook", enabled: true }], t)[0]
        .approval,
    ).toBe(true);
  });
});
