import { describe, expect, it } from "vitest";

import {
  isKnownPath,
  memoryOwnerPath,
  memoryRecentPath,
  navItems,
  pageBreadcrumbKeys,
  parseMemorySelection,
} from "@/lib/app-routing";

describe("memory page routing", () => {
  it("Given the rail, Then memory comes right after Hooks and /memory is a known page", () => {
    const paths = navItems.map((item) => item.path);
    expect(paths.indexOf("/memory")).toBe(paths.indexOf("/hooks") + 1);
    expect(isKnownPath("/memory")).toBe(true);
    expect(pageBreadcrumbKeys["/memory"]).toBe(
      navItems.find((item) => item.path === "/memory")?.labelKey,
    );
  });

  it("Given an owner, Then memoryOwnerPath builds the deep link that selects it", () => {
    expect(memoryOwnerPath({ kind: "agent", id: 12 })).toBe(
      "/memory?owner=agent:12",
    );
    expect(memoryOwnerPath({ kind: "department", id: 3 })).toBe(
      "/memory?owner=department:3",
    );
    expect(memoryOwnerPath({ kind: "project", id: 5 })).toBe(
      "/memory?owner=project:5",
    );
    expect(memoryOwnerPath({ kind: "global", id: 0 })).toBe(
      "/memory?owner=global",
    );
    expect(memoryRecentPath()).toBe("/memory?view=recent");
  });

  it("Given a query string, Then parseMemorySelection reads back what the links wrote", () => {
    expect(parseMemorySelection("?owner=agent:12")).toEqual({
      view: "owner",
      owner: { kind: "agent", id: 12 },
    });
    expect(parseMemorySelection("?owner=agent%3A12")).toEqual({
      view: "owner",
      owner: { kind: "agent", id: 12 },
    });
    expect(parseMemorySelection("?owner=global")).toEqual({
      view: "owner",
      owner: { kind: "global", id: 0 },
    });
    expect(parseMemorySelection("?view=recent")).toEqual({ view: "recent" });
  });

  it("Given no owner or a malformed one, Then the selection falls back to global", () => {
    const global = { view: "owner", owner: { kind: "global", id: 0 } };
    expect(parseMemorySelection("")).toEqual(global);
    expect(parseMemorySelection("?owner=agent:0")).toEqual(global);
    expect(parseMemorySelection("?owner=agent:abc")).toEqual(global);
    expect(parseMemorySelection("?owner=team:3")).toEqual(global);
    expect(parseMemorySelection("?owner=global:4")).toEqual(global);
  });
});
