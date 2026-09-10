import {
  classifyLink,
  mentionsToDisplayText,
  resolveToolPathInRoot,
} from "@agentre-hub/agentre-ui";

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

/**
 * 「本次会话」一行的四种状态（spec 决策 14）：前三种直接取 canonical.FileEdit
 * 的 `kind`，`written` 是 `file.write` 自成的第四种——全量写入不携带文件写入前
 * 的状态，冒充 created 会在 Write 覆盖既有文件时说谎，冒充 modified 会让「没有
 * 减数」看起来像一次逐行对比的结果。
 */
export type ChangeStatus = "created" | "modified" | "deleted" | "written";

/**
 * ChangeRow 是「变更」页一行的显示模型（两档同形，spec 决策 12：扁平列表）。
 * `path` 是相对工作根的路径，既是行的 key 也是预览与工具 diff 的入参；`name`
 * 主显、`dir` 是右侧从头截断的目录后缀（根目录下的文件为空串）。
 */
export type ChangeRow = {
  path: string;
  name: string;
  dir: string;
  status: ChangeStatus;
  /**
   * 本会话对该文件累计的加 / 减行数。全量写入是一次**绝对状态**：它之后文件
   * 正好是写进去的那些行，此前那些增量的加减因此不再是这一行能说的事，累计从
   * 写入那一刻重新起算（spec：写入行「只有加数（写入的行数），没有减数」）。
   */
  plus: number;
  minus: number;
  /** 最后一次动到这个文件的轮次，右键菜单的「跳到对应轮次」用它。 */
  lastTurn: number;
};

const EDIT_KINDS: ReadonlySet<string> = new Set([
  "created",
  "modified",
  "deleted",
]);

/** 一次工具调用对一个文件的一次改动。 */
type Touch = {
  path: string;
  status: ChangeStatus;
  plus: number;
  minus: number;
};

/**
 * ChangeSourceBlock 是本档取数只需要读的最小结构：已落库的 `chat_svc.ChatBlock`
 * 与在流的 `ChatBlockData`（= 同一个 wire 形状去掉 wails 注入的 convertValues）
 * 都满足它，两段因此走同一条派生，不必各写一份。
 */
type ChangeSourceBlock = {
  toolUseId?: string;
  canonical?: chat_svc.ChatBlock["canonical"];
};

// canonicalTouches 把一个块的 canonical 载荷拆成若干次改动。行数由 producer 算好
// （plus / minus / lines），前端不重复解析 diff；没有 canonical 的历史块产不出
// 状态与行数，本档因此不收它们——「工具改了什么」的四种状态全部来自 canonical。
function canonicalTouches(block: ChangeSourceBlock): Touch[] {
  const canonical = block.canonical;
  if (!canonical) return [];
  if (canonical.kind === "file.edit") {
    return (canonical.fileEdit?.files ?? [])
      .filter((f) => f.path !== "")
      .map((f) => ({
        path: f.path,
        // 后端多发一个没见过的 kind 时按「已修改」兜底，而不是让整行消失。
        status: (EDIT_KINDS.has(f.kind) ? f.kind : "modified") as ChangeStatus,
        plus: f.plus ?? 0,
        minus: f.minus ?? 0,
      }));
  }
  if (canonical.kind === "file.write" && canonical.fileWrite?.path) {
    return [
      {
        path: canonical.fileWrite.path,
        status: "written",
        // 全量写入只知道写进去多少行，减数无从得知，恒为 0（决策 14）。
        plus: canonical.fileWrite.lines ?? 0,
        minus: 0,
      },
    ];
  }
  return [];
}

/**
 * deriveSessionChanges 回答「本次会话里工具改了什么」（spec 决策 3）：只读
 * canonical 块、不读 git，因此 AI 中途提交、事后 rebase 或 amend 都不影响它。
 *
 * 取数是**已落库的消息 + 当前在流的 liveBlocks 两段**：发送那一刻插进 messages
 * 的 assistant 是 blocks 为空的占位，正在跑的这一轮全部内容都在 liveBlocks 里，
 * 只读 messages 就等于「AI 正在改文件时这一页恒为空」。live 的那一段归属当前
 * 轮次（messages 里最后一条 user 消息那一轮）。
 *
 * 两段可能重叠：轮次跑到一半重新装载会话时，后端已把这一段块落库，而同一批调用
 * 仍留在在流里 —— 按 toolUseId 去重，否则 ±行数每重载一次翻一倍。
 *
 * 一个文件一行，状态取**最后一次**调用（file.edit 取它的 kind，file.write 取
 * 「写入」），`±N` 累计本会话的调用、并在每一次全量写入处重新起算（见
 * ChangeRow.plus），行按变动规模降序、同规模按路径排序。
 */
