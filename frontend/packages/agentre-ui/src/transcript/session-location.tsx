import * as React from "react";

import type { LinkRoot } from "../lib/link-classify";

/**
 * 这棵转录渲染的会话在哪台机器上。宿主知道（桌面端看会话的 deviceID），包不知道；
 * 「文件不在本机」时给什么原因取决于它：远端会话的文件本来就在另一台机器上，本机
 * 会话则只可能是被移走 / 删掉了。undefined = 宿主没说，提示不猜原因。
 */
export type SessionLocation = "local" | "remote";

const SessionLocationContext = React.createContext<SessionLocation | undefined>(
  undefined,
);

/**
 * 会话已认领的工作根（spec 2026-11-24-chat-right-toolwindows「文件链接」）。宿主给了
 * 它，转录里的链接才按「AI 当时所在目录」补全、按最深的根归属；不给（agentre-server
 * 今天就不给）时链接照旧按会话工作目录解析，一个字节都不变。
 */
const SessionRootsContext = React.createContext<
  readonly LinkRoot[] | undefined
>(undefined);

export function SessionLocationProvider({
  location,
  roots,
  children,
}: {
  location: SessionLocation | undefined;
  /** 会话已认领的工作根（主根标 `primary`）。可选，见 `SessionRootsContext`。 */
  roots?: readonly LinkRoot[];
  children: React.ReactNode;
}) {
  return (
    <SessionLocationContext.Provider value={location}>
      <SessionRootsContext.Provider value={roots}>
        {children}
      </SessionRootsContext.Provider>
    </SessionLocationContext.Provider>
  );
}

export function useSessionLocation(): SessionLocation | undefined {
  return React.useContext(SessionLocationContext);
}

export function useSessionRoots(): readonly LinkRoot[] | undefined {
  return React.useContext(SessionRootsContext);
}

/**
 * 转录里某一项产生时 AI 所在的目录（工具块用自己的记录，正文用同一轮里它之前最近
 * 一个工具块的记录，再没有就是这一轮开始时的位置）。行视图按项挂上，链接据此补全
 * 相对路径；undefined = 没有记录，回落到会话工作目录。
 */
const ItemCwdContext = React.createContext<string | undefined>(undefined);

export function ItemCwdProvider({
  cwd,
  children,
}: {
  cwd: string | undefined;
  children: React.ReactNode;
}) {
  return (
    <ItemCwdContext.Provider value={cwd}>{children}</ItemCwdContext.Provider>
  );
}

export function useItemCwd(): string | undefined {
  return React.useContext(ItemCwdContext);
}
