import * as React from "react";

import type { TranscriptLocalCommand } from "../../dto";
import type { LocalCommandsAccess, LocalCommandView } from "../access";
import { isLocalCommandCollapsed } from "../collapsed";

/**
 * 本地命令接缝的**替身宿主**。
 *
 * 桌面端那侧是 zustand（属于宿主，包按定义碰不到），所以包的用例要自己扮演一个
 * 最小宿主。这个替身刻意实现得和桌面端适配层**同形**，而不是随手 stub：
 *
 *   - `useLocalCommand` 用 `useSyncExternalStore` + **投影缓存**：投影里的字段
 *     没变就返回同一个引用。桌面端用 `useShallow` 做同一件事。若这里图省事每次
 *     新建对象返回，输出追加就会推着卡片重渲染 —— 那样写出来的用例会**假绿**：
 *     它证明不了"输出不经 React"，因为整棵子树本来就在每 chunk 重渲染。
 *   - `subscribeOutput` 按订阅持有游标：订阅当场同步 seed 一次积压，之后只发增量。
 *
 * 放在 `__testing__/` 而不是 `.test.tsx`：它是被用例 import 的工具而非用例本身。
 */

type Entry = TranscriptLocalCommand;

function project(entry: Entry): LocalCommandView {
  const { output, ...rest } = entry;

  return { ...rest, hasOutput: output.length > 0 };
}

function sameView(a: LocalCommandView, b: LocalCommandView): boolean {
  return (
    a.id === b.id &&
    a.command === b.command &&
    a.status === b.status &&
    a.exitCode === b.exitCode &&
    a.createdAt === b.createdAt &&
    a.finishedAt === b.finishedAt &&
    a.expanded === b.expanded &&
    a.hasOutput === b.hasOutput
  );
}

export interface FakeLocalCommands {
  /** 传给 `<LocalCommandsProvider access={…}>` 的实现。模块级稳定引用。 */
  access: LocalCommandsAccess;
  /** 用例侧的写入口（扮演宿主收到 PTY 事件）。 */
  start(entry: Partial<Entry> & Pick<Entry, "id">): void;
  appendOutput(id: string, chunk: string): void;
  finish(id: string, status: Entry["status"], exitCode?: number): void;
  get(id: string): Entry | undefined;
}

export function createFakeLocalCommands(): FakeLocalCommands {
  const entries = new Map<string, Entry>();
  const views = new Map<string, LocalCommandView>();
  const listeners = new Set<() => void>();

  const notify = () => {
    for (const listener of [...listeners]) listener();
  };

  // 投影缓存：只有投影字段真的变了才换引用（桌面端 useShallow 的等价物）。
  const viewOf = (id: string): LocalCommandView | undefined => {
    const entry = entries.get(id);
    if (!entry) {
      views.delete(id);
      return undefined;
    }
    const next = project(entry);
    const cached = views.get(id);
    if (cached && sameView(cached, next)) return cached;
    views.set(id, next);
    return next;
  };

  const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => listeners.delete(listener);
  };

  const access: LocalCommandsAccess = {
    useLocalCommand(id) {
      return React.useSyncExternalStore(
        subscribe,
        () => viewOf(id),
        () => viewOf(id),
      );
    },

    subscribeOutput(id, onAppend) {
      let written = 0;
      const flush = () => {
        const entry = entries.get(id);
        if (!entry || entry.output.length <= written) return;
        onAppend(entry.output.slice(written));
        written = entry.output.length;
      };
      flush(); // 同步 seed。
      return subscribe(flush);
    },

    toggleExpanded(id) {
      const entry = entries.get(id);
      if (!entry) return;
      entries.set(id, {
        ...entry,
        expanded: isLocalCommandCollapsed(entry),
      });
      notify();
    },

    remove(id) {
      if (!entries.delete(id)) return;
      notify();
    },
  };

  return {
    access,

    start(entry) {
      entries.set(entry.id, {
        sessionId: 1,
        command: "echo hi",
        createdAt: 1,
        status: "running",
        output: "",
        ...entry,
      });
      notify();
    },

    appendOutput(id, chunk) {
      const entry = entries.get(id);
      if (!entry) return;
      entries.set(id, { ...entry, output: entry.output + chunk });
      notify();
    },

    finish(id, status, exitCode) {
      const entry = entries.get(id);
      if (!entry) return;
      entries.set(id, { ...entry, status, exitCode, finishedAt: 2200 });
      notify();
    },

    get(id) {
      return entries.get(id);
    },
  };
}
