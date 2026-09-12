import { describe, expect, it } from "vitest";

import {
  agentredAnsweredMethods,
  desktopAnsweredMethods,
  rpcMethods,
} from "../index";

// 这两个数组是给**消费方**用来对账的:agentre-server 的控制台按设备指纹拨号,而设备
// 表里 desktop 与 agentred 混在一起,于是「这个方法发给这台机器打不打得通」是它自己
// 判断不了的事。产物由 agentre 仓的 wireinbound.Contract() 生成 —— 这里守的是它作为
// 一份 TS 契约必须成立的那几条,尤其是「元素是 rpcMethods 的键名」:键名对不上,消费方
// 的 rpcMethods.X 调用点就一条也对不进来,而那不会有任何编译期信号。
const keys = new Set(Object.keys(rpcMethods));

describe("host answered methods", () => {
  it("names only real rpcMethods keys, so a consumer can match its own call sites", () => {
    for (const method of [
      ...desktopAnsweredMethods,
      ...agentredAnsweredMethods,
    ]) {
      expect(keys, `${method} 不是 rpcMethods 的键名`).toContain(method);
    }
  });

  it("lists each method at most once per host", () => {
    expect(new Set(desktopAnsweredMethods).size).toBe(
      desktopAnsweredMethods.length,
    );
    expect(new Set(agentredAnsweredMethods).size).toBe(
      agentredAnsweredMethods.length,
    );
  });

  // 两个宿主答得出的东西不是同一套 —— 这正是这份产物存在的理由。两条方向都举一个:
  // 只有 agentred 才有的自更新,和只有桌面端才经中继收的项目本地路径。
  it("separates the two hosts instead of publishing one merged list", () => {
    expect(agentredAnsweredMethods).toContain("agentredSelfUpdate");
    expect(desktopAnsweredMethods).not.toContain("agentredSelfUpdate");
    expect(desktopAnsweredMethods).toContain("projectSetLocalPath");
    expect(agentredAnsweredMethods).not.toContain("projectSetLocalPath");
  });

  // 引擎探测曾经对 kind=desktop 的机器全线打不通(调用点经 executionDevice() 放行了
  // desktop,桌面端却没注册)。修好之后 engineScan 两侧都答得出,而 engineDiscover
  // 的调用点经 onlineAgentred() 收窄,只有 agentred 那一侧 —— 两者必须分得开。
  it("keeps the engine probes that reach a desktop apart from the agentred-only one", () => {
    expect(desktopAnsweredMethods).toContain("engineScan");
    expect(agentredAnsweredMethods).toContain("engineScan");
    expect(agentredAnsweredMethods).toContain("engineDiscover");
    expect(desktopAnsweredMethods).not.toContain("engineDiscover");
  });
});
