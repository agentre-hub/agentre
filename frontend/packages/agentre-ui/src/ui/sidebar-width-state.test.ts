import { beforeEach, describe, expect, it } from "vitest";

import {
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  SIDEBAR_WIDTH_KEY_PREFIX,
  clampSidebarWidth,
  readSidebarWidth,
  writeSidebarWidth,
} from "./sidebar-width-state";

describe("sidebar-width-state", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("returns default width when no record exists", () => {
    expect(readSidebarWidth("chat")).toBe(SIDEBAR_DEFAULT_WIDTH);
  });

  it("returns stored width clamped to [min, max]", () => {
    localStorage.setItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat`, "400");
    expect(readSidebarWidth("chat")).toBe(400);

    localStorage.setItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat`, "50");
    expect(readSidebarWidth("chat")).toBe(SIDEBAR_MIN_WIDTH);

    localStorage.setItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat`, "9999");
    expect(readSidebarWidth("chat")).toBe(SIDEBAR_MAX_WIDTH);
  });

  it("falls back to default for malformed values", () => {
    localStorage.setItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat`, "wide");
    expect(readSidebarWidth("chat")).toBe(SIDEBAR_DEFAULT_WIDTH);
  });

  it("writes clamped width as a string", () => {
    writeSidebarWidth("chat", 380.7);
    expect(localStorage.getItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat`)).toBe("381");

    writeSidebarWidth("chat", 10);
    expect(localStorage.getItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat`)).toBe(
      String(SIDEBAR_MIN_WIDTH),
    );

    writeSidebarWidth("chat", 5000);
    expect(localStorage.getItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat`)).toBe(
      String(SIDEBAR_MAX_WIDTH),
    );
  });

  it("is a no-op when key is empty", () => {
    writeSidebarWidth("", 400);
    expect(localStorage.length).toBe(0);
    expect(readSidebarWidth("")).toBe(SIDEBAR_DEFAULT_WIDTH);
  });

  it("namespaces by key so chat and projects do not collide", () => {
    writeSidebarWidth("chat", 360);
    writeSidebarWidth("projects", 280);
    expect(readSidebarWidth("chat")).toBe(360);
    expect(readSidebarWidth("projects")).toBe(280);
  });

  it("clamps non-finite inputs to default", () => {
    expect(clampSidebarWidth(NaN)).toBe(SIDEBAR_DEFAULT_WIDTH);
    expect(clampSidebarWidth(Infinity)).toBe(SIDEBAR_DEFAULT_WIDTH);
  });

  it("readSidebarWidth honors caller-supplied fallback when no record exists", () => {
    expect(readSidebarWidth("chat-context", 240)).toBe(240);
  });

  it("readSidebarWidth honors caller-supplied fallback for malformed values", () => {
    localStorage.setItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat-context`, "abc");
    expect(readSidebarWidth("chat-context", 240)).toBe(240);
  });

  it("readSidebarWidth still returns stored value (clamped) when fallback is provided", () => {
    localStorage.setItem(`${SIDEBAR_WIDTH_KEY_PREFIX}chat-context`, "300");
    expect(readSidebarWidth("chat-context", 240)).toBe(300);
  });

  it("clampSidebarWidth honors caller-supplied fallback for non-finite inputs", () => {
    expect(clampSidebarWidth(NaN, 240)).toBe(240);
    expect(clampSidebarWidth(Infinity, 240)).toBe(240);
  });
});
