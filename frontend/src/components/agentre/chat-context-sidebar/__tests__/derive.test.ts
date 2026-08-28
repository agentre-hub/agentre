import { describe, expect, it } from "vitest";

import {
  collapseDirChain,
  deriveLatestWriteRoot,
  deriveOutline,
  deriveSessionChanges,
} from "../derive";

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

function editBlock(
  toolName: string,
  file_path: string,
  canonicalFiles: Array<{
    path: string;
    kind?: string;
    plus?: number;
    minus?: number;
  }>,
) {
  return {
    type: "tool_use",
    toolName,
    toolInput: { file_path },
    canonical: {
      kind: "file.edit",
      fileEdit: {
        files: canonicalFiles.map((f) => ({
          kind: f.kind ?? "modified",
          hunks: [],
          path: f.path,
          plus: f.plus,
          minus: f.minus,
        })),
      },
    },
  };
}

function writeBlock(toolName: string, path: string, lines: number) {
  return {
    type: "tool_use",
    toolName,
    toolInput: { file_path: path },
    canonical: {
      kind: "file.write",
      fileWrite: { path, content: "", lines, bytes: 0 },
    },
  };
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

describe("deriveSessionChanges", () => {
  const ROOT = "/Users/me/proj";

  it("lists one row per file the tools touched, biggest change first", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("Edit", "a.go", [{ path: "a.go", plus: 1, minus: 1 }]),
        editBlock("Edit", "b.go", [{ path: "b.go", plus: 3, minus: 0 }]),
      ]),
      userMsg(3, "u2"),
      assistantWithBlocks(4, [writeBlock("Write", "c.go", 5)]),
    ];

    expect(deriveSessionChanges(msgs, ROOT).map((r) => r.path)).toEqual([
      "c.go",
      "b.go",
      "a.go",
    ]);
  });

  it("breaks a size tie by path so the order never depends on transcript order", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("Edit", "z.go", [{ path: "z.go", plus: 2, minus: 0 }]),
        editBlock("Edit", "a.go", [{ path: "a.go", plus: 2, minus: 0 }]),
      ]),
    ];

    expect(deriveSessionChanges(msgs, ROOT).map((r) => r.path)).toEqual([
      "a.go",
      "z.go",
    ]);
  });

  it("takes the status from the file's last file.edit patch kind", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("Edit", "new.go", [
          { path: "new.go", kind: "created", plus: 4 },
        ]),
        editBlock("Edit", "gone.go", [
          { path: "gone.go", kind: "deleted", minus: 9 },
        ]),
        editBlock("Edit", "old.go", [
          { path: "old.go", kind: "modified", plus: 1, minus: 1 },
        ]),
      ]),
    ];

    const byPath = new Map(
      deriveSessionChanges(msgs, ROOT).map((r) => [r.path, r.status]),
    );
    expect(byPath.get("new.go")).toBe("created");
    expect(byPath.get("gone.go")).toBe("deleted");
    expect(byPath.get("old.go")).toBe("modified");
  });

  it("keeps only the last call's kind when a file is created and then modified", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("Edit", "a.go", [{ path: "a.go", kind: "created", plus: 4 }]),
      ]),
      userMsg(3, "u2"),
      assistantWithBlocks(4, [
        editBlock("Edit", "a.go", [
          { path: "a.go", kind: "modified", plus: 1, minus: 2 },
        ]),
      ]),
    ];

    const [row] = deriveSessionChanges(msgs, ROOT);
    expect(row.status).toBe("modified");
    expect(row.plus).toBe(5);
    expect(row.minus).toBe(2);
    expect(row.lastTurn).toBe(2);
  });

  it("gives a file whose last call is a full write its own status instead of created or modified", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("Edit", "a.go", [
          { path: "a.go", kind: "modified", plus: 1, minus: 1 },
        ]),
      ]),
      userMsg(3, "u2"),
      assistantWithBlocks(4, [writeBlock("Write", "a.go", 40)]),
    ];

    const [row] = deriveSessionChanges(msgs, ROOT);
    expect(row.status).toBe("written");
  });

  it("counts a full write as added lines only", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [writeBlock("Write", "a.go", 40)]),
    ];

    expect(deriveSessionChanges(msgs, ROOT)[0]).toMatchObject({
      status: "written",
      plus: 40,
      minus: 0,
    });
  });

  it("gives a write-last row the written line count alone, with no minus carried over from the edits before it", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("Edit", "a.go", [
          { path: "a.go", kind: "modified", plus: 5, minus: 3 },
        ]),
      ]),
      userMsg(3, "u2"),
      assistantWithBlocks(4, [writeBlock("Write", "a.go", 40)]),
    ];

    expect(deriveSessionChanges(msgs, ROOT)[0]).toMatchObject({
      status: "written",
      plus: 40,
      minus: 0,
    });
  });

  it("counts edits made after a full write on top of the written lines", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [writeBlock("Write", "a.go", 40)]),
      userMsg(3, "u2"),
      assistantWithBlocks(4, [
        editBlock("Edit", "a.go", [
          { path: "a.go", kind: "modified", plus: 2, minus: 1 },
        ]),
      ]),
    ];

    expect(deriveSessionChanges(msgs, ROOT)[0]).toMatchObject({
      status: "modified",
      plus: 42,
      minus: 1,
    });
  });

  it("falls back to modified when a patch carries an unknown kind", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("file_change", "a.go", [
          { path: "a.go", kind: "", plus: 1, minus: 0 },
        ]),
      ]),
    ];

    expect(deriveSessionChanges(msgs, ROOT)[0].status).toBe("modified");
  });

  it("splits every row into a basename and a directory suffix", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("Edit", "internal/service/chat.go", [
          { path: "internal/service/chat.go", plus: 2 },
        ]),
        editBlock("Edit", "main.go", [{ path: "main.go", plus: 1 }]),
      ]),
    ];

    const rows = deriveSessionChanges(msgs, ROOT);
    expect(rows[0]).toMatchObject({
      path: "internal/service/chat.go",
      name: "chat.go",
      dir: "internal/service",
    });
    expect(rows[1]).toMatchObject({
      path: "main.go",
      name: "main.go",
      dir: "",
    });
  });

  it("normalizes an absolute path inside the work root into one root-relative row", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("Edit", `${ROOT}/internal/a.go`, [
          { path: `${ROOT}/internal/a.go`, plus: 2, minus: 0 },
        ]),
      ]),
      userMsg(3, "u2"),
      assistantWithBlocks(4, [
        editBlock("Edit", "internal/a.go", [
          { path: "internal/a.go", plus: 1, minus: 0 },
        ]),
      ]),
    ];

    const rows = deriveSessionChanges(msgs, ROOT);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ path: "internal/a.go", plus: 3 });
  });

  it("drops every path resolving outside the work root, whatever its shape", () => {
    const outside = [
      "/tmp/patch.diff",
      "~/notes.md",
      "/Users/me/other-repo/main.go",
      "../sibling/main.go",
    ];
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(
        2,
        outside.map((p) => editBlock("Edit", p, [{ path: p, plus: 3 }])),
      ),
    ];

    expect(deriveSessionChanges(msgs, ROOT)).toEqual([]);
  });

  it("keeps the work root's own path out of the list", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("Edit", ROOT, [{ path: ROOT, plus: 1 }]),
      ]),
    ];

    expect(deriveSessionChanges(msgs, ROOT)).toEqual([]);
  });

  it("cannot judge membership without a work root, so it keeps the tool's own paths", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("Edit", "/tmp/patch.diff", [
          { path: "/tmp/patch.diff", plus: 3 },
        ]),
      ]),
    ];

    expect(deriveSessionChanges(msgs, "").map((r) => r.path)).toEqual([
      "/tmp/patch.diff",
    ]);
  });

  it("ignores read tools and mutation blocks that carry no canonical payload", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        {
          type: "tool_use",
          toolName: "Read",
          toolInput: { file_path: "a.go" },
        },
        {
          type: "tool_use",
          toolName: "Edit",
          toolInput: { file_path: "b.go" },
        },
        { type: "text", text: "done" },
      ]),
    ];

    expect(deriveSessionChanges(msgs, ROOT)).toEqual([]);
  });

  it("ignores empty canonical paths so a malformed block cannot open the work root itself", () => {
    const msgs = [
      userMsg(1, "u1"),
      assistantWithBlocks(2, [
        editBlock("Edit", "", [{ path: "", plus: 1 }]),
        writeBlock("Write", "", 3),
      ]),
    ];

    expect(deriveSessionChanges(msgs, ROOT)).toEqual([]);
  });

  it("returns an empty array for an empty transcript", () => {
    expect(deriveSessionChanges([], ROOT)).toEqual([]);
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

describe("deriveLatestWriteRoot", () => {
  const MAIN = "/Users/me/proj";
  const WT = "/Users/me/proj-ia";
  const roots = [MAIN, WT];

  it("Given the last tool write landed in a claimed worktree, When the latest root is derived, Then it is that worktree", () => {
    const msgs = [
      userMsg(1, "go"),
      assistantWithBlocks(2, [
        editBlock("Edit", `${MAIN}/a.go`, [{ path: `${MAIN}/a.go` }]),
        editBlock("Edit", `${WT}/b.go`, [{ path: `${WT}/b.go` }]),
      ]),
    ];

    expect(deriveLatestWriteRoot(msgs, roots)).toBe(WT);
  });

  it("Given the last write landed back in the main repo, When the latest root is derived, Then it is the main repo, not the worktree touched earlier", () => {
    const msgs = [
      userMsg(1, "go"),
      assistantWithBlocks(2, [
        editBlock("Edit", `${WT}/b.go`, [{ path: `${WT}/b.go` }]),
      ]),
      userMsg(3, "again"),
      assistantWithBlocks(4, [writeBlock("Write", `${MAIN}/c.md`, 4)]),
    ];

    expect(deriveLatestWriteRoot(msgs, roots)).toBe(MAIN);
  });

  it("Given every write landed outside the claimed roots, When the latest root is derived, Then nothing is claimed and the caller keeps its current root", () => {
    const msgs = [
      userMsg(1, "go"),
      assistantWithBlocks(2, [
        editBlock("Edit", "/tmp/patch.diff", [{ path: "/tmp/patch.diff" }]),
      ]),
    ];

    expect(deriveLatestWriteRoot(msgs, roots)).toBeNull();
  });

  it("Given nested roots, When a write falls in both subtrees, Then the most specific root wins", () => {
    const nested = [MAIN, `${MAIN}/vendored`];
    const msgs = [
      userMsg(1, "go"),
      assistantWithBlocks(2, [
        editBlock("Edit", `${MAIN}/vendored/x.go`, [
          { path: `${MAIN}/vendored/x.go` },
        ]),
      ]),
    ];

    expect(deriveLatestWriteRoot(msgs, nested)).toBe(`${MAIN}/vendored`);
  });

  it("Given a relative tool path, When the roots cannot disambiguate it, Then no root is claimed by it", () => {
    const msgs = [
      userMsg(1, "go"),
      assistantWithBlocks(2, [
        editBlock("Edit", "internal/turn.go", [{ path: "internal/turn.go" }]),
      ]),
    ];

    expect(deriveLatestWriteRoot(msgs, roots)).toBeNull();
  });
});
