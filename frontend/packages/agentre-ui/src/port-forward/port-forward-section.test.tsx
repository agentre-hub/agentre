import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
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
    target: "http://127.0.0.1:3000",
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
      mappings: [
        mapping({ target: "http://127.0.0.1:5173", name: "Storybook" }),
      ],
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

  // 表单挪进了共享包(规格「控制台界面」):「新增映射」不再直接触发宿主回调,
  // 而是打开包内自己的表单;宿主回调要等表单提交才跑(见 create form 用例组)。
  it("Given the create entry, When it is activated, Then the form opens instead of calling the host directly", async () => {
    const user = userEvent.setup();
    const { onCreate } = renderSection({ mappings: [] });

    await user.click(screen.getByRole("button", { name: "Add mapping" }));

    expect(onCreate).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Target")).toBeInTheDocument();
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

  // 规格「控制台界面」:环回目标只显示端口,其余显示规范化后的完整目标。
  it("Given a loopback target, When the row renders, Then only the port shows", () => {
    renderSection({
      mappings: [
        mapping({ target: "http://127.0.0.1:8080", address: undefined }),
      ],
    });

    expect(rows()[0]).toHaveTextContent("8080");
    expect(rows()[0]).not.toHaveTextContent("127.0.0.1");
  });

  it("Given a LAN target, When the row renders, Then the full normalized target shows", () => {
    renderSection({
      mappings: [mapping({ target: "https://192.168.1.5:8443" })],
    });

    expect(rows()[0]).toHaveTextContent("https://192.168.1.5:8443");
  });
});

describe("PortForwardSection create form", () => {
  // 规格「映射与目标」+「控制台界面」:表单只认三种写法,https-only 的「忽略证书
  // 错误」勾选框,以及三种写法都要能提交成功。判定的权威在设备侧(决策 8),这里的
  // 客户端校验只挡明显不成形的输入。
  function open() {
    return {
      onCreate: vi.fn().mockResolvedValue(undefined),
    };
  }

  async function openForm(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole("button", { name: "Add mapping" }));
  }

  it("Given the create entry is activated, When the form renders, Then it carries a target field with the accepted-forms hint and a name field, but no insecure checkbox yet", async () => {
    const user = userEvent.setup();
    render(<PortForwardSection mappings={[]} {...open()} />);

    await openForm(user);

    expect(screen.getByLabelText("Target")).toBeInTheDocument();
    expect(
      screen.getByText("Port, or http(s)://host:port"),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
    expect(
      screen.queryByLabelText("Ignore certificate errors"),
    ).not.toBeInTheDocument();
  });

  it.each([["3000"], ["192.168.1.5:8080"], ["http://192.168.1.5:8080"]])(
    "Given the target %s, When the form is submitted, Then the host callback receives it verbatim",
    async (target) => {
      const user = userEvent.setup();
      const { onCreate } = open();
      render(<PortForwardSection mappings={[]} onCreate={onCreate} />);

      await openForm(user);
      await user.type(screen.getByLabelText("Target"), target);
      await user.type(screen.getByLabelText("Name"), "dev server");
      await user.click(screen.getByRole("button", { name: "Add" }));

      await waitFor(() =>
        expect(onCreate).toHaveBeenCalledWith({
          target,
          name: "dev server",
          insecure: false,
        }),
      );
    },
  );

  it("Given the target is typed as https, When it is entered, Then the insecure checkbox appears; switching back to http withholds it", async () => {
    const user = userEvent.setup();
    render(<PortForwardSection mappings={[]} {...open()} />);

    await openForm(user);
    const targetField = screen.getByLabelText("Target");
    await user.type(targetField, "https://example.com");

    expect(
      screen.getByLabelText("Ignore certificate errors"),
    ).toBeInTheDocument();

    await user.clear(targetField);
    await user.type(targetField, "http://example.com");

    expect(
      screen.queryByLabelText("Ignore certificate errors"),
    ).not.toBeInTheDocument();
  });

  it("Given the insecure checkbox is checked on an https target, When submitted, Then the host callback receives insecure: true", async () => {
    const user = userEvent.setup();
    const { onCreate } = open();
    render(<PortForwardSection mappings={[]} onCreate={onCreate} />);

    await openForm(user);
    await user.type(screen.getByLabelText("Target"), "https://example.com");
    await user.click(screen.getByLabelText("Ignore certificate errors"));
    await user.click(screen.getByRole("button", { name: "Add" }));

    await waitFor(() =>
      expect(onCreate).toHaveBeenCalledWith({
        target: "https://example.com",
        name: "",
        insecure: true,
      }),
    );
  });

  it("Given a target that does not match any accepted form, When submitted, Then the host callback is never called and a validation message shows", async () => {
    const user = userEvent.setup();
    const { onCreate } = open();
    render(<PortForwardSection mappings={[]} onCreate={onCreate} />);

    await openForm(user);
    await user.type(screen.getByLabelText("Target"), "http://example.com/path");
    await user.click(screen.getByRole("button", { name: "Add" }));

    expect(onCreate).not.toHaveBeenCalled();
    expect(
      await screen.findByText(
        "Enter a port, host:port, or http(s)://host[:port].",
      ),
    ).toBeInTheDocument();
  });

  it("Given the host callback resolves, When submission succeeds, Then the form closes and clears", async () => {
    const user = userEvent.setup();
    const { onCreate } = open();
    render(<PortForwardSection mappings={[]} onCreate={onCreate} />);

    await openForm(user);
    await user.type(screen.getByLabelText("Target"), "3000");
    await user.click(screen.getByRole("button", { name: "Add" }));

    await waitFor(() => expect(onCreate).toHaveBeenCalled());
    expect(screen.queryByLabelText("Target")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add mapping" }),
    ).toBeInTheDocument();
  });

  it("Given the host callback rejects, When submission fails, Then the form stays open and shows the host's message", async () => {
    const user = userEvent.setup();
    const onCreate = vi.fn().mockRejectedValue(new Error("Device says no"));
    render(<PortForwardSection mappings={[]} onCreate={onCreate} />);

    await openForm(user);
    await user.type(screen.getByLabelText("Target"), "3000");
    await user.click(screen.getByRole("button", { name: "Add" }));

    expect(await screen.findByText("Device says no")).toBeInTheDocument();
    expect(screen.getByLabelText("Target")).toBeInTheDocument();
  });

  it("Given the cancel button, When it is clicked, Then the form closes without calling the host", async () => {
    const user = userEvent.setup();
    const { onCreate } = open();
    render(<PortForwardSection mappings={[]} onCreate={onCreate} />);

    await openForm(user);
    await user.type(screen.getByLabelText("Target"), "3000");
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    expect(onCreate).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("Target")).not.toBeInTheDocument();
  });

  it("Given the form is open, When the device goes offline, Then the form closes and clears", async () => {
    const user = userEvent.setup();
    const { onCreate } = open();
    const { rerender } = render(
      <PortForwardSection mappings={[]} onCreate={onCreate} offline={false} />,
    );

    await openForm(user);
    await user.type(screen.getByLabelText("Target"), "3000");

    rerender(<PortForwardSection mappings={[]} onCreate={onCreate} offline />);

    expect(screen.queryByLabelText("Target")).not.toBeInTheDocument();
  });
});
