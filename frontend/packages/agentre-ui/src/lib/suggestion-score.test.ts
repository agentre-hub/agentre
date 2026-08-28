import { describe, expect, it } from "vitest";

import { scoreSuggestion } from "./suggestion-score";

describe("shared suggestion scoring options", () => {
  it("Given an in-order query containing whitespace, When whitespace matching is not enabled, Then it does not fuzzy-match", () => {
    expect(
      scoreSuggestion({ query: "git ch ma", title: "git checkout main" }),
    ).toBe(0);
  });

  it("Given an in-order query containing whitespace, When whitespace matching is enabled, Then it uses the fuzzy tier", () => {
    const input = {
      query: "git ch ma",
      title: "git checkout main",
      allowWhitespaceInOrder: true,
    };

    expect(scoreSuggestion(input)).toBe(30);
  });
});
