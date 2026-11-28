import { describe, expect, it } from "vitest";

import { collapseDirChain, deriveOutline } from "../derive";

import type { chat_svc } from "../../../../../wailsjs/go/models";

type Msg = chat_svc.ChatMessage;

function userMsg(id: number, text: string, t = 0): Msg {
  return {
    id,
    role: "user",
    sessionId: 1,
    blocks: [{ type: "text", text }],
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: 0,
    createtime: t,
  } as unknown as Msg;
}

function assistantWithEdits(id: number, files: string[], errored = false): Msg {
  const blocks = files.map((p) => ({
    type: "tool_use",
    toolName: "Edit",
    toolInput: { file_path: p },
  }));
  return {
    id,
    role: "assistant",
    sessionId: 1,
    blocks,
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    durationMs: 0,
    errorText: errored ? "boom" : "",
    seq: 0,
    createtime: 0,
  } as unknown as Msg;
}

function assistantWithBlocks(id: number, blocks: unknown[]): Msg {
  return {
    id,
    role: "assistant",
    sessionId: 1,
    blocks,
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: 0,
    createtime: 0,
  } as unknown as Msg;
}

describe("deriveOutline", () => {
  it("treats each user message as one row in chronological order", () => {
    const msgs = [userMsg(1, "first", 1000), userMsg(2, "second", 2000)];
    const out = deriveOutline(msgs);
    expect(out).toHaveLength(2);
    expect(out[0].turn).toBe(1);
    expect(out[1].turn).toBe(2);
    expect(out[0].text).toBe("first");
  });

  it("counts edits between this user msg and the next", () => {
    const msgs = [
      userMsg(1, "do edits"),
      assistantWithEdits(2, ["a.go", "b.go"]),
      userMsg(3, "next"),
      assistantWithEdits(4, ["c.go"]),
    ];
    const out = deriveOutline(msgs);
    expect(out[0].edits).toBe(2);
    expect(out[1].edits).toBe(1);
  });

  // Hermes 的写文件工具用的是自己那套 wire 形状（`input.path`），与 claudecode 的
  // PascalCase / file_path 不同。read_file 同样是 input.path，但它是只读工具，
  // 不能计进「改动」；terminal 只有 command/workdir，连路径都没有。
  it("counts Hermes write_file and patch as edits but ignores read_file and terminal", () => {
    const msgs = [
      userMsg(1, "do hermes edits"),
      assistantWithBlocks(2, [
        {
          type: "tool_use",
          toolName: "write_file",
          toolInput: { path: "/tmp/a.txt", content: "OK\n" },
        },
        {
          type: "tool_use",
          toolName: "patch",
          toolInput: {
            path: "/tmp/a.txt",
            old_string: "OK",
            new_string: "DONE",
          },
        },
        {
          type: "tool_use",
          toolName: "read_file",
          toolInput: { path: "/tmp/a.txt" },
        },
        {
          type: "tool_use",
          toolName: "terminal",
          toolInput: { command: "ls -l", workdir: "/tmp" },
        },
      ]),
    ];

    expect(deriveOutline(msgs)[0].edits).toBe(2);
  });

  it("marks err=true if the following assistant has errorText", () => {
    const msgs = [userMsg(1, "trigger"), assistantWithEdits(2, [], true)];
    const out = deriveOutline(msgs);
    expect(out[0].err).toBe(true);
  });

  it("returns empty array for empty input", () => {
    expect(deriveOutline([])).toEqual([]);
  });

  it("renders @ mention XML in the message text as readable @label, not raw tags", () => {
    const msgs = [userMsg(1, 'ping <agent id="1">CEO 助手</agent> now')];
    const out = deriveOutline(msgs);
    expect(out[0].text).toBe("ping @CEO 助手 now");
  });
});

