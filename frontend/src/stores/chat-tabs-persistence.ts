import type { ChatTab } from "./chat-tabs-store";

export const CHAT_TABS_STORAGE_KEY = "agentre.chatTabs";

type PersistMeta =
  | { kind: "session"; sessionId: number }
  | {
      kind: "terminal";
      projectId: number;
      deviceId: string;
      terminalId: string;
    };

type PersistedTabV2 = {
  id: string;
  meta: PersistMeta;
  isPinned: boolean;
  pinAt: number;
  openedAt: number;
  title?: string;
};
type PersistedV2 = {
  v: 2;
  tabs: PersistedTabV2[];
  activeTabId: string | null;
};

function getStorage(): Storage | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function persistable(t: ChatTab): PersistMeta | null {
  if (t.meta.kind === "session" && !t.isPreview) return t.meta;
  if (t.meta.kind === "terminal") return t.meta.attach ? null : t.meta;
  return null;
}

export function writePersistedTabs(
  tabs: ChatTab[],
  activeTabId: string | null,
): void {
  const storage = getStorage();
  if (!storage) return;
  const out: PersistedV2 = {
    v: 2,
    tabs: tabs.flatMap((t) => {
      const meta = persistable(t);
      if (!meta) return [];
      return [
        {
          id: t.id,
          meta,
          isPinned: t.isPinned,
          pinAt: t.pinAt,
          openedAt: t.openedAt,
          title: t.title,
        },
      ];
    }),
    activeTabId,
  };
  try {
    storage.setItem(CHAT_TABS_STORAGE_KEY, JSON.stringify(out));
  } catch {
    /* quota / private mode */
  }
}

function toChatTab(
  id: string,
  meta: PersistMeta,
  isPinned: boolean,
  pinAt: number,
  openedAt: number,
  title?: string,
): ChatTab {
  return { id, meta, isPreview: false, isPinned, pinAt, openedAt, title };
}

export function readPersistedTabs(): {
  tabs: ChatTab[];
  activeTabId: string | null;
} | null {
  const storage = getStorage();
  if (!storage) return null;
  let raw: string | null;
  try {
    raw = storage.getItem(CHAT_TABS_STORAGE_KEY);
  } catch {
    return null;
  }
  if (!raw) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!parsed || typeof parsed !== "object") return null;
  const v = (parsed as { v?: number }).v;

  if (v === 2) {
    const p = parsed as PersistedV2;
    const tabs: ChatTab[] = [];
    for (const r of p.tabs ?? []) {
      const m = r.meta;
      if (m?.kind === "session" && typeof m.sessionId === "number") {
        tabs.push(
          toChatTab(
            r.id,
            { kind: "session", sessionId: m.sessionId },
            r.isPinned,
            r.pinAt,
            r.openedAt,
            r.title,
          ),
        );
      } else if (
        m?.kind === "terminal" &&
        typeof m.projectId === "number" &&
        typeof m.deviceId === "string" &&
        typeof m.terminalId === "string"
      ) {
        tabs.push(
          toChatTab(
            r.id,
            {
              kind: "terminal",
              projectId: m.projectId,
              deviceId: m.deviceId,
              terminalId: m.terminalId,
            },
            r.isPinned,
            r.pinAt,
            r.openedAt,
            r.title,
          ),
        );
      }
    }
    return { tabs, activeTabId: p.activeTabId };
  }

  return null;
}
