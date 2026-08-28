import * as React from "react";

import type { SlashCommand } from "@agentre-hub/agentre-ui";

import { ListAgentSkillCommands } from "@/../wailsjs/go/app/App";

import {
  skillCommandPrefix,
  skillCommandsFromCatalog,
  type SkillCommandSource,
} from "./registry";

// useAgentSkillCommands 在 composer 挂载时读当前 agent + cwd 的可调用 Skill 目录。
// service 统一合并有效 plugin 与 backend-native standalone/system Skill。发现失败
// 时软降级为空列表,不阻断用户输入。
export function useAgentSkillCommands(
  agentId: number,
  backendType: string,
  cwd = "",
): SlashCommand[] {
  const [commands, setCommands] = React.useState<SlashCommand[]>([]);

  React.useEffect(() => {
    let canceled = false;
    if (agentId <= 0 || !skillCommandPrefix(backendType)) {
      setCommands([]);
      return () => {
        canceled = true;
      };
    }

    void ListAgentSkillCommands(agentId, cwd)
      .then((catalog) => {
        if (canceled) return;
        setCommands(
          skillCommandsFromCatalog(
            backendType,
            (catalog?.commands ?? []) as SkillCommandSource[],
          ),
        );
      })
      .catch(() => {
        if (!canceled) setCommands([]);
      });

    return () => {
      canceled = true;
    };
  }, [agentId, backendType, cwd]);

  return commands;
}
