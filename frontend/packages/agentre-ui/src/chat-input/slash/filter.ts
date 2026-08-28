import { scoreSuggestion } from "../../lib/suggestion-score";

import type { SlashCommand } from "./types";

// filterByQuery 隔离当前 trigger,再按共享相关度评分。
// 空 query 全部保留且维持源顺序；同分候选也维持源顺序。
export function filterByQuery(
  commands: SlashCommand[],
  query: string,
  trigger?: "/" | "$",
): SlashCommand[] {
  return commands
    .map((command, sourceIndex) => ({
      command,
      sourceIndex,
      score:
        !trigger || command.trigger === trigger
          ? scoreSuggestion({
              query,
              title: command.name,
              subtitle: command.description,
            })
          : 0,
    }))
    .filter(({ score }) => score > 0)
    .sort(
      (left, right) =>
        right.score - left.score || left.sourceIndex - right.sourceIndex,
    )
    .map(({ command }) => command);
}
