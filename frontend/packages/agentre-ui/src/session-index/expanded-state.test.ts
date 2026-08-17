import { beforeEach, describe, expect, it } from "vitest";

import {
  SIDEBAR_EXPANDED_KEY_PREFIX,
  readSidebarExpanded,
  writeSidebarExpanded,
} from "./expanded-state";

describe("sidebar-expanded-state", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("returns undefined when no record exists", () => {
    expect(readSidebarExpanded("agent:7")).toBeUndefined();
  });

  it("returns true / false based on stored value", () => {
    localStorage.setItem(`${SIDEBAR_EXPANDED_KEY_PREFIX}agent:7`, "1");
    expect(readSidebarExpanded("agent:7")).toBe(true);

    localStorage.setItem(`${SIDEBAR_EXPANDED_KEY_PREFIX}agent:7`, "0");
    expect(readSidebarExpanded("agent:7")).toBe(false);
  });

  it("treats malformed values as undefined", () => {
    localStorage.setItem(`${SIDEBAR_EXPANDED_KEY_PREFIX}agent:7`, "yes");
    expect(readSidebarExpanded("agent:7")).toBeUndefined();
  });

  it("writes 1 / 0 for boolean values", () => {
    writeSidebarExpanded("agent:7", true);
    expect(localStorage.getItem(`${SIDEBAR_EXPANDED_KEY_PREFIX}agent:7`)).toBe(
      "1",
    );
    writeSidebarExpanded("agent:7", false);
    expect(localStorage.getItem(`${SIDEBAR_EXPANDED_KEY_PREFIX}agent:7`)).toBe(
      "0",
    );
  });

  it("is a no-op when key is empty", () => {
    writeSidebarExpanded("", true);
    expect(localStorage.length).toBe(0);
    expect(readSidebarExpanded("")).toBeUndefined();
  });

  it("namespaces by key prefix so project:N and agent:N do not collide", () => {
    writeSidebarExpanded("agent:7", true);
    writeSidebarExpanded("project:7", false);
    expect(readSidebarExpanded("agent:7")).toBe(true);
    expect(readSidebarExpanded("project:7")).toBe(false);
  });
});
