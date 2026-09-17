export type LocalCommandHistoryScope = {
  readonly deviceId: string;
  readonly cwd: string;
};

export type LocalCommandHistoryEntry = {
  readonly command: string;
  readonly lastUsedAt: number;
};

export const LOCAL_COMMAND_HISTORY_STORAGE_KEY = "agentre.localCommandHistory";

const LOCAL_COMMAND_HISTORY_VERSION = 1;
const MAX_ENTRIES_PER_SCOPE = 100;
/**
 * 时间戳的上界取 ECMAScript `Date` 能表示的最大值：`lastUsedAt` 会被 JSON 往返、
 * 也会被 `new Date(...)` 读。越界的数据（坏存储、旧版本写的脏值）整份丢弃，
 * 不让它把后续记录一起带坏。
 */
const MAX_TIMESTAMP = 8_640_000_000_000_000;

type PersistedLocalCommandHistory = {
  version: typeof LOCAL_COMMAND_HISTORY_VERSION;
  scopes: Record<string, LocalCommandHistoryEntry[]>;
};

export type LocalCommandHistoryStorage = Pick<Storage, "getItem" | "setItem">;

export type LocalCommandHistoryMutation = {
  readonly type: "record" | "clear";
  readonly scopeKey: string;
};

export type LocalCommandHistoryStore = {
  list(scope: LocalCommandHistoryScope): LocalCommandHistoryEntry[];
  subscribe(
    listener: (mutation: LocalCommandHistoryMutation) => void,
  ): () => void;
  /**
   * 记一条命令。`lastUsedAt` 省略时取 `Date.now()`；调用方在**提交那一刻**取好的
   * 时间戳可以传进来，这样并发的两条命令即使先后 resolve，MRU 仍按提交顺序排。
   */
  record(
    scope: LocalCommandHistoryScope,
    command: string,
    lastUsedAt?: number,
  ): void;
  clear(scope: LocalCommandHistoryScope): boolean;
};

type CreateLocalCommandHistoryStoreOptions = {
  storage?: LocalCommandHistoryStorage | null;
};

function browserStorage(): LocalCommandHistoryStorage | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function encodeScopeKey({ deviceId, cwd }: LocalCommandHistoryScope): string {
  const deviceScope = deviceId ? ["remote", deviceId] : ["local"];
  const cwdScope = cwd ? ["cwd", cwd] : ["default"];
  return JSON.stringify([deviceScope, cwdScope]);
}

function isHistoryScopeKey(scopeKey: string): boolean {
  let value: unknown;
  try {
    value = JSON.parse(scopeKey);
  } catch {
    return false;
  }
  if (!Array.isArray(value) || value.length !== 2) return false;

  const [deviceScope, cwdScope] = value;
  if (!Array.isArray(deviceScope) || !Array.isArray(cwdScope)) return false;

  const isLocal = deviceScope.length === 1 && deviceScope[0] === "local";
  const isRemote =
    deviceScope.length === 2 &&
    deviceScope[0] === "remote" &&
    typeof deviceScope[1] === "string" &&
    deviceScope[1].length > 0;
  const isDefault = cwdScope.length === 1 && cwdScope[0] === "default";
  const isCwd =
    cwdScope.length === 2 &&
    cwdScope[0] === "cwd" &&
    typeof cwdScope[1] === "string" &&
    cwdScope[1].length > 0;
  if ((!isLocal && !isRemote) || (!isDefault && !isCwd)) return false;

  return (
    encodeScopeKey({
      deviceId: isRemote ? (deviceScope[1] as string) : "",
      cwd: isCwd ? (cwdScope[1] as string) : "",
    }) === scopeKey
  );
}

function isValidHistoryTimestamp(value: unknown): value is number {
  return (
    typeof value === "number" &&
    Number.isSafeInteger(value) &&
    value >= 0 &&
    value <= MAX_TIMESTAMP
  );
}

function isHistoryEntry(value: unknown): value is LocalCommandHistoryEntry {
  if (!value || typeof value !== "object") return false;
  const entry = value as Partial<LocalCommandHistoryEntry>;
  return (
    typeof entry.command === "string" &&
    entry.command.length > 0 &&
    isValidHistoryTimestamp(entry.lastUsedAt)
  );
}

/**
 * 去重（同一条命令只留最新那次）+ 截断（每档最多 100 条）。
 *
 * 排序是**稳定**的，且新记录总是插在数组最前（见 `record`）—— 所以同一毫秒内的
 * 多条记录就按插入序破平：后记的那条排在前面。这正是去掉那套「预留时间戳」协议
 * 之后仍要守住的那条顺序。
 */
function normalizeEntries(
  entries: readonly LocalCommandHistoryEntry[],
): LocalCommandHistoryEntry[] {
  const commands = new Set<string>();
  return [...entries]
    .sort((left, right) => right.lastUsedAt - left.lastUsedAt)
    .filter(({ command }) => {
      if (commands.has(command)) return false;
      commands.add(command);
      return true;
    })
    .slice(0, MAX_ENTRIES_PER_SCOPE)
    .map(({ command, lastUsedAt }) => ({ command, lastUsedAt }));
}

