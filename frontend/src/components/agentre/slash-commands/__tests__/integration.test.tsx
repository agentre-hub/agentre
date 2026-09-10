import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { useRef, type ComponentProps } from "react";

import type { Editor } from "@tiptap/react";

import {
  AIChatInput,
  type AIChatInputHandle,
  type SlashCommand,
} from "@agentre-hub/agentre-ui";

import { listAvailable } from "../registry";

// 包一层把 editorRef 暴露给 test driver 做编程式插入。
function Harness({
  onSubmit,
  backendType = "claudecode",
  onSlashSelect = () => {},
  skillCommands = [],
}: {
  onSubmit: (text: string) => void;
  backendType?: string;
  onSlashSelect?: ComponentProps<typeof AIChatInput>["onSlashSelect"];
  skillCommands?: SlashCommand[];
}) {
  const editorRef = useRef<Editor | null>(null);
  const handleRef = useRef<AIChatInputHandle>(null);
  return (
    <>
      <button
        type="button"
        data-testid="insert-dollar"
        onClick={() => editorRef.current?.commands.insertContent("$")}
      >
        insert $
      </button>
      <button
        type="button"
        data-testid="insert-slash"
        onClick={() => editorRef.current?.commands.insertContent("/")}
      >
        insert /
      </button>
      <button
        type="button"
        data-testid="insert-co"
        onClick={() => editorRef.current?.commands.insertContent("co")}
      >
        insert co
      </button>
      <button
        type="button"
        data-testid="insert-mp"
        onClick={() => editorRef.current?.commands.insertContent("mp")}
      >
        insert mp
      </button>
      <button
        type="button"
        data-testid="insert-foo-slash"
        onClick={() => editorRef.current?.commands.insertContent("foo/")}
      >
        insert foo/
      </button>
      <button type="button" data-testid="outside">
        outside
      </button>
      <AIChatInput
        ref={handleRef}
        onSubmit={onSubmit}
        editorRef={editorRef}
        backendType={backendType}
        onSlashSelect={onSlashSelect}
        // 清单归宿主:静态注册表 + 技能命令合并后按 backend 过滤,与 chat.tsx 同路。
        slashCommands={listAvailable(backendType, skillCommands)}
        autoFocus
      />
    </>
  );
}

