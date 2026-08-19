import { describe, expect, it } from "vitest";

import { type Model, type ReferenceCounts, modelDeleteability } from "../index";

function model(modelKey: string): Model {
  return { id: 1, modelKey, modelId: "gpt-x" } as Model;
}

function counts(
  backends: number,
  sessions: number,
  routes: number,
): ReferenceCounts {
  return { backends, sessions, routes } as ReferenceCounts;
}

describe("modelDeleteability", () => {
  it("Given a model referenced by backends and sessions, when deleteability is computed, then it is deletable — references no longer block deletion", () => {
    const refs = new Map<string, ReferenceCounts>([["mk1", counts(2, 1, 0)]]);

    expect(modelDeleteability(model("mk1"), "other", refs)).toEqual({
      kind: "ok",
    });
  });

  it("Given reference counts that never loaded, when deleteability is computed, then it is still deletable — counts are informational only", () => {
    expect(modelDeleteability(model("mk1"), "other", new Map())).toEqual({
      kind: "ok",
    });
  });

  it("Given the provider default model, when deleteability is computed, then it stays blocked — a provider invariant, not a reference", () => {
    const refs = new Map<string, ReferenceCounts>([["mk1", counts(0, 0, 0)]]);

    expect(modelDeleteability(model("mk1"), "mk1", refs)).toEqual({
      kind: "default",
    });
  });
});
