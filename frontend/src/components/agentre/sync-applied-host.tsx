// sync-applied-host.tsx —— 把「多端同步刚落地了东西」接到左栏的刷新上。
//
// 项目树没有任何推送通道：全仓库的 EventsOn 里没有一条是项目变更，此前靠已删除的
// 项目页那条 1 秒轮询兜着。轮询随单一会话索引一起删掉之后，另一台设备同步过来的
// 项目会一直不出现，直到用户碰巧做了点别的事（sync-client 的 e2e 冒烟因此变红）。
//
// 后端在一轮下行**真的落地了东西**时才发这条事件（空转的轮次不发），所以这里可以
// 直接刷 —— 30 秒一次的轮询不会变成 30 秒一次的白拉。
import * as React from "react";

import { reloadSidebarSources } from "@/stores/sidebar-reload";

import { EventsOn, EventsOff } from "../../../wailsjs/runtime/runtime";

/** 与 `sync_svc.AppliedEvent` 同名；那边是常量，这边只有这一处引用。 */
const SYNC_APPLIED_EVENT = "sync:applied";

export function SyncAppliedHost() {
  React.useEffect(() => {
    // 载荷是落地的对象类型（["project", "agent", …]）。今天左栏的三个来源是一起
    // 刷的，所以不按类型分流；真要分流也该先有「哪一类对应哪一份数据」的映射，
    // 而不是在这里 if 一串字符串。
    EventsOn(SYNC_APPLIED_EVENT, () => {
      reloadSidebarSources();
    });
    return () => EventsOff(SYNC_APPLIED_EVENT);
  }, []);

  return null;
}