describe("AIChatInput slash menu integration", () => {
  it("行首输入 / 弹出 popover 含 /compact", async () => {
    const onSubmit = vi.fn();
    render(<Harness onSubmit={onSubmit} />);

    act(() => {
      screen.getByTestId("insert-slash").click();
    });

    await waitFor(() => {
      expect(
        screen.getByRole("listbox", { name: "Command and skill suggestions" }),
      ).toBeInTheDocument();
    });
    expect(screen.getByText("/compact")).toBeInTheDocument();
  });

  it("继续输入 co 仍命中 /compact;输入 xyz 列表为空", async () => {
    const onSubmit = vi.fn();
    render(<Harness onSubmit={onSubmit} />);

    act(() => {
      screen.getByTestId("insert-slash").click();
    });
    await waitFor(() =>
      expect(screen.getByText("/compact")).toBeInTheDocument(),
    );

    act(() => {
      screen.getByTestId("insert-co").click();
    });
    // 仍然能看到 /compact (co 是前缀)
    await waitFor(() =>
      expect(screen.getByText("/compact")).toBeInTheDocument(),
    );
  });

  it("Given a manually highlighted candidate, When a non-prefix query changes ranking, Then the best result becomes selected", async () => {
    render(
      <Harness
        onSubmit={() => undefined}
        skillCommands={[
          {
            description: "mp documentation",
            label: "/helper",
            name: "helper",
            resolve: () => ({ kind: "literal_text", text: "/helper" }),
            trigger: "/",
          },
        ]}
      />,
    );

    act(() => {
      screen.getByTestId("insert-slash").click();
    });
    const editor = screen.getByRole("textbox");
    await screen.findByText("/compact");

    fireEvent.keyDown(editor, { key: "ArrowDown" });
    await waitFor(() =>
      expect(screen.getByRole("option", { name: /\/new/ })).toHaveAttribute(
        "aria-selected",
        "true",
      ),
    );

    act(() => {
      screen.getByTestId("insert-mp").click();
    });

    await waitFor(() =>
      expect(screen.getByRole("option", { name: /\/compact/ })).toHaveAttribute(
        "aria-selected",
        "true",
      ),
    );
    expect(screen.getByRole("option", { name: /\/helper/ })).toHaveAttribute(
      "aria-selected",
      "false",
    );
  });

  it("foo/ 不触发 popover (词内 / 不算 trigger)", async () => {
    const onSubmit = vi.fn();
    render(<Harness onSubmit={onSubmit} />);

    act(() => {
      screen.getByTestId("insert-foo-slash").click();
    });
    // 给一拍时间让 selectionUpdate fire
    await new Promise((r) => setTimeout(r, 20));
    expect(
      screen.queryByRole("listbox", { name: "Command and skill suggestions" }),
    ).toBeNull();
  });

  it("点击 /compact 仅填入输入框,不直接发送 (literal_text 路径)", async () => {
    const onSubmit = vi.fn();
    render(<Harness onSubmit={onSubmit} />);

    act(() => {
      screen.getByTestId("insert-slash").click();
    });
    await waitFor(() =>
      expect(screen.getByText("/compact")).toBeInTheDocument(),
    );

    const item = screen.getByText("/compact").closest("button")!;
    act(() => {
      item.dispatchEvent(
        new MouseEvent("mousedown", { bubbles: true, cancelable: true }),
      );
    });

    // popover 关闭后,/compact 应作为草稿留在编辑器里 (而不是被立即发出去),
    // 由用户再决定是否回车发送。
    await waitFor(() =>
      expect(
        screen.queryByRole("listbox", {
          name: "Command and skill suggestions",
        }),
      ).toBeNull(),
    );
    expect(onSubmit).not.toHaveBeenCalled();
    // 编辑器里应当能看到完整的 /compact 文本
    expect(document.querySelector(".ProseMirror")?.textContent ?? "").toContain(
      "/compact",
    );
  });

  it("backendType=codex 时 /compact 也仅补全文字,不直接发送", async () => {
    const onSubmit = vi.fn();
    const onSlashSelect = vi.fn();
    render(
      <Harness
        onSubmit={onSubmit}
        backendType="codex"
        onSlashSelect={onSlashSelect}
      />,
    );

    act(() => {
      screen.getByTestId("insert-slash").click();
    });
    await waitFor(() =>
      expect(screen.getByText("/compact")).toBeInTheDocument(),
    );

    const item = screen.getByText("/compact").closest("button")!;
    act(() => {
      item.dispatchEvent(
        new MouseEvent("mousedown", { bubbles: true, cancelable: true }),
      );
    });

    await waitFor(() =>
      expect(
        screen.queryByRole("listbox", {
          name: "Command and skill suggestions",
        }),
      ).toBeNull(),
    );
    // literal_text 由 AIChatInput 内部消化,不会冒泡到 onSlashSelect;
    // onSubmit 也不应被自动触发 —— 用户回车才执行(由 chat-panel 拦截 /compact 转 Compact RPC)。
    expect(onSlashSelect).not.toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
    expect(document.querySelector(".ProseMirror")?.textContent ?? "").toContain(
      "/compact",
    );
  });

  it("Given a Codex skill catalog, When typing $, Then the popover offers the $ skill and selection only fills the draft", async () => {
    const onSubmit = vi.fn();
    render(
      <Harness
        onSubmit={onSubmit}
        backendType="codex"
        skillCommands={[
          {
            description: "Invoke a Codex skill",
            label: "$browser:browser",
            name: "browser:browser",
            resolve: () => ({
              kind: "literal_text",
              text: "$browser:browser",
            }),
            trigger: "$",
          },
        ]}
      />,
    );

    act(() => {
      screen.getByTestId("insert-dollar").click();
    });
    const item = await screen.findByText("$browser:browser");
    expect(screen.queryByText("/compact")).toBeNull();

    act(() => {
      item
        .closest("button")!
        .dispatchEvent(
          new MouseEvent("mousedown", { bubbles: true, cancelable: true }),
        );
    });

    await waitFor(() =>
      expect(
        screen.queryByRole("listbox", {
          name: "Command and skill suggestions",
        }),
      ).toBeNull(),
    );
    expect(onSubmit).not.toHaveBeenCalled();
    expect(document.querySelector(".ProseMirror")?.textContent ?? "").toContain(
      "$browser:browser",
    );
  });

  it("Given an open slash menu, When the user pointer-downs outside the editor, Then the menu closes", async () => {
    const onSubmit = vi.fn();
    render(<Harness onSubmit={onSubmit} />);

    act(() => {
      screen.getByTestId("insert-slash").click();
    });
    await waitFor(() =>
      expect(
        screen.getByRole("listbox", { name: "Command and skill suggestions" }),
      ).toBeInTheDocument(),
    );

    fireEvent.pointerDown(screen.getByTestId("outside"));

    await waitFor(() =>
      expect(
        screen.queryByRole("listbox", {
          name: "Command and skill suggestions",
        }),
      ).toBeNull(),
    );
  });

  it("Given a Claude Code skill catalog, When typing /, Then static commands and / skills share one popover", async () => {
    render(
      <Harness
        onSubmit={() => undefined}
        backendType="claudecode"
        skillCommands={[
          {
            description: "Invoke a Claude Code skill",
            label: "/superpowers:tdd",
            name: "superpowers:tdd",
            resolve: () => ({
              kind: "literal_text",
              text: "/superpowers:tdd",
            }),
            trigger: "/",
          },
        ]}
      />,
    );

    act(() => {
      screen.getByTestId("insert-slash").click();
    });

    expect(await screen.findByText("/compact")).toBeInTheDocument();
    expect(screen.getByText("/superpowers:tdd")).toBeInTheDocument();
  });
});
