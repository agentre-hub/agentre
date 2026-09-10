import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import {
  PortForwardSection,
  type PortForwardMappingView,
} from "./port-forward-section";

/**
 * 端口转发小节的契约：它**只认 props**。
 *
 * 两个宿主（桌面端、agentre-server 控制台）读的是同一台设备上的同一份声明，
 * 行的渲染与交互语义因此归共享包；而「按下去会发生什么」（Wails 绑定 / relay
 * 请求 / 系统浏览器 / 剪贴板）与「地址长什么样」全部由宿主注入。所以这份用例
 * 把每个动作钉在「触发了注入的那个回调」上，绝不断言宿主侧发生了什么。
 *
 * 能力检测按包内既有纪律：回调缺席就**不渲染**那个控件，而不是渲染一个按下去
 * 没反应的。
 */
function mapping(
  overrides: Partial<PortForwardMappingView> = {},
): PortForwardMappingView {
  return {
    id: "m-3000",
    port: 3000,
    name: "Vite dev server",
    enabled: true,
    address: "127.0.0.1:49000",
    ...overrides,
  };
}

type Props = Parameters<typeof PortForwardSection>[0];

function renderSection(overrides: Partial<Props> = {}) {
  const handlers = {
    onCreate: vi.fn(),
    onToggleEnabled: vi.fn(),
    onRemove: vi.fn(),
    onOpen: vi.fn(),
    onCopyAddress: vi.fn(),
  };

  const view = render(
    <PortForwardSection mappings={[mapping()]} {...handlers} {...overrides} />,
  );

  return { ...handlers, view };
}

function rows() {
  return screen.getAllByRole("listitem");
}

