import { describe, expect, it } from "vitest";

import {
  buildSlashCommands,
  listAvailable,
  skillCommandsFromCatalog,
} from "@agentre-hub/agentre-ui";

import { desktopSlashCommands } from "../registry";

// 内置命令(/compact、/goal)、Skill 目录的翻译与按 backend 过滤都归共享包,测试也
// 在那边(packages/agentre-ui/src/chat-input/slash/__tests__/registry.test.ts)。
// 这里只钉**桌面端独有**的那一条,以及它与包里那份清单合在一起之后的结果。
const t = (key: string) => key;

/** 桌面端真正交给输入框的那份清单 —— 与 chat.tsx 的组装同构。 */
function desktopAvailable(
  backendType: string,
  skills: { name: string }[] = [],
) {
  return listAvailable(backendType, [
    ...buildSlashCommands(t),
    ...desktopSlashCommands,
    ...skillCommandsFromCatalog(backendType, skills, t),
  ]);
}

describe("桌面端的 slash 命令清单", () => {
  it("/new 在任何非空 backend 上都摆得出 —— 它与 backend 无关", () => {
    for (const backend of ["claudecode", "codex", "piagent", "builtin"]) {
      expect(desktopAvailable(backend).map((c) => c.label)).toContain("/new");
    }
  });

  it("/new 的说明每次读都重新取,切了语言不会留在旧文案上", () => {
    // 这一条不经过 useSlashCommands 的 t,所以 registry 里用 getter 推迟求值;
    // 写成模块加载时的字面量就会冻结在 import 那一刻的语言上。
    const [command] = desktopSlashCommands;
    expect(command.description).toBeTruthy();
    expect(command.description).toBe(command.description);
  });

  it("backend 还不知道时一条都不列,/new 也不例外", () => {
    expect(desktopAvailable("")).toEqual([]);
  });

  it("Skill 与内置命令重名时,内置那条只出现一次", () => {
    expect(
      desktopAvailable("claudecode", [
        { name: "compact" },
        { name: "custom" },
      ]).map((command) => command.label),
    ).toEqual(["/compact", "/new", "/custom"]);
  });

  it("codex 会话上 /goal 与 $ 前缀的 Skill 并存", () => {
    expect(
      desktopAvailable("codex", [{ name: "shadcn" }]).map((c) => c.label),
    ).toEqual(["/compact", "/goal", "/new", "$shadcn"]);
  });
});
