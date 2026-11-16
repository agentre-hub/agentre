import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { DeviceTag } from "../device-tag";

describe("DeviceTag", () => {
  it("本地: deviceId 为空时显示 '本地' chip", () => {
    render(<DeviceTag deviceId="" deviceName="" online={false} />);
    expect(screen.getByText("Local")).toBeInTheDocument();
  });

  it("远端 online: 显示 deviceName, 不显示 offline, 也不挂离线图标", () => {
    render(<DeviceTag deviceId="7" deviceName="linux-srv" online />);
    expect(screen.getByText("linux-srv")).toBeInTheDocument();
    expect(screen.queryByText(/offline/i)).toBeNull();
    expect(screen.queryByRole("img", { name: "Offline" })).toBeNull();
  });

  it("远端 offline: 离线图标(名「离线」) + deviceName, 不再写 offline 字样", () => {
    const { container } = render(
      <DeviceTag deviceId="7" deviceName="mac-mini" online={false} />,
    );
    expect(screen.getByRole("img", { name: "Offline" })).toBeInTheDocument();
    expect(container.firstElementChild).toHaveTextContent(/^mac-mini$/);
  });

  it("远端 offline + lastSeen: 名字后跟「 · 上次在线」", () => {
    const { container } = render(
      <DeviceTag
        deviceId="7"
        deviceName="mac-mini"
        online={false}
        lastSeen="2h"
      />,
    );
    expect(screen.getByRole("img", { name: "Offline" })).toBeInTheDocument();
    expect(container.firstElementChild).toHaveTextContent(/^mac-mini · 2h$/);
  });

  it("传入 className 时附加到根 span 上(不覆盖默认样式)", () => {
    const { container } = render(
      <DeviceTag
        deviceId=""
        deviceName=""
        online={false}
        className="my-test-class"
      />,
    );
    const span = container.querySelector("span");
    expect(span?.className).toContain("my-test-class");
    expect(span?.className).toContain("bg-primary-soft"); // 默认样式仍在
  });
});