describe("PortForwardSection", () => {
  it("Given a mapping, When the section renders, Then the row carries its port, name, address, enable switch and actions", () => {
    renderSection({
      mappings: [mapping({ port: 5173, name: "Storybook" })],
    });

    expect(
      screen.getByRole("heading", { name: "Port forwarding" }),
    ).toBeInTheDocument();

    const [row] = rows();
    expect(row).toHaveTextContent("5173");
    expect(row).toHaveTextContent("Storybook");
    expect(row).toHaveTextContent("127.0.0.1:49000");
    expect(within(row).getByRole("switch")).toBeChecked();
    expect(
      within(row).getByRole("button", { name: "Copy address" }),
    ).toBeInTheDocument();
    expect(
      within(row).getByRole("button", { name: "Open" }),
    ).toBeInTheDocument();
    expect(
      within(row).getByRole("button", { name: "More actions" }),
    ).toBeInTheDocument();
  });

  // 桌面端那条监听是点「打开」时才 net.Listen 绑出来的,打开之前根本没有地址;
  // 停用的映射同理。两者共用这一条路径 —— 渲一个编出来的 127.0.0.1:xxxxx 比留空更糟。
  it("Given a mapping without an address, When the row renders, Then it shows a dash and offers no copy control", () => {
    renderSection({ mappings: [mapping({ address: undefined })] });

    const [row] = rows();
    expect(within(row).getByText("—")).toBeInTheDocument();
    expect(
      within(row).queryByRole("button", { name: "Copy address" }),
    ).not.toBeInTheDocument();
  });

  it("Given a disabled mapping, When the row renders, Then the row stays listed but carries no open action", () => {
    renderSection({
      mappings: [mapping({ enabled: false, address: undefined })],
    });

    const [row] = rows();
    expect(row).toHaveTextContent("3000");
    expect(within(row).getByRole("switch")).not.toBeChecked();
    expect(
      within(row).queryByRole("button", { name: "Open" }),
    ).not.toBeInTheDocument();
  });

  it("Given no mappings at all, When the section renders, Then it says so and still offers the create entry", () => {
    renderSection({ mappings: [] });

    expect(
      screen.getByText("No port mappings on this device yet."),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add mapping" }),
    ).toBeInTheDocument();
  });

  // 点了必然失败的入口不该渲染 —— 设备够不着时新增写不进那台机器。
  it("Given the device is offline, When the section renders, Then it explains why and withholds the create entry", () => {
    renderSection({ mappings: [], offline: true, offlineDetail: "3m ago" });

    expect(
      screen.getByText(/Offline — forwarding is unavailable\./),
    ).toBeInTheDocument();
    expect(screen.getByText("3m ago")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Add mapping" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("No port mappings on this device yet."),
    ).not.toBeInTheDocument();
  });

  it("Given the device is offline with existing mappings, When the section renders, Then the rows are still listed but cannot be opened", () => {
    renderSection({ mappings: [mapping()], offline: true });

    const [row] = rows();
    expect(row).toHaveTextContent("3000");
    expect(row).toHaveTextContent("Vite dev server");
    expect(
      within(row).queryByRole("button", { name: "Open" }),
    ).not.toBeInTheDocument();
  });

  it("Given every action callback is withheld, When the section renders, Then no dead control is drawn", () => {
    render(<PortForwardSection mappings={[mapping()]} />);

    const [row] = rows();
    expect(within(row).queryByRole("switch")).not.toBeInTheDocument();
    expect(
      within(row).queryByRole("button", { name: "Open" }),
    ).not.toBeInTheDocument();
    expect(
      within(row).queryByRole("button", { name: "Copy address" }),
    ).not.toBeInTheDocument();
    expect(
      within(row).queryByRole("button", { name: "More actions" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Add mapping" }),
    ).not.toBeInTheDocument();
  });

  it("Given the create entry, When it is activated, Then the host callback runs", async () => {
    const user = userEvent.setup();
    const { onCreate } = renderSection({ mappings: [] });

    await user.click(screen.getByRole("button", { name: "Add mapping" }));

    expect(onCreate).toHaveBeenCalledTimes(1);
  });

  it("Given an enabled mapping, When its switch is toggled, Then the host is told which mapping and the requested state", async () => {
    const user = userEvent.setup();
    const { onToggleEnabled } = renderSection();

    await user.click(within(rows()[0]).getByRole("switch"));

    expect(onToggleEnabled).toHaveBeenCalledWith(
      expect.objectContaining({ id: "m-3000" }),
      false,
    );
  });

  // 决策 12：地址本身只提供复制,「打开」是行上独立的动作 —— 两件事挤在同一个
  // 元素上会让复制变成误触。
  it("Given a mapping with an address, When copy and open are used, Then they are separate actions reporting the same mapping", async () => {
    const user = userEvent.setup();
    const { onCopyAddress, onOpen } = renderSection();
    const [row] = rows();

    await user.click(within(row).getByRole("button", { name: "Copy address" }));
    await user.click(within(row).getByRole("button", { name: "Open" }));

    expect(onCopyAddress).toHaveBeenCalledWith(
      expect.objectContaining({ id: "m-3000", address: "127.0.0.1:49000" }),
    );
    expect(onOpen).toHaveBeenCalledWith(
      expect.objectContaining({ id: "m-3000" }),
    );
  });

  it("Given the row menu, When delete is chosen, Then the host is told which mapping to drop", async () => {
    const user = userEvent.setup();
    const { onRemove } = renderSection();

    fireEvent.pointerDown(
      within(rows()[0]).getByRole("button", { name: "More actions" }),
      { button: 0 },
    );
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));

    expect(onRemove).toHaveBeenCalledWith(
      expect.objectContaining({ id: "m-3000" }),
    );
  });

  // 脚注是宿主专属的说明（桌面端讲本机监听的生命周期,控制台没有这句）,
  // 所以由宿主传进来,包内不按宿主类型分支。
  it("Given a host footnote, When the section renders, Then it appears below the list; without one nothing is drawn in its place", () => {
    const { view } = renderSection({ footnote: "Bound while open." });
    expect(screen.getByText("Bound while open.")).toBeInTheDocument();

    view.unmount();
    renderSection();
    expect(screen.queryByText("Bound while open.")).not.toBeInTheDocument();
  });

  it("Given a host-supplied open label, When the row renders, Then that label names the open action", () => {
    renderSection({ openLabel: "Open in new tab" });

    expect(
      within(rows()[0]).getByRole("button", { name: "Open in new tab" }),
    ).toBeInTheDocument();
  });
});
