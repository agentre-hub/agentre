import { describe, expect, it } from "vitest";

import {
  buildOrgReportToMap as buildReportToMap,
  resolveOrgReportTo as resolveReportTo,
} from "./reporting";
import type { OrgAgentModel, OrgDepartmentModel } from "./types";

const ceoOf = (overrides: Partial<OrgAgentModel> = {}): OrgAgentModel =>
  ({
    id: 1,
    name: "CEO 助手",
    systemBadge: "DEFAULT",
    departmentId: 0,
    parentAgentId: 0,
    ...overrides,
  }) as OrgAgentModel;

const agentOf = (overrides: Partial<OrgAgentModel>): OrgAgentModel =>
  ({
    id: 0,
    name: "",
    systemBadge: "",
    departmentId: 0,
    parentAgentId: 0,
    ...overrides,
  }) as OrgAgentModel;

const deptOf = (overrides: Partial<OrgDepartmentModel>): OrgDepartmentModel =>
  ({
    id: 0,
    name: "",
    parentId: 0,
    leadAgentId: 0,
    ...overrides,
  }) as OrgDepartmentModel;

describe("resolveReportTo", () => {
  it("returns the explicit parent when set and valid", () => {
    const ceo = ceoOf();
    const boss = agentOf({ id: 2, name: "Boss" });
    const me = agentOf({
      id: 3,
      name: "Me",
      parentAgentId: 2,
      departmentId: 10,
    });
    const dept = deptOf({ id: 10, leadAgentId: 99 }); // even if dept points elsewhere

    expect(resolveReportTo(me, [ceo, boss, me], [dept])).toBe(2);
  });

  it("ignores explicit parent that is missing from the agent list", () => {
    const ceo = ceoOf();
    const me = agentOf({ id: 3, parentAgentId: 999, departmentId: 10 });
    const lead = agentOf({ id: 4, name: "Lead", departmentId: 10 });
    const dept = deptOf({ id: 10, leadAgentId: 4 });

    expect(resolveReportTo(me, [ceo, lead, me], [dept])).toBe(4);
  });

  it("ignores explicit parent that points to self", () => {
    const ceo = ceoOf();
    const me = agentOf({ id: 3, parentAgentId: 3, departmentId: 10 });
    const lead = agentOf({ id: 4, departmentId: 10 });
    const dept = deptOf({ id: 10, leadAgentId: 4 });

    expect(resolveReportTo(me, [ceo, lead, me], [dept])).toBe(4);
  });

  it("returns the department leader when not self", () => {
    const ceo = ceoOf();
    const lead = agentOf({ id: 4, name: "Lead", departmentId: 10 });
    const me = agentOf({ id: 5, departmentId: 10 });
    const dept = deptOf({ id: 10, leadAgentId: 4 });

    expect(resolveReportTo(me, [ceo, lead, me], [dept])).toBe(4);
  });

  it("recurses to parent department leader when I am the leader of my dept", () => {
    const ceo = ceoOf();
    const parentLead = agentOf({ id: 2, name: "Parent Lead", departmentId: 1 });
    const me = agentOf({ id: 4, name: "Me", departmentId: 2 });
    const parentDept = deptOf({ id: 1, leadAgentId: 2 });
    const myDept = deptOf({ id: 2, parentId: 1, leadAgentId: 4 });

    expect(
      resolveReportTo(me, [ceo, parentLead, me], [parentDept, myDept]),
    ).toBe(2);
  });

  it("keeps recursing if the parent dept lead is also me, until a different leader appears", () => {
    const ceo = ceoOf();
    const grandLead = agentOf({ id: 2, name: "Grand", departmentId: 1 });
    const me = agentOf({ id: 5, departmentId: 3 });
    const grand = deptOf({ id: 1, leadAgentId: 2 });
    const mid = deptOf({ id: 2, parentId: 1, leadAgentId: 5 }); // me again
    const mine = deptOf({ id: 3, parentId: 2, leadAgentId: 5 }); // me again

    expect(resolveReportTo(me, [ceo, grandLead, me], [grand, mid, mine])).toBe(
      2,
    );
  });

  it("falls back to CEO when no upstream department has a different leader", () => {
    const ceo = ceoOf();
    const me = agentOf({ id: 5, departmentId: 2 });
    const root = deptOf({ id: 1, leadAgentId: 5 }); // me as well
    const mine = deptOf({ id: 2, parentId: 1, leadAgentId: 5 });

    expect(resolveReportTo(me, [ceo, me], [root, mine])).toBe(ceo.id);
  });

  it("falls back to CEO when the agent has no department and no explicit parent", () => {
    const ceo = ceoOf();
    const me = agentOf({ id: 7 });

    expect(resolveReportTo(me, [ceo, me], [])).toBe(ceo.id);
  });

  it("CEO itself resolves to 0 (no upstream)", () => {
    const ceo = ceoOf();
    const me = agentOf({ id: 9, departmentId: 1 });
    const dept = deptOf({ id: 1, leadAgentId: 9 });

    expect(resolveReportTo(ceo, [ceo, me], [dept])).toBe(0);
  });

  it("returns 0 when there is no CEO and no valid leader chain", () => {
    const me = agentOf({ id: 7, departmentId: 1 });
    const dept = deptOf({ id: 1, leadAgentId: 7 });

    expect(resolveReportTo(me, [me], [dept])).toBe(0);
  });

  it("breaks dept.parentId cycles without infinite loop", () => {
    const ceo = ceoOf();
    const me = agentOf({ id: 5, departmentId: 1 });
    // Cycle: dept 1 → dept 2 → dept 1. Both leads are me.
    const d1 = deptOf({ id: 1, parentId: 2, leadAgentId: 5 });
    const d2 = deptOf({ id: 2, parentId: 1, leadAgentId: 5 });

    expect(resolveReportTo(me, [ceo, me], [d1, d2])).toBe(ceo.id);
  });
});

describe("buildReportToMap", () => {
  it("returns a parent id for every agent (0 for roots)", () => {
    const ceo = ceoOf();
    const lead = agentOf({ id: 2, name: "Lead", departmentId: 10 });
    const ic = agentOf({ id: 3, name: "IC", departmentId: 10 });
    const dept = deptOf({ id: 10, leadAgentId: 2 });

    const map = buildReportToMap([ceo, lead, ic], [dept]);

    expect(map.get(ceo.id)).toBe(0);
    expect(map.get(lead.id)).toBe(ceo.id);
    expect(map.get(ic.id)).toBe(lead.id);
  });

  it("handles a three-level org all the way to CEO", () => {
    const ceo = ceoOf();
    const ctoAgent = agentOf({ id: 2, name: "CTO", departmentId: 1 });
    const platformLead = agentOf({
      id: 3,
      name: "Platform Lead",
      departmentId: 2,
    });
    const fe = agentOf({ id: 4, name: "FE", departmentId: 3 });
    const tech = deptOf({ id: 1, leadAgentId: 2 });
    const platform = deptOf({ id: 2, parentId: 1, leadAgentId: 3 });
    const frontend = deptOf({ id: 3, parentId: 2, leadAgentId: 0 }); // no leader

    const map = buildReportToMap(
      [ceo, ctoAgent, platformLead, fe],
      [tech, platform, frontend],
    );

    expect(map.get(fe.id)).toBe(platformLead.id);
    expect(map.get(platformLead.id)).toBe(ctoAgent.id);
    expect(map.get(ctoAgent.id)).toBe(ceo.id);
  });
});
