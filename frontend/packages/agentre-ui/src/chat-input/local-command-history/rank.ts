import { scoreSuggestion } from "../../lib/suggestion-score";

import type { LocalCommandHistoryEntry } from "./types";

export function rankLocalCommandHistory(
  entries: readonly LocalCommandHistoryEntry[],
  query: string,
): LocalCommandHistoryEntry[] {
  if (!query.trim()) {
    return [...entries].sort(
      (left, right) => right.lastUsedAt - left.lastUsedAt,
    );
  }

  return entries
    .map((entry, sourceIndex) => ({
      entry,
      sourceIndex,
      score: scoreSuggestion({
        query,
        title: entry.command,
        allowWhitespaceInOrder: true,
      }),
    }))
    .filter(({ score }) => score > 0)
    .sort(
      (left, right) =>
        right.score - left.score ||
        right.entry.lastUsedAt - left.entry.lastUsedAt ||
        left.sourceIndex - right.sourceIndex,
    )
    .map(({ entry }) => entry);
}
