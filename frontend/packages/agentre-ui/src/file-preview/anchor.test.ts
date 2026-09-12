import { describe, expect, it } from "vitest";

import { clampAnchor } from "./anchor";

describe("clampAnchor", () => {
  it("Given a range well inside the file, when clamped, then both ends survive unchanged", () => {
    expect(clampAnchor({ line: 311, endLine: 330 }, 1000)).toEqual({
      startLine: 311,
      endLine: 330,
    });
  });

  it("Given a single line, when clamped, then the range collapses onto that line", () => {
    expect(clampAnchor({ line: 42 }, 1000)).toEqual({
      startLine: 42,
      endLine: 42,
    });
  });

  it("Given a range whose end runs past the file, when clamped, then the end lands on the last line", () => {
    expect(clampAnchor({ line: 311, endLine: 330 }, 320)).toEqual({
      startLine: 311,
      endLine: 320,
    });
  });

  it("Given a start past the file, when clamped, then both ends land on the last line", () => {
    expect(clampAnchor({ line: 900, endLine: 950 }, 320)).toEqual({
      startLine: 320,
      endLine: 320,
    });
  });

  it("Given a non-positive start, when clamped, then it lands on the first line", () => {
    expect(clampAnchor({ line: 0 }, 320)).toEqual({
      startLine: 1,
      endLine: 1,
    });
  });

  it("Given an end before the start, when clamped, then the range collapses onto the start", () => {
    expect(clampAnchor({ line: 330, endLine: 311 }, 1000)).toEqual({
      startLine: 330,
      endLine: 330,
    });
  });

  it("Given an empty model, when clamped, then it still yields the first line", () => {
    expect(clampAnchor({ line: 311, endLine: 330 }, 0)).toEqual({
      startLine: 1,
      endLine: 1,
    });
  });
});
