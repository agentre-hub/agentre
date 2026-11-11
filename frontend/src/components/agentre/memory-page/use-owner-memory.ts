import * as React from "react";

import type { MemoryOwnerRef } from "@agentre-hub/agentre-ui";

import {
  MemoryListByOwner,
  MemoryVisible,
} from "../../../../wailsjs/go/app/App";
import type { memory_svc } from "../../../../wailsjs/go/models";

type State =
  | { status: "loading" }
  | { status: "error" }
  | {
      status: "ready";
      list: memory_svc.OwnerList;
      /** agent 归属才有：它的会话看得到的其他层（不含它自己）。 */
      inherited: memory_svc.VisibleLayer[];
    };

/**
 * 右侧选中的那个归属：三组条目、预算用量、agent 的记忆开关，以及 agent 的「继承的」层。
 *
 * 组件按归属挂载（父组件以 owner 为 key），所以这里不处理「中途换了归属」：一次加载
 * 只属于一个归属。`reload` 是后台重拉，不回到加载态；加载出来过之后的失败保留旧内容。
 */
export function useOwnerMemory({ kind, id }: MemoryOwnerRef) {
  const [state, setState] = React.useState<State>({ status: "loading" });
  const loaded = React.useRef(false);
  const seq = React.useRef(0);

  const reload = React.useCallback(async () => {
    const mine = ++seq.current;
    try {
      const [list, visible] = await Promise.all([
        MemoryListByOwner({ kind, id }),
        kind === "agent" ? MemoryVisible(id, 0) : Promise.resolve(null),
      ]);
      // 晚到的旧结果不盖掉更新的那一次。
      if (mine !== seq.current) return;
      loaded.current = true;
      setState({
        status: "ready",
        list,
        inherited: (visible?.layers ?? []).filter(
          (layer) => !(layer.owner.kind === kind && layer.owner.id === id),
        ),
      });
    } catch {
      if (mine !== seq.current || loaded.current) return;
      setState({ status: "error" });
    }
  }, [kind, id]);

  const retry = React.useCallback(() => {
    setState({ status: "loading" });
    void reload();
  }, [reload]);

  React.useEffect(() => {
    void reload();
  }, [reload]);

  return { state, reload, retry };
}
