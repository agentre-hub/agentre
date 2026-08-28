import { scoreSuggestion } from "../../lib/suggestion-score";

import type { MentionItem, MentionSources } from "./types";

function rankGroup(items: MentionItem[], query: string): MentionItem[] {
  return items
    .map((item, sourceIndex) => ({
      item,
      sourceIndex,
      score: scoreSuggestion({
        query,
        title: item.label,
        subtitle: item.kind === "project" ? item.path : undefined,
      }),
    }))
    .filter(({ score }) => score > 0)
    .sort(
      (left, right) =>
        right.score - left.score || left.sourceIndex - right.sourceIndex,
    )
    .map(({ item }) => item);
}

export function rankMentionItems(
  sources: MentionSources,
  query: string,
): MentionItem[] {
  return [
    ...rankGroup(sources.agents, query),
    ...rankGroup(sources.projects, query),
  ];
}
