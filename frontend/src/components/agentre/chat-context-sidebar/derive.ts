import { mentionsToDisplayText } from "@agentre-hub/agentre-ui";

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

// 工具名按后端各自的原样大小写收录:claudecode 用 PascalCase,codex 用
// file_change(历史消息可能仍是 apply_patch),pi agent 全小写(edit / write / read),
// hermes 是 write_file / patch(路径都在 input.path)。read_file 同样带 input.path,
// 但它是只读工具，绝不能计进「改动」。
const EDIT_TOOLS = new Set([
  "Edit",
  "Write",
  "MultiEdit",
  "file_change",
  "apply_patch",
  "edit",
  "write",
  "write_file",
  "patch",
]);

function textOf(m: Msg): string {
  for (const b of m.blocks ?? []) {
    if ((b as { type?: string }).type === "text") {
      return (b as { text?: string }).text ?? "";
    }
  }
  return "";
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
 * collapseDirChain 是「目录树的链压缩」唯一的判定规则：从 startCursor 开始，
 * 只要当前段「已知且恰好一个子项、该子项是目录、没有直接子文件」，链就吸收那个
 * 子目录继续往下探；否则（子项未知，或有文件，或有 0/多个子项）链在当前段落
 * 终止，当前段落连同它已知的子项（可能是 null）一起作为链尾返回。
 *
 * 调用方决定 cursor 与 childrenOf 的形状：「目录」页懒加载到哪一层，cursor 是
 * 相对路径字符串，childrenOf 只读当前已加载的层，未加载的层返回 null——这正是
 * 「不触发额外即时加载」的落点：本函数完全是纯读取，从不调用任何加载副作用；
 * 等外部把更深的层加载进来，同一份数据下次调用会自然探得更深。
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
