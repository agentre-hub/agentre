import { sameChord } from "./conflict";
import { REGISTRY, getDefaultBindings, getDef } from "./registry";
import type { KeyChord, ModifierKind } from "./types";

export const STORAGE_KEY = "agentre.shortcuts";
const CURRENT_VERSION = 1;

const VALID_KEY_RE = /^[A-Z0-9,]$/;

type StoredShape = {
  version?: unknown;
  bindings?: unknown;
};

type ShortcutsState = {
  bindings: Map<string, KeyChord>;
};

function parseChord(raw: unknown): KeyChord | null {
  if (!raw || typeof raw !== "object") return null;
  const o = raw as { mod?: unknown; key?: unknown };
  const mod = o.mod;
  const key = o.key;
  if (mod !== "primary" && mod !== "primary-shift" && mod !== "primary-alt") {
    return null;
  }
  if (typeof key !== "string") return null;
  const upper = key.length === 1 ? key.toUpperCase() : key;
  if (!VALID_KEY_RE.test(upper)) return null;
  return { mod: mod as ModifierKind, key: upper };
}

export function loadShortcutsState(): ShortcutsState {
  const defaults = getDefaultBindings();

  let raw: string | null;
  try {
    raw = localStorage.getItem(STORAGE_KEY);
  } catch {
    return { bindings: defaults };
  }
  if (!raw) {
    return { bindings: defaults };
  }

  let parsed: StoredShape;
  try {
    parsed = JSON.parse(raw) as StoredShape;
  } catch {
    console.warn("[shortcuts] localStorage payload is not valid JSON; reset.");
    return { bindings: defaults };
  }

  if (parsed.version !== CURRENT_VERSION) {
    console.warn(
      `[shortcuts] unsupported version ${String(parsed.version)}; using defaults.`,
    );
    return { bindings: defaults };
  }

  const bindings = new Map(defaults);
  const overrides = parsed.bindings;
  if (overrides && typeof overrides === "object") {
    for (const [id, rawChord] of Object.entries(overrides)) {
      if (!getDef(id)) {
        console.warn(`[shortcuts] unknown action id "${id}"; dropping.`);
        continue;
      }
      const chord = parseChord(rawChord);
      if (!chord) {
        console.warn(`[shortcuts] invalid chord for "${id}"; using default.`);
        continue;
      }
      bindings.set(id, chord);
    }
  }

  return { bindings };
}

export function saveShortcutsState(state: ShortcutsState): void {
  const overrides: Record<string, KeyChord> = {};
  for (const def of REGISTRY) {
    const current = state.bindings.get(def.id);
    if (!current) continue;
    if (!sameChord(current, def.defaultBinding)) {
      overrides[def.id] = current;
    }
  }

  const payload = {
    version: CURRENT_VERSION,
    bindings: overrides,
  };

  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(payload));
  } catch {
    console.warn("[shortcuts] failed to persist to localStorage.");
  }
}

export function clearShortcutsState(): void {
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    // ignore
  }
}
