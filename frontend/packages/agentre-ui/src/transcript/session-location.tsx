import * as React from "react";

/**
 * 这棵转录渲染的会话在哪台机器上。宿主知道（桌面端看会话的 deviceID），包不知道；
 * 「文件不在本机」时给什么原因取决于它：远端会话的文件本来就在另一台机器上，本机
 * 会话则只可能是被移走 / 删掉了。undefined = 宿主没说，提示不猜原因。
 */
export type SessionLocation = "local" | "remote";

const SessionLocationContext = React.createContext<SessionLocation | undefined>(
  undefined,
);

export function SessionLocationProvider({
  location,
  children,
}: {
  location: SessionLocation | undefined;
  children: React.ReactNode;
}) {
  return (
    <SessionLocationContext.Provider value={location}>
      {children}
    </SessionLocationContext.Provider>
  );
}

export function useSessionLocation(): SessionLocation | undefined {
  return React.useContext(SessionLocationContext);
}
