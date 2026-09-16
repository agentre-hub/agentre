import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  LOCAL_COMMAND_HISTORY_STORAGE_KEY,
  createLocalCommandHistoryStore,
  deriveLocalCommandHistoryScopeKey,
  type LocalCommandHistoryScope,
} from "../local-command-history-store";

const localRepo: LocalCommandHistoryScope = { deviceId: "", cwd: "/repo" };
const localOtherRepo: LocalCommandHistoryScope = {
  deviceId: "",
  cwd: "/other",
};
const remoteRepo: LocalCommandHistoryScope = {
  deviceId: "device-1",
  cwd: "/repo",
};

beforeEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

describe("local command history scope and persistence", () => {
  it("Given local and remote submissions in different cwd scopes, when reconstructed, then only versioned command MRU data is restored per deterministic scope", () => {
    const store = createLocalCommandHistoryStore({ storage: localStorage });

    store.record(localRepo, "pnpm test", 10);
    store.record(localOtherRepo, "go test ./...", 20);
    store.record(remoteRepo, "make deploy", 30);

    const localDefault = deriveLocalCommandHistoryScopeKey({
      deviceId: "",
      cwd: "",
    });
    const remoteDefault = deriveLocalCommandHistoryScopeKey({
      deviceId: "device-1",
      cwd: "",
    });
    expect(localDefault).not.toBe(remoteDefault);
    expect(deriveLocalCommandHistoryScopeKey(localRepo)).not.toBe(
      deriveLocalCommandHistoryScopeKey(remoteRepo),
    );
    expect(deriveLocalCommandHistoryScopeKey(localRepo)).not.toBe(
      deriveLocalCommandHistoryScopeKey(localOtherRepo),
    );

    const reconstructed = createLocalCommandHistoryStore({
      storage: localStorage,
    });
    expect(reconstructed.list(localRepo)).toEqual([
      { command: "pnpm test", lastUsedAt: 10 },
    ]);
    expect(reconstructed.list(localOtherRepo)).toEqual([
      { command: "go test ./...", lastUsedAt: 20 },
    ]);
    expect(reconstructed.list(remoteRepo)).toEqual([
      { command: "make deploy", lastUsedAt: 30 },
    ]);

    expect(
      JSON.parse(localStorage.getItem(LOCAL_COMMAND_HISTORY_STORAGE_KEY)!),
    ).toEqual({
      version: 1,
      scopes: {
        [deriveLocalCommandHistoryScopeKey(localRepo)]: [
          { command: "pnpm test", lastUsedAt: 10 },
        ],
        [deriveLocalCommandHistoryScopeKey(localOtherRepo)]: [
          { command: "go test ./...", lastUsedAt: 20 },
        ],
        [deriveLocalCommandHistoryScopeKey(remoteRepo)]: [
          { command: "make deploy", lastUsedAt: 30 },
        ],
      },
    });
  });

  it("Given repeated records at one millisecond, when listed, then the later record wins the same-timestamp tie and persists in that order", () => {
    vi.spyOn(Date, "now").mockReturnValue(100);
    const store = createLocalCommandHistoryStore({ storage: localStorage });

    store.record(localRepo, "first");
    store.record(localRepo, "second");
    store.record(localRepo, "third");

    const expected = [
      { command: "third", lastUsedAt: 100 },
      { command: "second", lastUsedAt: 100 },
      { command: "first", lastUsedAt: 100 },
    ];
    expect(store.list(localRepo)).toEqual(expected);
    // 重建读回的是持久化的数组序，同毫秒的插入序不能被归一化打乱。
    expect(
      createLocalCommandHistoryStore({ storage: localStorage }).list(localRepo),
    ).toEqual(expected);
  });

  it("Given explicit same-millisecond records, when a later one is recorded, then it is prepended ahead of the equal-timestamp one", () => {
    const store = createLocalCommandHistoryStore({ storage: localStorage });

    store.record(localRepo, "alpha", 50);
    store.record(localRepo, "beta", 50);

    expect(store.list(localRepo)).toEqual([
      { command: "beta", lastUsedAt: 50 },
      { command: "alpha", lastUsedAt: 50 },
    ]);
  });

  it.each([
    ["the safe-integer ceiling", Number.MAX_SAFE_INTEGER],
    ["an unsafe integer", Number.MAX_SAFE_INTEGER + 1],
    ["a negative integer", -1],
    ["a fractional number", 100.5],
  ])(
    "Given %s as an explicit timestamp, when a command is recorded, then it falls back to now without poisoning later records",
    (_label, invalidTimestamp) => {
      vi.spyOn(Date, "now").mockReturnValue(100);
      const store = createLocalCommandHistoryStore({ storage: localStorage });
      store.record(localRepo, "older valid command", 90);

      store.record(localRepo, "invalid timestamp command", invalidTimestamp);

      expect(store.list(localRepo)).toEqual([
        { command: "invalid timestamp command", lastUsedAt: 100 },
        { command: "older valid command", lastUsedAt: 90 },
      ]);
      store.record(localRepo, "later command", 101);
      expect(store.list(localRepo)[0]).toEqual({
        command: "later command",
        lastUsedAt: 101,
      });
      expect(
        createLocalCommandHistoryStore({ storage: localStorage }).list(
          localRepo,
        ),
      ).toEqual(store.list(localRepo));
    },
  );

  it("Given exact repeats settle out of order, when an older write follows a newer one, then timestamp, MRU order, and persistence stay on the newer case-sensitive entry", () => {
    const setItem = vi.fn(localStorage.setItem.bind(localStorage));
    const store = createLocalCommandHistoryStore({
      storage: {
        getItem: localStorage.getItem.bind(localStorage),
        setItem,
      },
    });

    store.record(localRepo, "Git Status", 10);
    store.record(localRepo, "pnpm test", 20);
    store.record(localRepo, "git status", 25);
    store.record(localRepo, "Git Status", 30);

    const expectedEntries = [
      { command: "Git Status", lastUsedAt: 30 },
      { command: "git status", lastUsedAt: 25 },
      { command: "pnpm test", lastUsedAt: 20 },
    ];
    const persistedBeforeStaleWrite = localStorage.getItem(
      LOCAL_COMMAND_HISTORY_STORAGE_KEY,
    );
    const writesBeforeStaleWrite = setItem.mock.calls.length;

    store.record(localRepo, "Git Status", 5);

    expect(store.list(localRepo)).toEqual(expectedEntries);
    expect(setItem).toHaveBeenCalledTimes(writesBeforeStaleWrite);
    expect(localStorage.getItem(LOCAL_COMMAND_HISTORY_STORAGE_KEY)).toBe(
      persistedBeforeStaleWrite,
    );
    expect(
      createLocalCommandHistoryStore({ storage: localStorage }).list(localRepo),
    ).toEqual(expectedEntries);
  });

  it("Given 101 unique commands in one scope, when the last is recorded, then only the newest 100 remain in that scope", () => {
    const store = createLocalCommandHistoryStore({ storage: localStorage });

    for (let index = 0; index <= 100; index += 1) {
      store.record(localRepo, `command-${index}`, index);
    }

    const entries = store.list(localRepo);
    expect(entries).toHaveLength(100);
    expect(entries[0]).toEqual({ command: "command-100", lastUsedAt: 100 });
    expect(entries[entries.length - 1]).toEqual({
      command: "command-1",
      lastUsedAt: 1,
    });
    expect(entries.some(({ command }) => command === "command-0")).toBe(false);
  });

  it("Given 101 commands submitted oldest to newest, when records settle newest to oldest, then the submission-MRU keeps the newest 100", () => {
    const store = createLocalCommandHistoryStore({ storage: localStorage });
    const submissions = Array.from({ length: 101 }, (_, index) => ({
      command: `command-${index}`,
      lastUsedAt: index + 1,
    }));

    for (const { command, lastUsedAt } of [...submissions].reverse()) {
      store.record(localRepo, command, lastUsedAt);
    }

    const entries = store.list(localRepo);
    const commands = entries.map(({ command }) => command);
    expect
      .soft({
        newestRetained: commands.includes("command-100"),
        oldestRetained: commands.includes("command-0"),
      })
      .toEqual({ newestRetained: true, oldestRetained: false });
    expect(entries).toEqual([...submissions.slice(1)].reverse());
    expect(
      createLocalCommandHistoryStore({ storage: localStorage }).list(localRepo),
    ).toEqual(entries);
  });

  it("Given two populated scopes, when one scope is cleared, then its editor history is empty and the other scope survives reconstruction", () => {
    const store = createLocalCommandHistoryStore({ storage: localStorage });
    store.record(localRepo, "pnpm test", 10);
    store.record(remoteRepo, "make deploy", 20);

    expect(store.clear(localRepo)).toBe(true);

    expect(store.list(localRepo)).toEqual([]);
    expect(store.list(remoteRepo)).toEqual([
      { command: "make deploy", lastUsedAt: 20 },
    ]);
    const reconstructed = createLocalCommandHistoryStore({
      storage: localStorage,
    });
    expect(reconstructed.list(localRepo)).toEqual([]);
    expect(reconstructed.list(remoteRepo)).toEqual([
      { command: "make deploy", lastUsedAt: 20 },
    ]);
  });

  it("Given thousands of unseen scopes, when each is cleared and then recorded, then the later record is accepted", () => {
    vi.spyOn(Date, "now").mockReturnValue(100);
    const store = createLocalCommandHistoryStore({ storage: null });
    const unseenScopes = Array.from({ length: 2_000 }, (_, index) => ({
      deviceId: `dynamic-device-${index}`,
      cwd: `/dynamic/cwd/${index}`,
    }));

    expect(store.clear(unseenScopes[0]!)).toBe(true);
    for (const scope of unseenScopes.slice(1)) store.clear(scope);
    for (const [index, scope] of unseenScopes.entries()) {
      store.record(scope, `command-${index}`, Date.now());
    }

    expect(
      unseenScopes.every(
        (scope, index) => store.list(scope)[0]?.command === `command-${index}`,
      ),
    ).toBe(true);
  });

  it("Given a command submitted before a clear settles after it, when recorded, then the cleared scope stays deleted", () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(100);
    const store = createLocalCommandHistoryStore({ storage: localStorage });
    const submittedAt = 100;

    now.mockReturnValue(200);
    store.clear(localRepo);
    store.record(localRepo, "in flight", submittedAt);

    expect(store.list(localRepo)).toEqual([]);

    store.record(localRepo, "allowed after clear", 201);
    expect(store.list(localRepo)).toEqual([
      { command: "allowed after clear", lastUsedAt: 201 },
    ]);
  });
});

