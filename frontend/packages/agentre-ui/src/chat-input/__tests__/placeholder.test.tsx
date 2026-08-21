import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AIChatInput } from "../index";
import type { MentionSources } from "../mentions/types";
import type { SlashCommand } from "../slash/types";

/**
 * 占位文案按**本次真正接上的能力**拼 —— 判据是「这次渲染启用了哪些触发器」，
 * 不是 backendType 查表。
 *
 * 起因：桌面端曾按 backendType 四选一，而 peer-panel 传的正是 `backendType=""`、
 * 没接 `onRunCommand` 也没接 `mentionSources` —— 落到的默认文案许诺 `@ / !` 三样，
 * 它一样都没接。agentre-server 又照抄了第三套。判据换成「接上了什么」之后，
 * 谁少接一样能力就自动少一段，overpromise 在结构上不可能。
 */

function placeholderOf(container: HTMLElement): string {
  return (
    container
      .querySelector("[data-placeholder]")
      ?.getAttribute("data-placeholder") ?? ""
  );
}

const EMPTY_SOURCES: MentionSources = { agents: [], projects: [] };

const SOURCES_WITH_AGENT: MentionSources = {
  agents: [{ kind: "agent", refId: 1, label: "Ada" }],
  projects: [],
};

function cmd(
  name: string,
  trigger: "/" | "$",
  kind?: SlashCommand["kind"],
): SlashCommand {
  return {
    name,
    label: `${trigger}${name}`,
    trigger,
    kind,
    resolve: () => ({ kind: "literal_text", text: `${trigger}${name}` }),
  };
}

describe("AIChatInput 的占位文案", () => {
  it("一样能力都没接时只剩基础提示", () => {
    const { container } = render(<AIChatInput onSubmit={() => {}} />);
    expect(placeholderOf(container)).toBe("Type a message");
  });

  it("显式传 placeholder 时仍然由调用方说了算", () => {
    const { container } = render(
      <AIChatInput onSubmit={() => {}} placeholder="发送消息到远端会话…" />,
    );
    expect(placeholderOf(container)).toBe("发送消息到远端会话…");
  });

  it("mentionSources 非空才提 @", () => {
    const { container: without } = render(
      <AIChatInput onSubmit={() => {}} mentionSources={EMPTY_SOURCES} />,
    );
    expect(placeholderOf(without)).toBe("Type a message");

    const { container: withAgent } = render(
      <AIChatInput onSubmit={() => {}} mentionSources={SOURCES_WITH_AGENT} />,
    );
    expect(placeholderOf(withAgent)).toBe("Type a message · @ to mention");
  });

  it("有清单但没接 onSlashSelect 时不提 /（菜单本来就没启用）", () => {
    const { container } = render(
      <AIChatInput
        onSubmit={() => {}}
        backendType="claudecode"
        slashCommands={[cmd("compact", "/")]}
      />,
    );
    expect(placeholderOf(container)).toBe("Type a message");
  });

  it("接上 / 命令后提 /", () => {
    const { container } = render(
      <AIChatInput
        onSubmit={() => {}}
        backendType="claudecode"
        onSlashSelect={() => {}}
        slashCommands={[cmd("compact", "/")]}
      />,
    );
    expect(placeholderOf(container)).toBe("Type a message · / for commands");
  });

  it("Skill 也走 / 时并进同一段，而不是另起一段", () => {
    const { container } = render(
      <AIChatInput
        onSubmit={() => {}}
        backendType="claudecode"
        onSlashSelect={() => {}}
        slashCommands={[cmd("compact", "/"), cmd("brainstorm", "/", "skill")]}
      />,
    );
    expect(placeholderOf(container)).toBe(
      "Type a message · / for commands and skills",
    );
  });

  it("Skill 走 $ 时单列一段", () => {
    const { container } = render(
      <AIChatInput
        onSubmit={() => {}}
        backendType="codex"
        onSlashSelect={() => {}}
        slashCommands={[cmd("compact", "/"), cmd("brainstorm", "$", "skill")]}
      />,
    );
    expect(placeholderOf(container)).toBe(
      "Type a message · / for commands · $ for skills",
    );
  });

  it("接上 onCommandSubmit 才提 !", () => {
    const { container } = render(
      <AIChatInput onSubmit={() => {}} onCommandSubmit={() => {}} />,
    );
    expect(placeholderOf(container)).toBe(
      "Type a message · ! to run in terminal",
    );
  });

  it("宿主说了不能真执行本地命令时不提 !，哪怕接了 onCommandSubmit", () => {
    // agentre-server 接 onCommandSubmit 只为兜住静默吞字（缺它时包会把 `!foo`
    // clearContent 掉），那一端没有 PTY，真按下去执行不了。
    const { container } = render(
      <AIChatInput
        onSubmit={() => {}}
        onCommandSubmit={() => {}}
        localCommandsEnabled={false}
      />,
    );
    expect(placeholderOf(container)).toBe("Type a message");
  });

  it("四样齐全时逐段拼出来", () => {
    const { container } = render(
      <AIChatInput
        onSubmit={() => {}}
        backendType="codex"
        onSlashSelect={() => {}}
        onCommandSubmit={() => {}}
        mentionSources={SOURCES_WITH_AGENT}
        slashCommands={[cmd("compact", "/"), cmd("brainstorm", "$", "skill")]}
      />,
    );
    expect(placeholderOf(container)).toBe(
      "Type a message · @ to mention · / for commands · $ for skills · ! to run in terminal",
    );
  });

  it("Skill 目录异步到达后占位跟着变", () => {
    // 桌面端的 skill 清单是挂载后异步拉的。编辑器只在挂载时建一次，
    // 占位若在创建时定死，用户看到的就永远是「还没拉到 skill」那一版。
    const { container, rerender } = render(
      <AIChatInput
        onSubmit={() => {}}
        backendType="codex"
        onSlashSelect={() => {}}
        slashCommands={[cmd("compact", "/")]}
      />,
    );
    expect(placeholderOf(container)).toBe("Type a message · / for commands");

    rerender(
      <AIChatInput
        onSubmit={() => {}}
        backendType="codex"
        onSlashSelect={() => {}}
        slashCommands={[cmd("compact", "/"), cmd("brainstorm", "$", "skill")]}
      />,
    );
    expect(placeholderOf(container)).toBe(
      "Type a message · / for commands · $ for skills",
    );
  });
});
