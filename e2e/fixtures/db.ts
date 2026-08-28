import { realpathSync } from "node:fs";
import { join, resolve } from "node:path";
import { DatabaseSync } from "node:sqlite";

export const e2eDataDir = () => {
  const value = process.env.AGENTRE_DATA_DIR;
  if (!value) throw new Error("SQLite oracle must be started by the E2E runner");
  return value;
};

const databasePath = () => join(e2eDataDir(), "agentre.db");

function query<T>(sql: string, ...params: Array<string | number>): T[] {
  const db = new DatabaseSync(databasePath(), { readOnly: true });
  try {
    db.exec("PRAGMA busy_timeout = 5000");
    return db.prepare(sql).all(...params) as T[];
  } finally {
    db.close();
  }
}

function queryCount(sql: string, ...params: Array<string | number>): number {
  return query<{ n: number }>(sql, ...params)[0].n;
}

export function mainDatabaseFileFromPragma(): string {
  const mainRows = query<{ name: string; file: string }>("PRAGMA database_list").filter(
    (row) => row.name === "main",
  );
  if (mainRows.length !== 1 || !mainRows[0].file) {
    throw new Error("SQLite oracle must expose exactly one main database file");
  }
  return realpathSync(resolve(mainRows[0].file));
}

export type DesktopProject = {
  id: number;
  name: string;
  path: string;
  sync_id: string;
  sync_version: number;
  local_path_missing: number;
  status: number;
};

export function projectByName(name: string): DesktopProject | undefined {
  return query<DesktopProject>(
    "SELECT id, name, path, sync_id, sync_version, local_path_missing, status FROM projects WHERE name = ? AND status = 1",
    name,
  )[0];
}

export function outboundQueueCountForSyncID(syncID: string): number {
  return queryCount(
    "SELECT COUNT(*) AS n FROM sync_outbound_queue WHERE entity_sync_id = ?",
    syncID,
  );
}

export function runningSessionCount(): number {
  return queryCount("SELECT COUNT(*) AS n FROM chat_sessions WHERE agent_status = 'running'");
}

// 正文按「一块一行」存在 chat_message_blocks（迁移 202608270002 同时删掉了
// chat_messages.blocks_json 那一列），所以按内容找消息要连到块表上。
//
// 只看 codec = 0 的块：超过 4 KiB 的块以 deflate 存储（chat_entity.EncodeBlockData），
// 压缩字节里搜不到明文。E2E 的提示词与假回复都远小于阈值，恒为原样存储；真撞上一个
// 大块时这里宁可漏计也不要给出一个看着对的错数。
// COUNT(DISTINCT m.id)：一条消息有多个块，逐块匹配会把同一条消息数成好几条。
const MESSAGE_BLOCK_MATCH = `SELECT COUNT(DISTINCT m.id) AS n
     FROM chat_messages m
     JOIN chat_message_blocks b ON b.message_id = m.id
    WHERE m.role = ? AND b.codec = 0 AND b.data LIKE '%' || ? || '%'`;

export function userMessageCountContaining(text: string): number {
  return queryCount(MESSAGE_BLOCK_MATCH, "user", text);
}

export function assistantMessageCountContaining(text: string): number {
  return queryCount(MESSAGE_BLOCK_MATCH, "assistant", text);
}

export function errorSessionCountContaining(errorText: string): number {
  return queryCount(
    "SELECT COUNT(DISTINCT s.id) AS n FROM chat_sessions s JOIN chat_messages m ON m.session_id = s.id WHERE s.agent_status = 'error' AND m.role = 'assistant' AND m.error_text LIKE '%' || ? || '%'",
    errorText,
  );
}

export type RemoteSession = {
  id: number;
  agent_status: string;
  provider_session_id: string;
  exec_device_id: number;
  exec_device_fingerprint: string;
  event_cursor: number;
  error_text: string;
};

export function remoteSessionByPrompt(prompt: string): RemoteSession | undefined {
  return query<RemoteSession>(
    `SELECT s.id, s.agent_status, s.provider_session_id, s.exec_device_id,
            s.exec_device_fingerprint, s.event_cursor,
            COALESCE(a.error_text, '') AS error_text
       FROM chat_sessions s
       JOIN chat_messages u ON u.session_id = s.id AND u.role = 'user'
       JOIN chat_message_blocks ub ON ub.message_id = u.id
  LEFT JOIN chat_messages a ON a.session_id = s.id AND a.role = 'assistant'
      WHERE ub.codec = 0 AND ub.data LIKE '%' || ? || '%'
   ORDER BY s.id DESC, a.id DESC
      LIMIT 1`,
    prompt,
  )[0];
}