function decodePersistedHistory(
  raw: string | null,
): PersistedLocalCommandHistory {
  const empty: PersistedLocalCommandHistory = {
    version: LOCAL_COMMAND_HISTORY_VERSION,
    scopes: {},
  };
  if (!raw) return empty;

  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return empty;
  }
  if (!value || typeof value !== "object") return empty;

  const persisted = value as Partial<PersistedLocalCommandHistory>;
  if (
    persisted.version !== LOCAL_COMMAND_HISTORY_VERSION ||
    !persisted.scopes ||
    typeof persisted.scopes !== "object" ||
    Array.isArray(persisted.scopes)
  ) {
    return empty;
  }

  const scopes = Object.create(null) as Record<
    string,
    LocalCommandHistoryEntry[]
  >;
  for (const [scopeKey, entries] of Object.entries(persisted.scopes)) {
    if (
      !isHistoryScopeKey(scopeKey) ||
      !Array.isArray(entries) ||
      !entries.every(isHistoryEntry)
    ) {
      return empty;
    }
    scopes[scopeKey] = normalizeEntries(entries);
  }
  return { version: LOCAL_COMMAND_HISTORY_VERSION, scopes };
}

function readPersistedHistory(
  storage: LocalCommandHistoryStorage | null,
): PersistedLocalCommandHistory {
  if (!storage) return decodePersistedHistory(null);
  try {
    return decodePersistedHistory(
      storage.getItem(LOCAL_COMMAND_HISTORY_STORAGE_KEY),
    );
  } catch {
    return decodePersistedHistory(null);
  }
}

function writePersistedHistory(
  storage: LocalCommandHistoryStorage | null,
  history: PersistedLocalCommandHistory,
): boolean {
  if (!storage) return true;
  try {
    storage.setItem(LOCAL_COMMAND_HISTORY_STORAGE_KEY, JSON.stringify(history));
    return true;
  } catch {
    // Record writes stay best-effort; authoritative clears use the result to roll back.
    return false;
  }
}

export function deriveLocalCommandHistoryScopeKey(
  scope: LocalCommandHistoryScope,
): string {
  return encodeScopeKey(scope);
}

export function createLocalCommandHistoryStore(
  options: CreateLocalCommandHistoryStoreOptions = {},
): LocalCommandHistoryStore {
  const storage =
    "storage" in options ? (options.storage ?? null) : browserStorage();
  const history = readPersistedHistory(storage);
  const listeners = new Set<(mutation: LocalCommandHistoryMutation) => void>();
  const notify = (mutation: LocalCommandHistoryMutation) => {
    for (const listener of [...listeners]) listener(mutation);
  };
  /**
   * 每个作用域最后一次被清掉的时刻。一条**清空前就提交**、清空后才 resolve 的命令
   * 带着更早的时间戳回来时，就落在这里挡下 —— 否则「清空历史」会被一条在途命令
   * 悄悄复活。同毫秒（`usedAt === clearedAt`）放行，免得清完紧接着记的第一条被误伤。
   */
  const clearedAt = new Map<string, number>();

  return {
    list(scope) {
      const key = deriveLocalCommandHistoryScopeKey(scope);
      return (history.scopes[key] ?? []).map(({ command, lastUsedAt }) => ({
        command,
        lastUsedAt,
      }));
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    record(scope, command, lastUsedAt) {
      if (!command) return;
      const key = deriveLocalCommandHistoryScopeKey(scope);
      const usedAt =
        lastUsedAt !== undefined && isValidHistoryTimestamp(lastUsedAt)
          ? lastUsedAt
          : Date.now();
      const cleared = clearedAt.get(key);
      if (cleared !== undefined && usedAt < cleared) return;

      const entries = history.scopes[key] ?? [];
      const existingEntry = entries.find((entry) => entry.command === command);
      if (existingEntry && existingEntry.lastUsedAt >= usedAt) return;

      history.scopes[key] = normalizeEntries([
        { command, lastUsedAt: usedAt },
        ...entries,
      ]);
      writePersistedHistory(storage, history);
      notify({ type: "record", scopeKey: key });
    },
    clear(scope) {
      const key = deriveLocalCommandHistoryScopeKey(scope);
      const nextScopes = { ...history.scopes };
      delete nextScopes[key];
      const nextHistory: PersistedLocalCommandHistory = {
        version: LOCAL_COMMAND_HISTORY_VERSION,
        scopes: nextScopes,
      };
      if (!writePersistedHistory(storage, nextHistory)) return false;

      history.scopes = nextScopes;
      clearedAt.set(key, Date.now());
      notify({ type: "clear", scopeKey: key });
      return true;
    },
  };
}

export const localCommandHistoryStore = createLocalCommandHistoryStore();
