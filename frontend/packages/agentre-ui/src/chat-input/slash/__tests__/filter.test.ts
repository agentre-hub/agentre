import { describe, expect, it } from "vitest";

import { filterByQuery } from "../filter";
import type { SlashCommand } from "../types";

function command(
  name: string,
  trigger: "/" | "$",
  description?: string,
): SlashCommand {
  return {
    name,
    label: `${trigger}${name}`,
    trigger,
    description,
    resolve: () => ({ kind: "literal_text", text: `${trigger}${name}` }),
  };
}

describe("slash command filtering", () => {
  it("Given slash candidates, When filtering by query, Then matching is case-insensitive and keeps empty-query source order", () => {
    const candidates = [command("compact", "/"), command("goal", "/")];

    expect(filterByQuery(candidates, "")).toEqual(candidates);
    expect(filterByQuery(candidates, "COMP").map((c) => c.name)).toEqual([
      "compact",
    ]);
    expect(filterByQuery(candidates, "xyz")).toEqual([]);
  });

  it.each(["/", "$"] as const)(
    "Given %s non-prefix name and description matches, When filtering, Then stronger scores rank first and ties and empty queries keep source order",
    (trigger) => {
      const candidates = [
        command("helper", trigger, "Runs compact migration"),
        command("compact", trigger),
        command("campus", trigger),
      ];

      expect(filterByQuery(candidates, "", trigger)).toEqual(candidates);
      expect(
        filterByQuery(candidates, "mp", trigger).map(
          (candidate) => candidate.name,
        ),
      ).toEqual(["compact", "campus", "helper"]);
    },
  );

  it("Given slash and skill candidates with the same query, When filtering, Then the active trigger stays isolated", () => {
    const candidates = [command("review", "$"), command("review", "/")];

    expect(
      filterByQuery(candidates, "view", "/").map(
        (candidate) => candidate.label,
      ),
    ).toEqual(["/review"]);
    expect(
      filterByQuery(candidates, "view", "$").map(
        (candidate) => candidate.label,
      ),
    ).toEqual(["$review"]);
  });
});