describe("collapseDirChain", () => {
  // 「变动」模式的数据形状：整棵树一次性可得，cursor 就是节点本身。
  type TreeChild = { name: string; isDir: boolean; children?: TreeChild[] };
  function treeChildrenOf(node: TreeChild) {
    if (!node.isDir || node.children === undefined) return null;
    return node.children.map((c) => ({
      name: c.name,
      isDir: c.isDir,
      cursor: c,
    }));
  }

  // 「目录」模式的数据形状：逐层懒加载，cursor 是相对路径字符串，未加载的层
  // 在 levels 里没有对应 key（用 undefined 表示「还不知道」，而不是空数组）。
  function levelsChildrenOf(
    levels: Record<string, Array<{ name: string; isDir: boolean }>>,
  ) {
    return (path: string) => {
      const entries = levels[path];
      if (entries === undefined) return null;
      return entries.map((e) => ({
        name: e.name,
        isDir: e.isDir,
        cursor: path ? `${path}/${e.name}` : e.name,
      }));
    };
  }

  it("folds a single-subdirectory, file-free chain into one row (tree data shape)", () => {
    const chatSvc: TreeChild = {
      name: "chat_svc",
      isDir: true,
      children: [{ name: "chat.go", isDir: false }],
    };
    const service: TreeChild = {
      name: "service",
      isDir: true,
      children: [chatSvc],
    };
    const internal: TreeChild = {
      name: "internal",
      isDir: true,
      children: [service],
    };

    const result = collapseDirChain("internal", internal, treeChildrenOf);

    expect(result.names).toEqual(["internal", "service", "chat_svc"]);
    expect(result.children).toEqual([
      {
        name: "chat.go",
        isDir: false,
        cursor: { name: "chat.go", isDir: false },
      },
    ]);
  });

  it("does not fold past a directory that directly contains a file", () => {
    const b: TreeChild = {
      name: "b",
      isDir: true,
      children: [{ name: "x.go", isDir: false }],
    };
    const a: TreeChild = {
      name: "a",
      isDir: true,
      children: [b, { name: "y.go", isDir: false }],
    };

    const result = collapseDirChain("a", a, treeChildrenOf);

    expect(result.names).toEqual(["a"]);
    expect(result.children).toHaveLength(2);
  });

  it("does not fold past a directory with multiple subdirectories", () => {
    const a: TreeChild = {
      name: "a",
      isDir: true,
      children: [
        { name: "b", isDir: true, children: [] },
        { name: "c", isDir: true, children: [] },
      ],
    };

    const result = collapseDirChain("a", a, treeChildrenOf);

    expect(result.names).toEqual(["a"]);
    expect(result.children?.map((c) => c.name)).toEqual(["b", "c"]);
  });

  it("folds a chain that starts at the tree root, not just nested ones", () => {
    // 根层的单子目录链（root 本身没有实体行，链从根下第一个目录开始）与嵌套
    // 处的链走同一条规则——不需要「祖先层已经是链」这个前提。
    const empty: TreeChild = { name: "b", isDir: true, children: [] };
    const a: TreeChild = { name: "a", isDir: true, children: [empty] };

    const result = collapseDirChain("a", a, treeChildrenOf);

    expect(result.names).toEqual(["a", "b"]);
    expect(result.children).toEqual([]);
  });

  it("lazily-loaded shape: folds only the portion already known, without needing an unopened level", () => {
    const levels = {
      internal: [{ name: "service", isDir: true }],
      // "internal/service" 尚未加载：levelsChildrenOf 对它返回 null。
    };
    const childrenOf = levelsChildrenOf(levels);

    const result = collapseDirChain("internal", "internal", childrenOf);

    expect(result.names).toEqual(["internal", "service"]);
    expect(result.cursor).toBe("internal/service");
    expect(result.children).toBeNull();
  });

  it("lazily-loaded shape: extends correctly once a deeper level has arrived", () => {
    const levels = {
      internal: [{ name: "service", isDir: true }],
      "internal/service": [{ name: "chat_svc", isDir: true }],
      "internal/service/chat_svc": [{ name: "chat.go", isDir: false }],
    };
    const childrenOf = levelsChildrenOf(levels);

    const result = collapseDirChain("internal", "internal", childrenOf);

    expect(result.names).toEqual(["internal", "service", "chat_svc"]);
    expect(result.cursor).toBe("internal/service/chat_svc");
    expect(result.children).toEqual([
      {
        name: "chat.go",
        isDir: false,
        cursor: "internal/service/chat_svc/chat.go",
      },
    ]);
  });

  it("lazily-loaded shape: a start whose own level is unloaded folds to itself alone", () => {
    const childrenOf = levelsChildrenOf({});

    const result = collapseDirChain("internal", "internal", childrenOf);

    expect(result.names).toEqual(["internal"]);
    expect(result.cursor).toBe("internal");
    expect(result.children).toBeNull();
  });
});