describe("local command history mutation subscriptions", () => {
  it("Given two listeners, when accepted records and repeated clears mutate scopes, then notifications are synchronous while no-op records and unsubscribed listeners stay silent", () => {
    vi.spyOn(Date, "now").mockReturnValue(0);
    const store = createLocalCommandHistoryStore({ storage: localStorage });
    const localScopeKey = deriveLocalCommandHistoryScopeKey(localRepo);
    const remoteScopeKey = deriveLocalCommandHistoryScopeKey(remoteRepo);
    const observedEntries: Array<{
      type: "record" | "clear";
      scopeKey: string;
      commands: string[];
    }> = [];
    const firstListener = vi.fn(
      (mutation: { type: "record" | "clear"; scopeKey: string }) => {
        const scope =
          mutation.scopeKey === localScopeKey ? localRepo : remoteRepo;
        observedEntries.push({
          ...mutation,
          commands: store.list(scope).map(({ command }) => command),
        });
      },
    );
    const secondListener = vi.fn();
    const unsubscribeFirst = store.subscribe(firstListener);
    const unsubscribeSecond = store.subscribe(secondListener);

    store.record(localRepo, "pnpm test", 10);
    store.record(localRepo, "pnpm test", 5);
    store.record(localRepo, "", 15);
    store.record(remoteRepo, "make deploy", 20);
    store.clear(localRepo);
    store.clear(localRepo);

    expect(observedEntries).toEqual([
      {
        type: "record",
        scopeKey: localScopeKey,
        commands: ["pnpm test"],
      },
      {
        type: "record",
        scopeKey: remoteScopeKey,
        commands: ["make deploy"],
      },
      { type: "clear", scopeKey: localScopeKey, commands: [] },
      { type: "clear", scopeKey: localScopeKey, commands: [] },
    ]);
    expect(secondListener).toHaveBeenCalledTimes(4);

    unsubscribeFirst();
    store.record(localRepo, "git status", 30);
    expect(firstListener).toHaveBeenCalledTimes(4);
    expect(secondListener).toHaveBeenCalledTimes(5);

    unsubscribeSecond();
    store.clear(remoteRepo);
    expect(secondListener).toHaveBeenCalledTimes(5);
  });
});

