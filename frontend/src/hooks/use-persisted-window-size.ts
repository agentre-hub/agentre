// 窗口尺寸的持久化:读写 localStorage 里那一条、夹到允许范围内、把变化回写到 Wails。
//
// 读/写/夹三个助手与这个 hook 是同一件事的三段,分开放只会让人来回跳文件。

import { useLayoutEffect } from "react";

import {
  WindowCenter,
  WindowGetSize,
  WindowIsFullscreen,
  WindowSetSize,
  WindowShow,
} from "../../wailsjs/runtime/runtime";

import {
  hasWailsRuntime,
  getBrowserStorage,
  type RuntimeMode,
} from "../lib/platform";

export type StoredWindowSize = {
  height: number;
  width: number;
};

export const windowSizeStorageKey = "agentre.windowSize";

export const windowSizeSaveDelayMs = 250;

export const minWindowWidth = 860;

export const minWindowHeight = 640;

export const maxWindowWidth = 4096;

export const maxWindowHeight = 3072;

export function clampWindowDimension(value: number, min: number, max: number) {
  return Math.min(Math.max(Math.round(value), min), max);
}

export function numberFromStorage(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

export function normaliseWindowSize(value: unknown): StoredWindowSize | null {
  if (!value || typeof value !== "object") {
    return null;
  }

  const record = value as Record<string, unknown>;
  const rawWidth = numberFromStorage(record.width ?? record.w);
  const rawHeight = numberFromStorage(record.height ?? record.h);

  if (rawWidth === null || rawHeight === null) {
    return null;
  }

  if (rawWidth < minWindowWidth || rawHeight < minWindowHeight) {
    return null;
  }

  return {
    height: clampWindowDimension(rawHeight, minWindowHeight, maxWindowHeight),
    width: clampWindowDimension(rawWidth, minWindowWidth, maxWindowWidth),
  };
}

export function readStoredWindowSize(): StoredWindowSize | null {
  const storage = getBrowserStorage();

  if (typeof storage?.getItem !== "function") {
    return null;
  }

  try {
    const value = storage.getItem(windowSizeStorageKey);

    return value ? normaliseWindowSize(JSON.parse(value)) : null;
  } catch {
    return null;
  }
}

export function writeStoredWindowSize(size: StoredWindowSize) {
  const storage = getBrowserStorage();
  const normalised = normaliseWindowSize(size);

  if (!normalised || typeof storage?.setItem !== "function") {
    return;
  }

  try {
    storage.setItem(windowSizeStorageKey, JSON.stringify(normalised));
  } catch {
    // Some embedded previews may block localStorage.
  }
}

export function usePersistedWindowSize(runtimeMode: RuntimeMode) {
  useLayoutEffect(() => {
    if (runtimeMode !== "interactive" || !hasWailsRuntime()) {
      return;
    }

    const storedWindowSize = readStoredWindowSize();

    if (storedWindowSize) {
      try {
        WindowSetSize(storedWindowSize.width, storedWindowSize.height);
      } catch {
        // Browser previews and test doubles may not expose every Wails API.
      }
    }

    try {
      WindowCenter();
    } catch {
      // Browser previews and test doubles may not expose every Wails API.
    }

    try {
      WindowShow();
    } catch {
      // Browser previews and test doubles may not expose every Wails API.
    }

    let mounted = true;
    let saveTimer: number | undefined;

    const saveCurrentWindowSize = async () => {
      try {
        const isFullscreen = await WindowIsFullscreen();

        if (!mounted || isFullscreen) {
          return;
        }

        const size = await WindowGetSize();

        if (mounted) {
          writeStoredWindowSize({ height: size.h, width: size.w });
        }
      } catch {
        // Wails runtime calls can reject during startup/shutdown.
      }
    };

    const scheduleSave = () => {
      if (saveTimer !== undefined) {
        window.clearTimeout(saveTimer);
      }

      saveTimer = window.setTimeout(() => {
        saveTimer = undefined;
        void saveCurrentWindowSize();
      }, windowSizeSaveDelayMs);
    };

    const flushSave = () => {
      if (saveTimer !== undefined) {
        window.clearTimeout(saveTimer);
        saveTimer = undefined;
      }

      void saveCurrentWindowSize();
    };

    window.addEventListener("resize", scheduleSave);
    window.addEventListener("beforeunload", flushSave);
    window.addEventListener("pagehide", flushSave);

    return () => {
      mounted = false;

      if (saveTimer !== undefined) {
        window.clearTimeout(saveTimer);
      }

      window.removeEventListener("resize", scheduleSave);
      window.removeEventListener("beforeunload", flushSave);
      window.removeEventListener("pagehide", flushSave);
    };
  }, [runtimeMode]);
}
