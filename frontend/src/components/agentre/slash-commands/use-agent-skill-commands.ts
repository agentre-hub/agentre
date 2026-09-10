import * as React from "react";

import {
  skillCommandPrefix,
  type SkillCommandSource,
} from "@agentre-hub/agentre-ui";

import { ListAgentSkillCommands } from "@/../wailsjs/go/app/App";

// useAgentSkillCommands 在 composer 挂载时读当前 agent + cwd 的可调用 Skill 目录。
// service 统一合并有效 plugin 与 backend-native standalone/system Skill;执行目标
// 落在别的机器上时,整份清单由那台机器答(skills.commands RPC)。发现失败时软降级
// 为空列表,不阻断用户输入。
//
// 它交出的是**目录**而不是命令:「目录怎么变成命令」(前缀、去重、按 backend 解析)
// 两端同一件事,已经收在共享包里(`skillCommandsFromCatalog` / `useSlashCommands`)。
// 这里只管「问哪台机器要」——那才是宿主独有的部分。
export function useAgentSkillCommands(
  agentId: number,
  backendType: string,
  cwd = "",
): SkillCommandSource[] {
  const [commands, setCommands] = React.useState<SkillCommandSource[]>([]);

  React.useEffect(() => {
    let canceled = false;
    // 没有 skill 这一说的 backend 连问都不必问一次。
    if (agentId <= 0 || !skillCommandPrefix(backendType)) {
      setCommands([]);
      return () => {
        canceled = true;
      };
    }

    void ListAgentSkillCommands(agentId, cwd)
      .then((catalog) => {
        if (canceled) return;
        setCommands((catalog?.commands ?? []) as SkillCommandSource[]);
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
