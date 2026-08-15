import { mentionsToDisplayText } from "@agentre-ai/agentre-ui";

import type { chat_svc } from "../../../../wailsjs/go/models";

type Msg = chat_svc.ChatMessage;

export type OutlineItem = {
  messageId: number;
  turn: number;
  text: string;
  time: number;
  edits: number;
  err: boolean;
};

export type FileEntry = {
  path: string;
  plus: number;
  minus: number;
  lastTurn: number;
};

export type FileTreeNode =
  | { kind: "dir"; name: string; children: FileTreeNode[] }
  | { kind: "file"; entry: FileEntry };

// 工具名按后端各自的原样大小写收录:claudecode 用 PascalCase,codex 用
// file_change(历史消息可能仍是 apply_patch),pi agent 全小写(edit / write / read)。
const EDIT_TOOLS = new Set([
  "Edit",
  "Write",
  "MultiEdit",
  "file_change",
  "apply_patch",
  "edit",
  "write",
]);
const READ_TOOLS = new Set(["Read", "read"]);

function textOf(m: Msg): string {
  for (const b of m.blocks ?? []) {
    if ((b as { type?: string }).type === "text") {
      return (b as { text?: string }).text ?? "";
    }
  }
  return "";
}

// 从 block.canonical 汇总某条 tool_use 的 plus/minus（producer 已算好行数，前端不重复解析）。
// file.edit 按 fileEdit.files[].path 汇总 plus/minus；file.write 把 fileWrite.lines 计入 plus。
function canonicalDeltas(
  block: chat_svc.ChatBlock,
): Array<{ path: string; plus: number; minus: number }> {
  const canonical = block.canonical;
  if (!canonical) return [];
  if (canonical.kind === "file.edit") {
    return (canonical.fileEdit?.files ?? [])
      .filter((f) => f.path !== "")
      .map((f) => ({
        path: f.path,
        plus: f.plus ?? 0,
        minus: f.minus ?? 0,
      }));
  }
  if (canonical.kind === "file.write" && canonical.fileWrite?.path) {
    return [
      {
        path: canonical.fileWrite.path,
        plus: canonical.fileWrite.lines ?? 0,
        minus: 0,
      },
    ];
  }
  return [];
}

function extractToolPaths(
  block: chat_svc.ChatBlock,
): { name: string; paths: string[] } | null {
  if (block.type !== "tool_use" || !block.toolName) return null;
  const input = block.toolInput ?? {};
  const paths: string[] = [];
  if (typeof input.file_path === "string" && input.file_path !== "") {
    paths.push(input.file_path);
  }
  if (typeof input.path === "string" && input.path !== "") {
    paths.push(input.path);
  }
  const changes = (input as { changes?: Array<{ path?: string }> }).changes;
  if (Array.isArray(changes)) {
    for (const c of changes) {
      if (typeof c?.path === "string" && c.path !== "") paths.push(c.path);
    }
  }
  return paths.length > 0 ? { name: block.toolName, paths } : null;
}

export function deriveOutline(messages: Msg[]): OutlineItem[] {
  const out: OutlineItem[] = [];
  let turn = 0;
  for (let i = 0; i < messages.length; i++) {
    const m = messages[i];
    if (m.role !== "user") continue;
    turn += 1;
    let edits = 0;
    let err = false;
    for (
      let j = i + 1;
      j < messages.length && messages[j].role !== "user";
      j++
    ) {
      const peer = messages[j];
      if (peer.errorText) err = true;
      for (const block of peer.blocks ?? []) {
        const ext = extractToolPaths(block);
        if (ext && EDIT_TOOLS.has(ext.name)) edits += 1;
      }
    }
    out.push({
      messageId: m.id,
      turn,
      text: mentionsToDisplayText(textOf(m)).slice(0, 200),
      time: m.createtime ?? 0,
      edits,
      err,
    });
  }
  return out;
}

export function deriveFiles(messages: Msg[]): FileEntry[] {
  const map = new Map<string, FileEntry>();
  let turn = 0;
  for (const m of messages) {
    if (m.role === "user") {
      turn += 1;
      continue;
    }
    for (const block of m.blocks ?? []) {
      const ext = extractToolPaths(block);
      if (ext) {
        const isEdit = EDIT_TOOLS.has(ext.name);
        const isRead = READ_TOOLS.has(ext.name);
        if (isEdit || isRead) {
          for (const p of ext.paths) {
            const cur = map.get(p) ?? {
              path: p,
              plus: 0,
              minus: 0,
              lastTurn: 0,
            };
            cur.lastTurn = Math.max(cur.lastTurn, turn);
            map.set(p, cur);
          }
        }
      }
      // canonical 路径可能与 toolInput 的 path 不同（如 apply_patch），两者取并集。
      for (const d of canonicalDeltas(block)) {
        const cur = map.get(d.path) ?? {
          path: d.path,
          plus: 0,
          minus: 0,
          lastTurn: 0,
        };
        cur.plus += d.plus;
        cur.minus += d.minus;
        cur.lastTurn = Math.max(cur.lastTurn, turn);
        map.set(d.path, cur);
      }
    }
  }
  return [...map.values()].sort((a, b) => {
    const da = a.plus + a.minus;
    const db = b.plus + b.minus;
    if (db !== da) return db - da;
    return b.lastTurn - a.lastTurn;
  });
}

