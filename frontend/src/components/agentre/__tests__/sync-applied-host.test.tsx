// 另一台设备同步过来的东西要自己出现在左栏。
//
// 项目树没有任何推送通道，此前全靠已删除的项目页那条 1 秒轮询兜着；轮询随单一
// 会话索引一起删掉之后，同步下来的项目会一直不出现，直到用户碰巧做了点别的事。
// e2e 的 sync-client 冒烟就是这么红的。
import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const handlers = new Map<string, (payload: unknown) => void>();
// unsubscribed：本组件自己那条订阅被退掉；offAll：EventsOff(name) 把这个事件名下
// **所有**组件的订阅一并清掉（组织页、设置面板也订阅了这两条事件）。
const unsubscribed: string[] = [];
const offAll: string[] = [];

vi.mock("../../../../wailsjs/runtime/runtime", async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  EventsOn: (name: string, handler: (payload: unknown) => void) => {
    handlers.set(name, handler);
    return () => unsubscribed.push(name);
  },
  EventsOff: (name: string) => {
    offAll.push(name);
  },
}));

const reloadSidebarSources = vi.fn();
vi.mock("@/stores/sidebar-reload", () => ({
  reloadSidebarSources: () => reloadSidebarSources(),
}));

import { SyncAppliedHost } from "../sync-applied-host";

describe("SyncAppliedHost", () => {
  beforeEach(() => {
    handlers.clear();
    unsubscribed.length = 0;
    offAll.length = 0;
    reloadSidebarSources.mockClear();
  });

  it("Given the host is mounted, When a sync round reports it landed something, Then the sidebar sources reload without waiting for the user to do anything", () => {
    render(<SyncAppliedHost />);

    handlers.get("sync:applied")?.(["project"]);

    expect(reloadSidebarSources).toHaveBeenCalledTimes(1);
  });

  it("Given the host unmounts, When it goes away, Then it takes only its own subscription with it", () => {
    const view = render(<SyncAppliedHost />);

    view.unmount();

    expect(unsubscribed).toContain("sync:applied");
    expect(offAll).toEqual([]);
  });

  it("Given the host is mounted, When a local write reports config:changed, Then the sidebar sources reload too", () => {
    render(<SyncAppliedHost />);

    handlers.get("config:changed")?.(["project"]);

    expect(reloadSidebarSources).toHaveBeenCalledTimes(1);
  });

  it("Given the host unmounts, When it goes away, Then it takes the config:changed subscription with it too", () => {
    const view = render(<SyncAppliedHost />);

    view.unmount();

    expect(unsubscribed).toContain("config:changed");
    expect(offAll).toEqual([]);
  });
});