describe("local command history storage failures", () => {
  it("Given a persisted safe-integer-ceiling timestamp, when reconstructed and used again, then the whole history is rejected before it can poison new storage", () => {
    localStorage.setItem(
      LOCAL_COMMAND_HISTORY_STORAGE_KEY,
      JSON.stringify({
        version: 1,
        scopes: {
          [deriveLocalCommandHistoryScopeKey(localRepo)]: [
            { command: "must not partially survive", lastUsedAt: 40 },
          ],
          [deriveLocalCommandHistoryScopeKey(remoteRepo)]: [
            { command: "poisoned", lastUsedAt: Number.MAX_SAFE_INTEGER },
          ],
        },
      }),
    );
    vi.spyOn(Date, "now").mockReturnValue(100);

    const store = createLocalCommandHistoryStore({ storage: localStorage });

    expect(store.list(localRepo)).toEqual([]);
    expect(store.list(remoteRepo)).toEqual([]);

    store.record(localRepo, "safe command", 103);
    const reconstructed = createLocalCommandHistoryStore({
      storage: localStorage,
    });
    expect(reconstructed.list(localRepo)).toEqual([
      { command: "safe command", lastUsedAt: 103 },
    ]);
  });

  it.each([
    ["invalid JSON", "not-json"],
    ["unknown version", JSON.stringify({ version: 99, scopes: {} })],
    [
      "malformed entries",
      JSON.stringify({
        version: 1,
        scopes: {
          [deriveLocalCommandHistoryScopeKey(localRepo)]: [
            { command: "unsafe", lastUsedAt: "yesterday" },
          ],
        },
      }),
    ],
    [
      "a safe-integer ceiling timestamp beside otherwise valid history",
      JSON.stringify({
        version: 1,
        scopes: {
          [deriveLocalCommandHistoryScopeKey(localRepo)]: [
            { command: "must not partially survive", lastUsedAt: 40 },
          ],
          [deriveLocalCommandHistoryScopeKey(remoteRepo)]: [
            { command: "poisoned", lastUsedAt: Number.MAX_SAFE_INTEGER },
          ],
        },
      }),
    ],
  ])(
    "Given %s, when read and followed by a valid record, then callers see empty history and storage is rebuilt",
    (_label, raw) => {
      localStorage.setItem(LOCAL_COMMAND_HISTORY_STORAGE_KEY, raw);
      const store = createLocalCommandHistoryStore({ storage: localStorage });

      expect(store.list(localRepo)).toEqual([]);
      expect(() => store.record(localRepo, "safe command", 50)).not.toThrow();

      const reconstructed = createLocalCommandHistoryStore({
        storage: localStorage,
      });
      expect(reconstructed.list(localRepo)).toEqual([
        { command: "safe command", lastUsedAt: 50 },
      ]);
    },
  );

  it("Given malformed scope metadata, when followed by a valid record, then storage is rebuilt without retaining out-of-schema history", () => {
    localStorage.setItem(
      LOCAL_COMMAND_HISTORY_STORAGE_KEY,
      JSON.stringify({
        version: 1,
        scopes: {
          "not-a-device-cwd-scope": [
            { command: "should not survive", lastUsedAt: 10 },
          ],
        },
      }),
    );
    const store = createLocalCommandHistoryStore({ storage: localStorage });

    store.record(localRepo, "safe command", 50);

    expect(
      JSON.parse(localStorage.getItem(LOCAL_COMMAND_HISTORY_STORAGE_KEY)!),
    ).toEqual({
      version: 1,
      scopes: {
        [deriveLocalCommandHistoryScopeKey(localRepo)]: [
          { command: "safe command", lastUsedAt: 50 },
        ],
      },
    });
  });

  it("Given unavailable reads and failing writes, when commands are recorded, then callers never receive an error and current-run memory remains usable", () => {
    const failingStorage = {
      getItem(): string | null {
        throw new Error("private mode");
      },
      setItem(): void {
        throw new Error("quota exceeded");
      },
    };

    expect(() =>
      createLocalCommandHistoryStore({ storage: failingStorage }),
    ).not.toThrow();
    const store = createLocalCommandHistoryStore({ storage: failingStorage });
    expect(() => store.record(localRepo, "pnpm test", 10)).not.toThrow();
    expect(store.list(localRepo)).toEqual([
      { command: "pnpm test", lastUsedAt: 10 },
    ]);
  });

  it("Given durable scoped history, when deletion persistence fails, then clear rolls back without a mutation notification", () => {
    const seeded = createLocalCommandHistoryStore({ storage: localStorage });
    seeded.record(localRepo, "sensitive command", 10);
    const rawBeforeClear = localStorage.getItem(
      LOCAL_COMMAND_HISTORY_STORAGE_KEY,
    );
    const setItem = vi.fn(() => {
      throw new Error("private storage path quota failure");
    });
    const store = createLocalCommandHistoryStore({
      storage: {
        getItem: localStorage.getItem.bind(localStorage),
        setItem,
      },
    });
    const listener = vi.fn();
    store.subscribe(listener);

    expect(store.clear(localRepo)).toBe(false);

    expect(store.list(localRepo)).toEqual([
      { command: "sensitive command", lastUsedAt: 10 },
    ]);
    expect(listener).not.toHaveBeenCalled();
    expect(localStorage.getItem(LOCAL_COMMAND_HISTORY_STORAGE_KEY)).toBe(
      rawBeforeClear,
    );
    expect(
      createLocalCommandHistoryStore({ storage: localStorage }).list(localRepo),
    ).toEqual([{ command: "sensitive command", lastUsedAt: 10 }]);

    store.record(localRepo, "record still valid", 11);
    expect(store.list(localRepo)).toEqual([
      { command: "record still valid", lastUsedAt: 11 },
      { command: "sensitive command", lastUsedAt: 10 },
    ]);
  });
});
