import { describe, expect, it } from "vitest";

import { rankLocalCommandHistory } from "./rank";
import type { LocalCommandHistoryEntry } from "./types";

const entries: LocalCommandHistoryEntry[] = [
  { command: "git checkout main", lastUsedAt: 10 },
  { command: "git cherry-pick abc", lastUsedAt: 30 },
  { command: "git checkout feature", lastUsedAt: 20 },
  { command: "pnpm test", lastUsedAt: 40 },
];

describe("rankLocalCommandHistory", () => {
  it("Given an empty normalized query, when history is ranked, then every entry is returned newest first", () => {
    expect(rankLocalCommandHistory(entries, "   ")).toEqual([
      entries[3],
      entries[1],
      entries[2],
      entries[0],
    ]);
  });

  it("Given a non-empty query, when scores differ or tie, then score wins first and lastUsedAt only breaks equal-score ties", () => {
    const differentScores: LocalCommandHistoryEntry[] = [
      { command: "git checkout", lastUsedAt: 10 },
      { command: "git checkout main", lastUsedAt: 100 },
    ];

    expect(rankLocalCommandHistory(differentScores, "git checkout")).toEqual(
      differentScores,
    );
    expect(rankLocalCommandHistory(entries, "checkout")).toEqual([
      entries[2],
      entries[0],
    ]);
  });

  it("Given a Shell query with internal ASCII spaces, when ranked, then in-order fuzzy matching finds the full command without returning zero-score entries", () => {
    expect(rankLocalCommandHistory(entries, "git ch ma")).toEqual([entries[0]]);
    expect(rankLocalCommandHistory(entries, "no match")).toEqual([]);
  });

  it("Given equal score and timestamp, when ranked, then source order is deterministic and the input is not mutated", () => {
    const tied: LocalCommandHistoryEntry[] = [
      { command: "git branch one", lastUsedAt: 10 },
      { command: "git branch two", lastUsedAt: 10 },
    ];
    const original = [...tied];

    expect(rankLocalCommandHistory(tied, "git branch")).toEqual(tied);
    expect(tied).toEqual(original);
  });
});
