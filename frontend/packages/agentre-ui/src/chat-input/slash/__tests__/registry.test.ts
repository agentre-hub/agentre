import { describe, expect, it } from "vitest";

import {
  SLASH_COMPACT,
  buildSlashCommands,
  isNativeCompactBackend,
  listAvailable,
  skillCommandPrefix,
  skillCommandsFromCatalog,
} from "../registry";
import type { SlashCommand } from "../types";

/** 测试里把 key 原样当文案用 —— 断言要钉的是「取了哪个 key」,不是那句中文。 */
const t = (key: string) => key;

describe("skillCommandPrefix", () => {
  it("按 CLI 自己的协议给出触发字符", () => {
    expect(skillCommandPrefix("claudecode")).toBe("/");
    expect(skillCommandPrefix("piagent")).toBe("/");
    // Codex 的 skill mention 走 $,与它内置的 / 命令不是同一个键。
    expect(skillCommandPrefix("codex")).toBe("$");
  });

  it("没有 skill 这一说的 backend 回 null,而不是猜一个前缀", () => {
    expect(skillCommandPrefix("builtin")).toBeNull();
    expect(skillCommandPrefix("")).toBeNull();
  });
});

describe("buildSlashCommands", () => {
  it("/compact 在三个认它的 backend 上都成立", () => {
    const compact = buildSlashCommands(t).find((c) => c.name === SLASH_COMPACT);
    expect(compact).toBeDefined();
    for (const backend of ["claudecode", "codex", "piagent"]) {
      expect(compact?.resolve(backend)).toEqual({
        kind: "literal_text",
        text: "/compact",
      });
    }
    expect(compact?.resolve("builtin")).toBeNull();
  });

  it("/goal 是 codex 专有 —— 别的 backend 上摆出来会是一颗按不动的命令", () => {
    const goal = buildSlashCommands(t).find((c) => c.name === "goal");
    // 尾随空格是有意的:选中之后光标停在参数位上,用户接着打目标本身。
    expect(goal?.resolve("codex")).toEqual({
      kind: "literal_text",
      text: "/goal ",
    });
    expect(goal?.resolve("claudecode")).toBeNull();
    expect(goal?.resolve("piagent")).toBeNull();
  });

  it("说明文案取自宿主的 t,而不是写死的字符串", () => {
    const commands = buildSlashCommands((key) => `译:${key}`);
    expect(commands.map((c) => c.description)).toEqual([
      "译:slashCommands.compact.description",
      "译:slashCommands.goal.description",
    ]);
  });
});

describe("isNativeCompactBackend", () => {
  it("只有 claudecode 的 CLI 自己认 /compact", () => {
    expect(isNativeCompactBackend("claudecode")).toBe(true);
    // 其余后端由宿主自己的压缩通路承担,所以宿主要能把这一句话认出来并拦下。
    expect(isNativeCompactBackend("codex")).toBe(false);
    expect(isNativeCompactBackend("piagent")).toBe(false);
    expect(isNativeCompactBackend(undefined)).toBe(false);
  });
});

describe("skillCommandsFromCatalog", () => {
  it("裸名字冠上这个 backend 的触发字符", () => {
    const got = skillCommandsFromCatalog(
      "codex",
      [{ name: "brainstorming", description: "先想清楚" }],
      t,
    );
    expect(got).toHaveLength(1);
    expect(got[0]).toMatchObject({
      name: "brainstorming",
      label: "$brainstorming",
      trigger: "$",
      kind: "skill",
      description: "先想清楚",
    });
  });

  it("目录里已经带前缀时不再冠一次", () => {
    const got = skillCommandsFromCatalog("claudecode", [{ name: "/cago" }], t);
    expect(got[0].label).toBe("/cago");
    expect(got[0].name).toBe("cago");
  });

  it("没有说明时回落到按 backend 分的那句默认文案", () => {
    expect(
      skillCommandsFromCatalog("codex", [{ name: "a" }], t)[0].description,
    ).toBe("slashCommands.skill.codexDescription");
    expect(
      skillCommandsFromCatalog("claudecode", [{ name: "a" }], t)[0].description,
    ).toBe("slashCommands.skill.claudeDescription");
    expect(
      skillCommandsFromCatalog("piagent", [{ name: "a" }], t)[0].description,
    ).toBe("slashCommands.skill.piDescription");
  });

  it("重名与空名都只留一条 / 一条不留", () => {
    const got = skillCommandsFromCatalog(
      "codex",
      [{ name: "dup" }, { name: "dup" }, { name: "  " }],
      t,
    );
    expect(got.map((c) => c.name)).toEqual(["dup"]);
  });

  it("换了 backend 的会话上这些命令一律解析不出来", () => {
    const [command] = skillCommandsFromCatalog("codex", [{ name: "a" }], t);
    expect(command.resolve("codex")).toEqual({
      kind: "literal_text",
      text: "$a",
    });
    expect(command.resolve("claudecode")).toBeNull();
  });

  it("没有 skill 这一说的 backend 一条都不产出", () => {
    expect(skillCommandsFromCatalog("builtin", [{ name: "a" }], t)).toEqual([]);
  });
});

describe("listAvailable", () => {
  const extra: SlashCommand = {
    name: "new",
    label: "/new",
    trigger: "/",
    description: "宿主自己的命令",
    resolve: (backend) =>
      backend ? { kind: "literal_text", text: "/new" } : null,
  };

  it("宿主追加的命令与内置的并在一起", () => {
    const names = listAvailable("codex", [...buildSlashCommands(t), extra]).map(
      (c) => c.name,
    );
    expect(names).toContain("compact");
    expect(names).toContain("goal");
    expect(names).toContain("new");
  });

  it("这个 backend 解析不出来的命令不进候选", () => {
    const names = listAvailable("claudecode", [
      ...buildSlashCommands(t),
      extra,
    ]).map((c) => c.name);
    expect(names).not.toContain("goal");
  });

  it("backend 还不知道时一条都不列 —— 列出来就得先猜它支不支持", () => {
    expect(listAvailable("", [...buildSlashCommands(t), extra])).toEqual([]);
  });

  it("触发字符与名字都相同才算重复", () => {
    const shadow: SlashCommand = { ...extra, description: "后来的那条" };
    const got = listAvailable("codex", [
      ...buildSlashCommands(t),
      extra,
      shadow,
    ]);
    expect(got.filter((c) => c.name === "new")).toHaveLength(1);
    expect(got.find((c) => c.name === "new")?.description).toBe(
      "宿主自己的命令",
    );
  });
});
