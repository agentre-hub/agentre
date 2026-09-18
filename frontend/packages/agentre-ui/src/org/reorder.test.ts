import { describe, expect, it } from "vitest";

import { computeOrgReorder as computeReorder } from "./reorder";

describe("computeReorder", () => {
  it("moves dragged item to insertIndex=0 (beginning)", () => {
    // orderedIds = [1, 2, 3], drag id=3 to index=0
    expect(computeReorder([1, 2, 3], 3, 0)).toEqual([3, 1, 2]);
  });

  it("moves dragged item to the end", () => {
    // orderedIds = [1, 2, 3], drag id=1 to index=3 (end)
    expect(computeReorder([1, 2, 3], 1, 3)).toEqual([2, 3, 1]);
  });

  it("moves dragged item to the middle", () => {
    // orderedIds = [1, 2, 3, 4], drag id=4 to index=1
    expect(computeReorder([1, 2, 3, 4], 4, 1)).toEqual([1, 4, 2, 3]);
  });

  it("is a no-op when item stays at same relative position", () => {
    expect(computeReorder([1, 2, 3], 1, 0)).toEqual([1, 2, 3]);
  });

  it("drags rightward past self into a middle slot (full-group index)", () => {
    // drag id=1 to slot 2 ("between original 2 and 3") → [2, 1, 3], not [2, 3, 1]
    expect(computeReorder([1, 2, 3], 1, 2)).toEqual([2, 1, 3]);
  });

  it("is a no-op when dropped in the slot just after the dragged element", () => {
    expect(computeReorder([1, 2, 3], 2, 2)).toEqual([1, 2, 3]);
  });

  it("is a no-op when dropped in the slot just before the dragged element", () => {
    expect(computeReorder([1, 2, 3], 2, 1)).toEqual([1, 2, 3]);
  });
});
