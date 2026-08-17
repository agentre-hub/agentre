import { describe, expect, it } from "vitest";

import { detectAtTrigger } from "../trigger";

describe("detectAtTrigger", () => {
  it("line-start @ triggers, empty query", () => {
    expect(detectAtTrigger("@")).toEqual({ startOffset: 0, query: "" });
  });
  it("line-start @rev triggers", () => {
    expect(detectAtTrigger("@rev")).toEqual({ startOffset: 0, query: "rev" });
  });
  it("after whitespace triggers with correct offset", () => {
    expect(detectAtTrigger("hi @rev")).toEqual({
      startOffset: 3,
      query: "rev",
    });
  });
  it("email-like foo@bar does not trigger", () => {
    expect(detectAtTrigger("foo@bar")).toBeNull();
  });
  it("query with space ends the trigger", () => {
    expect(detectAtTrigger("@rev iewer")).toBeNull();
  });
  it("no @ → null", () => {
    expect(detectAtTrigger("hello")).toBeNull();
  });
  it("nearest @ to cursor wins", () => {
    expect(detectAtTrigger("@a @co")).toEqual({ startOffset: 3, query: "co" });
  });
});
