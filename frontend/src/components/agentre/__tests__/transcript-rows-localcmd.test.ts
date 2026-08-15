import { describe, expect, it } from "vitest";

import { buildTranscriptRows } from "@agentre-ai/agentre-ui";
import type { ChatBlockData } from "@/stores/chat-streams-store";
import type { LocalCommandEntry } from "@/stores/local-commands-store";
import type { chat_svc } from "../../../../wailsjs/go/models";

// F7:本地命令条目(F4 store 的临时态)按 createdAt 归并进 transcript 行,
// 渲染成 LocalCommandCard(F5)。关键不变量:消息绝不被重排,命令只在消息之间
// 按时间插队。

function text(t: string): ChatBlockData {
  return { type: "text", text: t } as ChatBlockData;
}

function message(
  id: number,
  createtime: number,
  blocks: ChatBlockData[],
): chat_svc.ChatMessage {
  return {
    blocks,
    completionTokens: 0,
    createtime,
    durationMs: 0,
    errorText: "",
    id,
    model: "",
    promptTokens: 0,
    role: "assistant",
    seq: id,
    sessionId: 1,
  } as chat_svc.ChatMessage;
}

function localCmd(
  id: string,
  createdAt: number,
  extra: Partial<LocalCommandEntry> = {},
): LocalCommandEntry {
  return {
    command: "ls",
    createdAt,
    id,
    output: "",
    sessionId: 1,
    status: "running",
    ...extra,
  };
}

describe("buildTranscriptRows local command merge", () => {
  it("merges local command entries by createdAt", () => {
    const res = buildTranscriptRows({
      autonomousIds: new Set(),
      displayMessages: [],
      localCommands: [localCmd("t1", 100)],
    });
    const row = res.rows.find((r) => r.item.type === "local_command");
    expect(row).toBeTruthy();
    // @ts-expect-error narrow
    expect(row!.item.entry.id).toBe("t1");
    // 命令行没有 message 引用,messageId 是哨兵 -1。
    expect(row!.message).toBeUndefined();
    expect(row!.messageId).toBe(-1);
    expect(row!.key).toBe("localcmd:t1");
  });

  it("命令按 createdAt 插在两条消息组之间,且消息绝不被重排", () => {
    const { rows, firstRowIndexByMessageId, rowIndexByKey } =
      buildTranscriptRows({
        autonomousIds: new Set(),
        displayMessages: [
          message(1, 100, [text("first")]),
          message(2, 300, [text("second")]),
        ],
        localCommands: [localCmd("c1", 200)],
      });

    // 行序:消息1(createtime 100)→ 命令(createdAt 200)→ 消息2(createtime 300)。
    expect(rows.map((r) => [r.messageId, r.item.type])).toEqual([
      [1, "text"],
      [-1, "local_command"],
      [2, "text"],
    ]);

    // 消息顺序不变:消息1 在消息2 之前。
    expect(firstRowIndexByMessageId.get(1)).toBe(0);
    expect(firstRowIndexByMessageId.get(2)).toBe(2);
    // 命令行不进 firstRowIndexByMessageId(只收 messageId >= 0)。
    expect(firstRowIndexByMessageId.has(-1)).toBe(false);

    // rowIndexByKey 对最终扁平序每一行都正确。
    for (const [idx, row] of rows.entries()) {
      expect(rowIndexByKey.get(row.key)).toBe(idx);
    }
  });

  it("晚于所有消息的命令落到末尾;同插入点多条命令按 createdAt 升序", () => {
    const { rows } = buildTranscriptRows({
      autonomousIds: new Set(),
      displayMessages: [message(1, 100, [text("only")])],
      localCommands: [localCmd("late2", 400), localCmd("late1", 300)],
    });

    expect(rows.map((r) => [r.messageId, r.item.type])).toEqual([
      [1, "text"],
      [-1, "local_command"],
      [-1, "local_command"],
    ]);
    // 末尾两条命令按 createdAt 升序:late1(300)在 late2(400)前。
    expect(rows[1].key).toBe("localcmd:late1");
    expect(rows[2].key).toBe("localcmd:late2");
  });

  it("localCommands 为空时输出与不传时逐项一致(无回归)", () => {
    const args = {
      autonomousIds: new Set<number>(),
      displayMessages: [
        message(1, 100, [text("a")]),
        message(2, 200, [text("b")]),
      ],
    };
    const withEmpty = buildTranscriptRows({ ...args, localCommands: [] });
    const without = buildTranscriptRows(args);
    expect(withEmpty.rows.map((r) => r.key)).toEqual(
      without.rows.map((r) => r.key),
    );
    expect(withEmpty.rows.map((r) => r.messageId)).toEqual(
      without.rows.map((r) => r.messageId),
    );
  });
});