export function deriveSessionChanges(
  messages: Msg[],
  root: string,
  liveBlocks: readonly ChangeSourceBlock[] = [],
): ChangeRow[] {
  const rows = new Map<string, ChangeRow>();
  const seenToolCalls = new Set<string>();
  let turn = 0;
  const collect = (block: ChangeSourceBlock) => {
    const toolUseId = block.toolUseId;
    if (toolUseId) {
      if (seenToolCalls.has(toolUseId)) return;
      seenToolCalls.add(toolUseId);
    }
    for (const touch of canonicalTouches(block)) {
      const path = resolveToolPathInRoot(touch.path, root);
      if (path === null) continue;
      const cut = path.lastIndexOf("/");
      const row = rows.get(path) ?? {
        path,
        name: cut < 0 ? path : path.slice(cut + 1),
        dir: cut < 0 ? "" : path.slice(0, cut),
        status: touch.status,
        plus: 0,
        minus: 0,
        lastTurn: 0,
      };
      row.status = touch.status;
      if (touch.status === "written") {
        // 全量写入把此前的累计一笔勾销：它不携带写入前的内容,把前面那些
        // 增量的减数留在行上,等于说这次写入做过一次逐行对比——正是决策 14
        // 要避免的那句谎话。写入之后的增量改动再在这个基数上累加。
        row.plus = touch.plus;
        row.minus = 0;
      } else {
        row.plus += touch.plus;
        row.minus += touch.minus;
      }
      row.lastTurn = Math.max(row.lastTurn, turn);
      rows.set(path, row);
    }
  };

  for (const m of messages) {
    if (m.role === "user") {
      turn += 1;
      continue;
    }
    for (const block of m.blocks ?? []) collect(block);
  }
  // 在流的那一段属于当前轮次 —— turn 已经数到最后一条 user 消息，右键「跳到对应
  // 轮次」因此落在这一轮的提问上，而不是上一轮。
  for (const block of liveBlocks) collect(block);

  return [...rows.values()].sort((a, b) => {
    const size = b.plus + b.minus - (a.plus + a.minus);
    return size !== 0 ? size : a.path.localeCompare(b.path);
  });
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

/**
 * rootOfPath 把一条工具路径归到某个已认领的工作根下：与 resolveToolPathInRoot 同一套
 * 判定（classifyLink），但先不带 cwd 分类一次，把**相对路径挡在外面**——多根
 * 会话里 `internal/turn.go` 落在哪个根上无从消歧，硬拼到某一个根上会让侧栏跟
 * 着一条猜出来的归属乱切。嵌套的两个根都包住同一条路径时取最深的那个。
 */
function rootOfPath(raw: string, roots: readonly string[]): string | null {
  const anchored = classifyLink(raw);
  if (
    anchored.kind !== "local-internal" &&
    anchored.kind !== "local-external"
  ) {
    return null;
  }
  let best: string | null = null;
  for (const root of roots) {
    if (root === "") continue;
    const link = classifyLink(anchored.fullPath, root);
    if (link.kind !== "local-internal" || link.relPath === "") continue;
    if (best === null || root.length > best.length) best = root;
  }
  return best;
}

/**
 * deriveLatestWriteRoot 回答「AI 最后一次写文件写在哪个工作根里」（spec
 * 「切换与跟随」）：按消息顺序扫 canonical 的写入块，返回最后一次能归属到某个
 * 已认领工作根的那个根；一次都归属不上（全落在根集合之外、或全是相对路径）时
 * 返回 null —— 调用方据此保持当前工作根不动，而不是切到一个猜出来的根。
 */
export function deriveLatestWriteRoot(
  messages: Msg[],
  roots: readonly string[],
): string | null {
  let latest: string | null = null;
  for (const m of messages) {
    for (const block of m.blocks ?? []) {
      for (const touch of canonicalTouches(block)) {
        const root = rootOfPath(touch.path, roots);
        if (root !== null) latest = root;
      }
    }
  }
  return latest;
}
