import { beforeEach, describe, expect, it } from "vitest";

import {
  DEFAULT_INDEX_AXIS,
  SIDEBAR_AXIS_KEY,
  readSidebarAxis,
  writeSidebarAxis,
} from "@/lib/sidebar-axis-state";

describe("sidebar-axis-state", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("Given nothing stored, When the axis is read, Then it defaults to 项目 (decision 2: projects bind local path and exec target, so it is the real working context)", () => {
    expect(readSidebarAxis()).toBe("project");
    expect(DEFAULT_INDEX_AXIS).toBe("project");
  });

  it("Given each axis is written, When it is read back, Then the choice survives", () => {
    for (const axis of ["project", "agent", "time"] as const) {
      writeSidebarAxis(axis);
      expect(readSidebarAxis()).toBe(axis);
    }
  });

  it("Given a stored value that is not an axis, When it is read, Then it falls back to the default instead of grouping by a word nobody defined", () => {
    localStorage.setItem(SIDEBAR_AXIS_KEY, "byVibes");

    expect(readSidebarAxis()).toBe("project");
  });

  it("Given localStorage throws, When the axis is read or written, Then it degrades quietly rather than taking the sidebar down with it", () => {
    const original = Object.getOwnPropertyDescriptor(
      globalThis,
      "localStorage",
    );
    Object.defineProperty(globalThis, "localStorage", {
      configurable: true,
      get() {
        throw new Error("private mode");
      },
    });

    try {
      expect(readSidebarAxis()).toBe("project");
      expect(() => writeSidebarAxis("agent")).not.toThrow();
    } finally {
      if (original) Object.defineProperty(globalThis, "localStorage", original);
    }
  });
});