// 按 "/" 分段把扁平文件列表组织成目录树：目录节点字母序、文件节点保持传入顺序、
// 根目录下的文件成为根级 file 节点；同一层目录排在文件之前。空输入返回空数组。
export function deriveFileTree(files: FileEntry[]): FileTreeNode[] {
  if (files.length === 0) return [];

  const root: FileTreeNode[] = [];
  const dirByPath = new Map<string, Extract<FileTreeNode, { kind: "dir" }>>();

  for (const entry of files) {
    const segs = entry.path.split(/[\\/]/).filter(Boolean);
    let dirPath = "";
    let container = root;
    for (let i = 0; i < segs.length - 1; i++) {
      const parentPath = dirPath;
      dirPath = dirPath ? `${dirPath}/${segs[i]}` : segs[i];
      let dir = dirByPath.get(dirPath);
      if (!dir) {
        dir = { kind: "dir", name: segs[i], children: [] };
        dirByPath.set(dirPath, dir);
        if (parentPath === "") {
          root.push(dir);
        } else {
          dirByPath.get(parentPath)!.children.push(dir);
        }
      }
      container = dir.children;
    }
    container.push({ kind: "file", entry });
  }

  const sortDir = (node: FileTreeNode) => {
    if (node.kind !== "dir") return;
    node.children.sort((a, b) => {
      if (a.kind === "dir" && b.kind === "dir")
        return a.name.localeCompare(b.name);
      if (a.kind === "dir") return -1;
      if (b.kind === "dir") return 1;
      return 0; // 文件保持输入顺序（稳定排序）
    });
    node.children.forEach(sortDir);
  };
  sortDir({ kind: "dir", name: "", children: root });
  return root;
}

/** collapseDirChain 的一个子项：目录还是文件、以及继续探测下一层要用的 cursor。 */
export type ChainEntry<C> = { name: string; isDir: boolean; cursor: C };

export type ChainResult<C> = {
  /** 链上每一段的名字，含链首与链尾（链长 1 时就是未压缩的单段名）。 */
  names: string[];
  /** 链尾（最后一段已确认属于链的目录）的 cursor。 */
  cursor: C;
  /**
   * 链尾目录的直接子项；`null` 表示链尾这一层还未知（目录模式尚未加载到这一
   * 层）——由调用方决定要不要去取，本函数不因为探测链而触发任何加载。
   */
  children: Array<ChainEntry<C>> | null;
};

/**
 * collapseDirChain 是「树形模式的链压缩」唯一的判定规则：从 startCursor 开始，
 * 只要当前段「已知且恰好一个子项、该子项是目录、没有直接子文件」，链就吸收那个
 * 子目录继续往下探；否则（子项未知，或有文件，或有 0/多个子项）链在当前段落
 * 终止，当前段落连同它已知的子项（可能是 null）一起作为链尾返回。
 *
 * 两种调用方各自决定 cursor 与 childrenOf 的形状：「变动」模式（deriveFileTree
 * 整树已知）cursor 直接是节点引用，childrenOf 恒不返回 null；「目录」模式懒加载
 * 到哪一层，cursor 是相对路径字符串，childrenOf 只读当前已加载的层，未加载的层
 * 返回 null——这正是「不触发额外即时加载」的落点：本函数完全是纯读取，从不调用
 * 任何加载副作用；等外部把更深的层加载进来，同一份数据下次调用会自然探得更深。
 */
export function collapseDirChain<C>(
  startName: string,
  startCursor: C,
  childrenOf: (cursor: C) => Array<ChainEntry<C>> | null,
): ChainResult<C> {
  const names = [startName];
  let cursor = startCursor;
  for (;;) {
    const children = childrenOf(cursor);
    if (children === null) return { names, cursor, children: null };
    if (children.length !== 1 || !children[0].isDir) {
      return { names, cursor, children };
    }
    names.push(children[0].name);
    cursor = children[0].cursor;
  }
}
