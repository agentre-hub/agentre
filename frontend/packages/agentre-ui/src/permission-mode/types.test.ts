import { describe, expect, it } from "vitest";

import {
  isPermissionModeDisabled,
  nextPermissionMode,
  normalizePermissionMode,
} from "./types";

// 模拟 caps.permissionModeMeta 的几种典型 runtime 配置。
const CLAUDECODE_ORDER = [
  "default",
  "acceptEdits",
  "plan",
  "bypassPermissions",
] as const;
const CODEX_ORDER = ["default", "plan"] as const;

describe("nextPermissionMode (claudecode order)", () => {
  it("cycles default → acceptEdits → plan → bypassPermissions → default", () => {
    expect(nextPermissionMode("default", CLAUDECODE_ORDER)).toBe("acceptEdits");
    expect(nextPermissionMode("acceptEdits", CLAUDECODE_ORDER)).toBe("plan");
    expect(nextPermissionMode("plan", CLAUDECODE_ORDER)).toBe(
      "bypassPermissions",
    );
    expect(nextPermissionMode("bypassPermissions", CLAUDECODE_ORDER)).toBe(
      "default",
    );
  });

  it("returns first when current not in order", () => {
    expect(nextPermissionMode("unknown", CLAUDECODE_ORDER)).toBe("default");
  });

  it("returns current when order is empty", () => {
    expect(nextPermissionMode("default", [])).toBe("default");
  });
});

describe("nextPermissionMode (codex order)", () => {
  it("cycles default ↔ plan", () => {
    expect(nextPermissionMode("default", CODEX_ORDER)).toBe("plan");
    expect(nextPermissionMode("plan", CODEX_ORDER)).toBe("default");
  });
});

describe("normalizePermissionMode", () => {
  it("returns raw when in allowedModes", () => {
    expect(
      normalizePermissionMode("acceptEdits", CLAUDECODE_ORDER, "default"),
    ).toBe("acceptEdits");
    expect(
      normalizePermissionMode("bypassPermissions", CLAUDECODE_ORDER, "default"),
    ).toBe("bypassPermissions");
  });

  it("falls back to backendDefault when raw is empty/null/undefined", () => {
    expect(
      normalizePermissionMode(undefined, CLAUDECODE_ORDER, "default", "plan"),
    ).toBe("plan");
    expect(
      normalizePermissionMode(null, CLAUDECODE_ORDER, "default", "acceptEdits"),
    ).toBe("acceptEdits");
    expect(
      normalizePermissionMode(
        "",
        CLAUDECODE_ORDER,
        "default",
        "bypassPermissions",
      ),
    ).toBe("bypassPermissions");
  });

  it("falls back to defaultMode when raw is empty and backendDefault is missing/illegal", () => {
    expect(
      normalizePermissionMode(undefined, CLAUDECODE_ORDER, "default"),
    ).toBe("default");
    expect(
      normalizePermissionMode(
        undefined,
        CLAUDECODE_ORDER,
        "default",
        "garbage",
      ),
    ).toBe("default");
  });

  it("falls back to defaultMode when raw is unknown", () => {
    expect(
      normalizePermissionMode("nonsense", CLAUDECODE_ORDER, "default"),
    ).toBe("default");
  });

  it("backendDefault does not override valid raw", () => {
    expect(
      normalizePermissionMode(
        "acceptEdits",
        CLAUDECODE_ORDER,
        "default",
        "plan",
      ),
    ).toBe("acceptEdits");
  });
});

describe("isPermissionModeDisabled", () => {
  it("claudecode + bypassPermissions + active session + non-bypass launch → disabled", () => {
    expect(
      isPermissionModeDisabled("bypassPermissions", "claudecode", {
        hasActiveSession: true,
        permissionModeAtLaunch: "",
      }),
    ).toBe(true);
  });

  it("claudecode + non-bypass modes → never disabled", () => {
    for (const m of ["default", "acceptEdits", "plan"] as const) {
      expect(
        isPermissionModeDisabled(m, "claudecode", {
          hasActiveSession: true,
          permissionModeAtLaunch: "plan",
        }),
      ).toBe(false);
    }
  });

  it("pre-spawn (no active session) → bypass is always selectable", () => {
    expect(
      isPermissionModeDisabled("bypassPermissions", "claudecode", {
        hasActiveSession: false,
        permissionModeAtLaunch: "",
      }),
    ).toBe(false);
  });

  it("post-spawn launched in bypass → bypass stays selectable", () => {
    expect(
      isPermissionModeDisabled("bypassPermissions", "claudecode", {
        hasActiveSession: true,
        permissionModeAtLaunch: "bypassPermissions",
      }),
    ).toBe(false);
  });

  it("codex / builtin / empty runtimeKey → never disabled", () => {
    expect(
      isPermissionModeDisabled("bypassPermissions", "codex", {
        hasActiveSession: true,
        permissionModeAtLaunch: "",
      }),
    ).toBe(false);
    expect(
      isPermissionModeDisabled("plan", "builtin", {
        hasActiveSession: true,
        permissionModeAtLaunch: "",
      }),
    ).toBe(false);
    expect(
      isPermissionModeDisabled("bypassPermissions", null, {
        hasActiveSession: true,
        permissionModeAtLaunch: "",
      }),
    ).toBe(false);
  });
});
