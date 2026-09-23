// sync-applied-host.tsx —— 把「多端同步刚落地了东西」与「本机刚写完了这五类资源」
// 接到左栏的刷新上。
//
// 项目树没有任何推送通道：全仓库的 EventsOn 里没有一条是项目变更，此前靠已删除的
// 项目页那条 1 秒轮询兜着。轮询随单一会话索引一起删掉之后，另一台设备同步过来的
// 项目会一直不出现，直到用户碰巧做了点别的事（sync-client 的 e2e 冒烟因此变红）。
//
// 后端在一轮下行**真的落地了东西**时才发 sync:applied（空转的轮次不发），所以这里
// 可以直接刷 —— 30 秒一次的轮询不会变成 30 秒一次的白拉。config:changed 是本机
// Wails / orgtool / ctl 三条写入路径写完这五类资源后发的另一条事件，与登录态无关
// （docs/specs/2026-09-22-agrctl-resource-management.md「Real-time refresh」）。
import * as React from "react";

import { reloadSidebarSources } from "@/stores/sidebar-reload";

import { EventsOn, EventsOff } from "../../../wailsjs/runtime/runtime";

/** 与 `sync_svc.AppliedEvent` 同名；那边是常量，这边只有这一处引用。 */
const SYNC_APPLIED_EVENT = "sync:applied";
/** 与 `sync_svc.ConfigChangedEvent` 同名；那边是常量，这边只有这一处引用。 */
const CONFIG_CHANGED_EVENT = "config:changed";

export function SyncAppliedHost() {
  React.useEffect(() => {
    // 载荷是变更涉及的对象类型（["project", "agent", …]）。今天左栏的三个来源是
    // 一起刷的，所以不按类型分流；真要分流也该先有「哪一类对应哪一份数据」的映射，
    // 而不是在这里 if 一串字符串。
    EventsOn(SYNC_APPLIED_EVENT, () => {
      reloadSidebarSources();
    });
    EventsOn(CONFIG_CHANGED_EVENT, () => {
      reloadSidebarSources();
    });
    return () => {
      EventsOff(SYNC_APPLIED_EVENT);
      EventsOff(CONFIG_CHANGED_EVENT);
    };
  }, []);

  return null;
}
