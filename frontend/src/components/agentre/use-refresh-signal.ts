// useRefreshSignal —— 把「本机写入成功了这几类资源」(`config:changed`) 与「多端
// 同步落地了东西」(`sync:applied`) 合成一个单调递增的信号。
//
// 两条事件的载荷都是变更涉及的资源类型（["project", ...]），但消费它的宿主
// （@agentre-hub/agentre-ui 的 EngineSettingsPorts 面板）今天不按类型分流刷新，
// 它们的 `refreshSignal` 端口只关心「变没变」，因此这里不透传 kinds，只计数。
// 真要按类型分流，应该先有「哪一类对应哪一份数据」的映射，而不是在这里搭一层
// 过滤（同 sync-applied-host.tsx 的既有取舍）。
import * as React from "react";

import { EventsOn } from "../../../wailsjs/runtime/runtime";

const REFRESH_EVENTS = ["config:changed", "sync:applied"] as const;

export function useRefreshSignal(): number {
  const [signal, setSignal] = React.useState(0);

  React.useEffect(() => {
    const offs = REFRESH_EVENTS.map((name) =>
      EventsOn(name, () => setSignal((s) => s + 1)),
    );
    return () => {
      for (const off of offs) off();
    };
  }, []);

  return signal;
}
