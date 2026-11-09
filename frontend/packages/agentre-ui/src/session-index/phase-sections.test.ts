import { describe, expect, it } from "vitest";

import { phaseSections } from "./phase-sections";

const DAY = 24 * 60 * 60 * 1000;
const todayStart = 100 * DAY;

function s(
  id: string,
  at: number,
  extra: { lifecycleState?: string; waiting?: boolean } = {},
) {
  return { id, lastMessageAt: at, lifecycleState: "idle", ...extra };
}

describe("phaseSections", () => {
  it("splits into needs-you / in-progress / today / earlier, newest first within each", () => {
    const rows = [
      s("e1", todayStart - 5),
      s("t1", todayStart + 10),
      s("t2", todayStart + 20),
      s("r1", todayStart - 1, { lifecycleState: "running" }),
      s("r2", todayStart + 5, { lifecycleState: "running" }),
      s("w1", todayStart - 9 * DAY, { waiting: true }),
      s("w2", todayStart + 1, { waiting: true, lifecycleState: "running" }),
    ];
    const out = phaseSections(rows, todayStart);
    expect(out.map((x) => [x.key, x.items.map((i) => i.id)])).toEqual([
      ["needsYou", ["w2", "w1"]],
      ["inProgress", ["r2", "r1"]],
      ["today", ["t2", "t1"]],
      ["earlier", ["e1"]],
    ]);
  });

  it("omits empty sections", () => {
    expect(
      phaseSections([s("a", todayStart)], todayStart).map((x) => x.key),
    ).toEqual(["today"]);
    expect(phaseSections([], todayStart)).toEqual([]);
  });

  it("treats exactly the start of today as today, one ms before as earlier", () => {
    const out = phaseSections(
      [s("a", todayStart), s("b", todayStart - 1)],
      todayStart,
    );
    expect(out.map((x) => [x.key, x.items[0].id])).toEqual([
      ["today", "a"],
      ["earlier", "b"],
    ]);
  });

  it("does not mutate its input", () => {
    const rows = [s("a", 1), s("b", 2)];
    phaseSections(rows, todayStart);
    expect(rows.map((r) => r.id)).toEqual(["a", "b"]);
  });
});
