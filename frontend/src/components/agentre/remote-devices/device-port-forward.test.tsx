// frontend/src/components/agentre/remote-devices/device-port-forward.test.tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// wailsjs/ 是 gitignore 的生成物,这里按它生成出来的真实形状打桩:
// List 交回一批 MappingView(没有 address —— 那条地址是 Open 绑上监听之后才有的),
// Open 交回一条 http://127.0.0.1:<端口>。
const appMocks = vi.hoisted(() => ({
  PortForwardList: vi.fn(),
  PortForwardCreate: vi.fn(),
  PortForwardSetEnabled: vi.fn(),
  PortForwardDelete: vi.fn(),
  PortForwardOpen: vi.fn(),
}));
vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

const browserOpenURLMock = vi.hoisted(() => vi.fn());
vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  BrowserOpenURL: (url: string) => browserOpenURLMock(url),
}));

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

import { DevicePortForward } from "./device-port-forward";

const DEVICE_ID = 42;

/** `internal/app/coded_error.go` 写在 error 文本里的那条契约。 */
function coded(code: number, message: string): Error {
  return new Error(`agentre-code:${code} ${message}`);
}

function rows(): HTMLElement[] {
  return Array.from(
    document.querySelectorAll<HTMLElement>('[data-slot="port-forward-row"]'),
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("navigator", {
    ...navigator,
    clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
});

describe("DevicePortForward", () => {
  it("列举这台设备上的映射,按端口排序,地址一律是占位破折号", async () => {
    appMocks.PortForwardList.mockResolvedValue([
      { id: "12", port: 8080, name: "api", enabled: false },
      { id: "11", port: 5173, name: "vite dev", enabled: true },
    ]);

    render(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);

    await waitFor(() => expect(rows()).toHaveLength(2));
    expect(appMocks.PortForwardList).toHaveBeenCalledWith("42");
    expect(rows()[0]).toHaveTextContent("5173");
    expect(rows()[0]).toHaveTextContent("vite dev");
    expect(rows()[1]).toHaveTextContent("8080");
    // Open 之前没有地址可渲染 —— 不编一个出来。
    for (const row of rows()) {
      expect(
        row.querySelector('[data-slot="port-forward-address"]'),
      ).toHaveTextContent("—");
    }
  });

  it("一条映射都没有时给空态句子,新增入口照旧在", async () => {
    appMocks.PortForwardList.mockResolvedValue([]);

    render(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);

    expect(
      await screen.findByText("No port mappings on this device yet."),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add mapping" }),
    ).toBeInTheDocument();
  });

  it("设备够不着时出离线句、不出新增入口,已列出的行整行保留只是打不开", async () => {
    appMocks.PortForwardList.mockRejectedValue(coded(21000, "device offline"));

    render(
      <DevicePortForward
        deviceId={DEVICE_ID}
        offline
        offlineDetail="Last connected 3m ago"
      />,
    );

    expect(
      await screen.findByText("Offline — forwarding is unavailable."),
    ).toBeInTheDocument();
    expect(screen.getByText("Last connected 3m ago")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Add mapping" }),
    ).not.toBeInTheDocument();
    // 首屏就够不着时列表是**空的**(规格订正 358986f5):这一次列举一行都没读到,
    // 不该照旧列出些什么。下一条用例钉的是另一半——先列举成功、再撞上离线,那时候
    // 已列出的行要整行留着。
    expect(rows()).toHaveLength(0);
  });

  it("首屏就离线时列表是空的,先列举成功再撞上离线才留得住已列出的行", async () => {
    appMocks.PortForwardList.mockResolvedValue([
      { id: "11", port: 5173, name: "vite dev", enabled: true },
    ]);

    render(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);
    expect(await screen.findByText("5173")).toBeInTheDocument();

    // 规格「三种非常态」:离线时那一行整行保留、只是打不开——启停与删除照常画。
    appMocks.PortForwardSetEnabled.mockRejectedValue(coded(21000, "offline"));
    await userEvent.click(
      screen.getByRole("switch", { name: "Enable mapping" }),
    );

    expect(
      await screen.findByText("Offline — forwarding is unavailable."),
    ).toBeInTheDocument();
    expect(screen.getByText("5173")).toBeInTheDocument();
    expect(
      screen.getByRole("switch", { name: "Enable mapping" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "More actions" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Open" }),
    ).not.toBeInTheDocument();
  });

  // 离线态必须是**可逆**的。列举只在挂载时跑过一次,而这个小块是折叠展开才重挂的,
  // 所以离线态若没有自己的出口,一次首屏离线就把它永久钉住:设备早回来了,它还说着
  // 「够不着」、新增入口一直收着,而用户手上没有任何重试的入口。
  it("设备回来了(离线位从真变假)就重新列举,不再钉在离线态上", async () => {
    appMocks.PortForwardList.mockRejectedValue(coded(21000, "device offline"));

    const { rerender } = render(
      <DevicePortForward deviceId={DEVICE_ID} offline />,
    );
    expect(
      await screen.findByText("Offline — forwarding is unavailable."),
    ).toBeInTheDocument();

    appMocks.PortForwardList.mockResolvedValue([
      { id: "11", port: 5173, name: "vite dev", enabled: true },
    ]);
    rerender(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);

    expect(await screen.findByText("5173")).toBeInTheDocument();
    expect(
      screen.queryByText("Offline — forwarding is unavailable."),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add mapping" }),
    ).toBeInTheDocument();
  });

  it("一次动作撞上离线之后,下一次动作真的到了设备,离线句就该撤掉", async () => {
    appMocks.PortForwardList.mockResolvedValue([
      { id: "11", port: 5173, name: "vite dev", enabled: true },
    ]);
    render(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);
    expect(await screen.findByText("5173")).toBeInTheDocument();

    appMocks.PortForwardSetEnabled.mockRejectedValue(coded(21000, "offline"));
    await userEvent.click(
      screen.getByRole("switch", { name: "Enable mapping" }),
    );
    expect(
      await screen.findByText("Offline — forwarding is unavailable."),
    ).toBeInTheDocument();

    // 同一个开关按得动了 —— 那台机器就在那儿,离线句与收起来的新增入口都不该再留着。
    appMocks.PortForwardSetEnabled.mockResolvedValue({
      id: "11",
      port: 5173,
      name: "vite dev",
      enabled: false,
    });
    await userEvent.click(
      screen.getByRole("switch", { name: "Enable mapping" }),
    );

    await waitFor(() =>
      expect(
        screen.queryByText("Offline — forwarding is unavailable."),
      ).not.toBeInTheDocument(),
    );
    expect(
      screen.getByRole("button", { name: "Add mapping" }),
    ).toBeInTheDocument();
  });

  it("停用的那一行不出「打开」,启用的那一行出", async () => {
    appMocks.PortForwardList.mockResolvedValue([
      { id: "11", port: 5173, name: "vite dev", enabled: true },
      { id: "12", port: 8080, name: "api", enabled: false },
    ]);

    render(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);

    await waitFor(() => expect(rows()).toHaveLength(2));
    expect(
      rows()[0].querySelector('button[aria-label="Open"]'),
    ).toBeInTheDocument();
    expect(
      rows()[1].querySelector('button[aria-label="Open"]'),
    ).not.toBeInTheDocument();
  });

  it("点「打开」把 PortForwardOpen 交回的那条地址喂给系统浏览器并渲出来", async () => {
    appMocks.PortForwardList.mockResolvedValue([
      { id: "11", port: 5173, name: "vite dev", enabled: true },
    ]);
    appMocks.PortForwardOpen.mockResolvedValue("http://127.0.0.1:51234");

    render(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);
    await waitFor(() => expect(rows()).toHaveLength(1));

    await userEvent.click(screen.getByRole("button", { name: "Open" }));

    await waitFor(() =>
      expect(browserOpenURLMock).toHaveBeenCalledWith("http://127.0.0.1:51234"),
    );
    expect(appMocks.PortForwardOpen).toHaveBeenCalledWith("42", "11", 5173);
    await waitFor(() =>
      expect(
        rows()[0].querySelector('[data-slot="port-forward-address"]'),
      ).toHaveTextContent("http://127.0.0.1:51234"),
    );
    expect(
      screen.getByRole("button", { name: "Copy address" }),
    ).toBeInTheDocument();
  });

  it("停用一条映射:请求的是目标态 false,确认之后那条地址一并消失", async () => {
    appMocks.PortForwardList.mockResolvedValue([
      { id: "11", port: 5173, name: "vite dev", enabled: true },
    ]);
    appMocks.PortForwardOpen.mockResolvedValue("http://127.0.0.1:51234");
    appMocks.PortForwardSetEnabled.mockResolvedValue({
      id: "11",
      port: 5173,
      name: "vite dev",
      enabled: false,
    });

    render(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);
    await waitFor(() => expect(rows()).toHaveLength(1));
    await userEvent.click(screen.getByRole("button", { name: "Open" }));
    await waitFor(() => expect(browserOpenURLMock).toHaveBeenCalled());

    await userEvent.click(
      screen.getByRole("switch", { name: "Enable mapping" }),
    );

    await waitFor(() =>
      expect(appMocks.PortForwardSetEnabled).toHaveBeenCalledWith(
        "42",
        "11",
        false,
      ),
    );
    // 设备确认停用之后那条监听自己关了,地址随之失效。
    await waitFor(() =>
      expect(
        rows()[0].querySelector('[data-slot="port-forward-address"]'),
      ).toHaveTextContent("—"),
    );
    expect(rows()[0]).toHaveAttribute("data-enabled", "false");
  });

  it("那条声明在设备上已经没了时重新取一次列表", async () => {
    appMocks.PortForwardList.mockResolvedValueOnce([
      { id: "11", port: 5173, name: "vite dev", enabled: true },
    ]).mockResolvedValueOnce([]);
    appMocks.PortForwardSetEnabled.mockRejectedValue(
      coded(21001, "mapping not declared"),
    );

    render(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);
    await waitFor(() => expect(rows()).toHaveLength(1));

    await userEvent.click(
      screen.getByRole("switch", { name: "Enable mapping" }),
    );

    await waitFor(() =>
      expect(appMocks.PortForwardList).toHaveBeenCalledTimes(2),
    );
    await waitFor(() => expect(rows()).toHaveLength(0));
  });

  it("删除一条映射后那一行不再列出", async () => {
    appMocks.PortForwardList.mockResolvedValue([
      { id: "11", port: 5173, name: "vite dev", enabled: true },
    ]);
    appMocks.PortForwardDelete.mockResolvedValue(undefined);

    render(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);
    await waitFor(() => expect(rows()).toHaveLength(1));

    await userEvent.click(screen.getByRole("button", { name: "More actions" }));
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Delete" }),
    );

    await waitFor(() =>
      expect(appMocks.PortForwardDelete).toHaveBeenCalledWith("42", "11"),
    );
    await waitFor(() => expect(rows()).toHaveLength(0));
  });

  it("新增一条映射:端口按数字过桥,设备落库后的那一行直接进列表", async () => {
    appMocks.PortForwardList.mockResolvedValue([]);
    appMocks.PortForwardCreate.mockResolvedValue({
      id: "13",
      port: 3000,
      name: "next dev",
      enabled: true,
    });

    render(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);
    await screen.findByText("No port mappings on this device yet.");

    await userEvent.click(screen.getByRole("button", { name: "Add mapping" }));
    await userEvent.type(screen.getByLabelText("Port"), "3000");
    await userEvent.type(screen.getByLabelText("Name"), "next dev");
    await userEvent.click(screen.getByRole("button", { name: "Add" }));

    await waitFor(() =>
      expect(appMocks.PortForwardCreate).toHaveBeenCalledWith(
        "42",
        3000,
        "next dev",
      ),
    );
    await waitFor(() => expect(rows()).toHaveLength(1));
    expect(rows()[0]).toHaveTextContent("3000");
  });

  it("端口已被声明与端口越界落在同一格上,但说的是两句不同的话", async () => {
    appMocks.PortForwardList.mockResolvedValue([]);
    appMocks.PortForwardCreate.mockRejectedValueOnce(
      coded(21002, "port already declared"),
    ).mockRejectedValueOnce(coded(21003, "port out of range"));

    render(<DevicePortForward deviceId={DEVICE_ID} offline={false} />);
    await screen.findByText("No port mappings on this device yet.");

    await userEvent.click(screen.getByRole("button", { name: "Add mapping" }));
    await userEvent.type(screen.getByLabelText("Port"), "5173");
    await userEvent.click(screen.getByRole("button", { name: "Add" }));

    // 判据打在**这两句话本身**上,不是「非空且互不相同」:后者连 add.failed 那条
    // 兜底都能满足 —— 它把设备回来的两句英文原话插进同一个模板,同样非空、同样不等。
    // 也就是说,把 21002 / 21003 两个分支整个删掉,那种断言照样绿,而用户看到的已经
    // 从「换一个端口」变成了一句机器原话。
    const taken = await screen.findByTestId("port-forward-add-error");
    expect(taken).toHaveTextContent(
      "This port is already mapped on this device. Pick another one.",
    );
    expect(screen.getByLabelText("Port")).toHaveAttribute(
      "aria-invalid",
      "true",
    );

    await userEvent.click(screen.getByRole("button", { name: "Add" }));

    await waitFor(() => {
      expect(screen.getByTestId("port-forward-add-error")).toHaveTextContent(
        "The port number must be between 1 and 65535.",
      );
    });
  });
});
