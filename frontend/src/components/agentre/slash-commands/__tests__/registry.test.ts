import { describe, expect, it } from "vitest";

import type { SlashExec } from "@agentre-hub/agentre-ui";

import { listAvailable, skillCommandsFromCatalog } from "../registry";

describe("slash command registry", () => {
  it("claudecode 可用 /compact", () => {
    const xs = listAvailable("claudecode");
    expect(xs.map((c) => c.name)).toContain("compact");
    const compact = xs.find((c) => c.name === "compact")!;
    const exec = compact.resolve("claudecode")!;
    expect(exec.kind).toBe("literal_text");
    expect((exec as Extract<SlashExec, { kind: "literal_text" }>).text).toBe(
      "/compact",
    );
  });

  it.each(["codex", "piagent"])(
    "%s /compact 也走 literal_text (Enter 时由 chat-panel 拦截转 Compact RPC)",
    (backend) => {
      const xs = listAvailable(backend);
      expect(xs.map((c) => c.name)).toContain("compact");
      const compact = xs.find((c) => c.name === "compact")!;
      const exec = compact.resolve(backend)!;
      expect(exec.kind).toBe("literal_text");
      expect((exec as Extract<SlashExec, { kind: "literal_text" }>).text).toBe(
        "/compact",
      );
    },
  );

  it("codex 可用 /goal，选择后只补全文字，Enter 时由 chat-panel 转 Goal RPC", () => {
    const xs = listAvailable("codex");
    expect(xs.map((c) => c.name)).toContain("goal");
    const goal = xs.find((c) => c.name === "goal")!;
    const exec = goal.resolve("codex")!;
    expect(exec.kind).toBe("literal_text");
    expect((exec as Extract<SlashExec, { kind: "literal_text" }>).text).toBe(
      "/goal ",
    );
  });

  it.each(["claudecode", "codex", "piagent", "builtin"])(
    "%s 可用 /new,resolve 返回 literal_text /new(纯前端 tab 操作,与 backend 无关)",
    (backend) => {
      const xs = listAvailable(backend);
      expect(xs.map((c) => c.name)).toContain("new");
      const cmd = xs.find((c) => c.name === "new")!;
      const exec = cmd.resolve(backend)!;
      expect(exec.kind).toBe("literal_text");
      expect((exec as Extract<SlashExec, { kind: "literal_text" }>).text).toBe(
        "/new",
      );
    },
  );

  it("空 backend 返回空列表", () => {
    expect(listAvailable("")).toEqual([]);
  });

  it("Given backend-native command names, When building suggestions, Then naked and plugin-qualified skills get the backend prefix exactly once", () => {
    expect(
      skillCommandsFromCatalog("codex", [
        { name: "shadcn", description: "Compose UI" },
        { name: "lore:lore-memory", description: "Recall" },
      ]).map((command) => command.label),
    ).toEqual(["$shadcn", "$lore:lore-memory"]);
    expect(
      skillCommandsFromCatalog("claudecode", [
        { name: "cago", description: "Cago conventions" },
      ]).map((command) => command.label),
    ).toEqual(["/cago"]);
    expect(
      skillCommandsFromCatalog("piagent", [
        { name: "skill:review", description: "Review changes" },
      ]).map((command) => command.label),
    ).toEqual(["/skill:review"]);
  });

  it("Given a Claude skill shadows a built-in slash command, When listing suggestions, Then the built-in command appears only once", () => {
    const skills = skillCommandsFromCatalog("claudecode", [
      { name: "compact" },
      { name: "custom" },
    ]);

    expect(
      listAvailable("claudecode", skills).map((command) => command.label),
    ).toEqual(["/compact", "/new", "/custom"]);
  });
});
