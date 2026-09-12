import { describe, expect, it } from "vitest";

import { detectSlashTrigger } from "../trigger";

describe("detectSlashTrigger", () => {
  it("行首 / 触发,query 空", () => {
    expect(detectSlashTrigger("/")).toEqual({
      startOffset: 0,
      query: "",
      trigger: "/",
    });
  });

  it("行首 /co 触发,query=co", () => {
    expect(detectSlashTrigger("/co")).toEqual({
      startOffset: 0,
      query: "co",
      trigger: "/",
    });
  });

  it("空白后 /co 触发,startOffset 指向 /", () => {
    expect(detectSlashTrigger("hello /co")).toEqual({
      startOffset: 6,
      query: "co",
      trigger: "/",
    });
  });

  it("换行后 /com 触发", () => {
    expect(detectSlashTrigger("first line\n/com")).toEqual({
      startOffset: 11,
      query: "com",
      trigger: "/",
    });
  });

  it("词内 foo/bar 不触发", () => {
    expect(detectSlashTrigger("foo/bar")).toBeNull();
  });

  it("/co bar 已结束(query 含空格) → null", () => {
    expect(detectSlashTrigger("/co bar")).toBeNull();
  });

  it("没 / → null", () => {
    expect(detectSlashTrigger("hello world")).toBeNull();
  });

  it("空字符串 → null", () => {
    expect(detectSlashTrigger("")).toBeNull();
  });

  it("多个 / 取离光标最近的一个", () => {
    // ".../some /co" 末尾 / 才是 trigger
    expect(detectSlashTrigger("foo /bar /co")).toEqual({
      startOffset: 9,
      query: "co",
      trigger: "/",
    });
  });

  it("Given Codex skill triggers are enabled, When typing $ after whitespace, Then it returns the skill query", () => {
    expect(detectSlashTrigger("ask $lore:lore", ["/", "$"])).toEqual({
      startOffset: 4,
      query: "lore:lore",
      trigger: "$",
    });
  });

  it("Given only slash triggers are enabled, When typing $, Then it stays ordinary message text", () => {
    expect(detectSlashTrigger("$lore", ["/"])).toBeNull();
  });
});
