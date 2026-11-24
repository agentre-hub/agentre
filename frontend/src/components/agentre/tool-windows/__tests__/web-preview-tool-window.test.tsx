import "@testing-library/jest-dom/vitest";

import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  afterEach,
  beforeAll,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";

const api = vi.hoisted(() => ({ ProbeSessionWebPreview: vi.fn() }));
vi.mock("@/../wailsjs/go/app/App", () => api);
vi.mock("../../../../../wailsjs/go/app/App", () => api);

const runtime = vi.hoisted(() => ({ BrowserOpenURL: vi.fn() }));
vi.mock("../../../../../wailsjs/runtime/runtime", () => runtime);
vi.mock("@/../wailsjs/runtime/runtime", () => runtime);

import { useSettingsDialogStore } from "@/stores/settings-dialog-store";
import { useToolWindowLayout } from "@/stores/tool-window-layout-store";

import { useWebPreviewStore } from "../web-preview-store";
import {
  WEB_PREVIEW_LOAD_TIMEOUT_MS,
  WebPreviewToolWindow,
} from "../web-preview-tool-window";

// 标签按钮也带 title，ByTitle 会撞；iframe 只按标签名在文档里找。
const queryFrame = (name: string) =>
  document.querySelector<HTMLIFrameElement>(`iframe[title="${name}"]`);
const getFrame = (name: string) => {
  const el = queryFrame(name);
  if (!el) throw new Error(`no iframe titled ${name}`);
  return el;
};
const findFrame = (name: string) => waitFor(() => getFrame(name));

beforeAll(() => {
  // happy-dom 默认真去加载 iframe 的 src：测试不碰网络。
  (
    window as unknown as {
      happyDOM: { settings: { disableIframePageLoading: boolean } };
    }
  ).happyDOM.settings.disableIframePageLoading = true;
});

const S = 7;
const OK = { embeddable: true, reason: "", detail: "", forwarded: false };
const probeFor = (url: string, over: Record<string, unknown> = {}) => ({
  ...OK,
  url,
  ...over,
});

function mountWindow(props: { remote?: boolean; deviceName?: string } = {}) {
  return render(
    <WebPreviewToolWindow
      sessionId={S}
      remote={props.remote ?? false}
      deviceName={props.deviceName ?? ""}
    />,
  );
}

const sync = (...ports: Array<[number, string]>) =>
  act(() =>
    useWebPreviewStore.getState().syncPorts(
      S,
      ports.map(([port, command]) => ({ port, command })),
    ),
  );

beforeEach(() => {
  useWebPreviewStore.setState({ bySession: {} });
  api.ProbeSessionWebPreview.mockReset().mockImplementation(
    (_id: number, url: string) => Promise.resolve(probeFor(url)),
  );
  runtime.BrowserOpenURL.mockReset();
  useSettingsDialogStore.setState({ open: false });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("WebPreviewToolWindow · tabs", () => {
  it("Given two listening ports, Then tabs are named ':port + command' and the first loads in an iframe", async () => {
    sync([3000, "npm run dev"], [8080, "go run ."]);
    mountWindow();

    const tablist = screen.getByRole("tablist");
    expect(
      within(tablist)
        .getAllByRole("tab")
        .map((t) => t.textContent),
    ).toEqual([":3000 npm run dev", ":8080 go run ."]);
    expect(
      within(tablist).getByRole("tab", { name: ":3000 npm run dev" }),
    ).toHaveAttribute("aria-selected", "true");
    const frame = await findFrame(":3000 npm run dev");
    expect(frame).toHaveAttribute("src", "http://localhost:3000/");
    expect(api.ProbeSessionWebPreview).toHaveBeenCalledWith(
      S,
      "http://localhost:3000/",
    );
  });

  it("Given no port yet, Then it shows the empty state and never probes", () => {
    mountWindow();
    expect(screen.getByText(/No listening ports yet/)).toBeInTheDocument();
    expect(api.ProbeSessionWebPreview).not.toHaveBeenCalled();
  });

  it("Given a second tab, When it is clicked, Then it becomes current and loads", async () => {
    sync([3000, "a"], [8080, "b"]);
    mountWindow();
    await userEvent.click(screen.getByRole("tab", { name: ":8080 b" }));
    expect(await findFrame(":8080 b")).toHaveAttribute(
      "src",
      "http://localhost:8080/",
    );
  });

  it("Given the current tab's port stops listening, Then it stays with 'no longer listening' + Reload while other tabs disappear", async () => {
    sync([3000, "a"], [8080, "b"]);
    mountWindow();
    await findFrame(":3000 a");

    sync();

    expect(screen.getAllByRole("tab")).toHaveLength(1);
    expect(screen.getByRole("tab", { name: ":3000 a" })).toBeInTheDocument();
    expect(
      screen.getByText("This port is no longer listening"),
    ).toBeInTheDocument();
    expect(queryFrame(":3000 a")).toBeNull();

    api.ProbeSessionWebPreview.mockClear();
    sync([3000, "a"]);
    expect(await findFrame(":3000 a")).toBeInTheDocument();
    expect(screen.queryByText("This port is no longer listening")).toBeNull();
  });

  it("Given the port is still gone, When Reload is pressed, Then the message stays", async () => {
    sync([3000, "a"]);
    mountWindow();
    await findFrame(":3000 a");
    sync();
    await userEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(
      screen.getByText("This port is no longer listening"),
    ).toBeInTheDocument();
  });
});

describe("WebPreviewToolWindow · address bar", () => {
  it("Given a loaded tab, Then the address bar shows the current address", async () => {
    sync([3000, "a"]);
    mountWindow();
    expect(await screen.findByRole("textbox", { name: "Address" })).toHaveValue(
      "http://localhost:3000/",
    );
  });

  it("Given a typed http(s) address, When submitted, Then the tab navigates to it", async () => {
    sync([3000, "a"]);
    mountWindow();
    const input = await screen.findByRole("textbox", { name: "Address" });
    await userEvent.clear(input);
    await userEvent.type(input, "https://example.com/p?q=1{Enter}");

    await waitFor(() =>
      expect(api.ProbeSessionWebPreview).toHaveBeenLastCalledWith(
        S,
        "https://example.com/p?q=1",
      ),
    );
    expect(await findFrame(":3000 a")).toHaveAttribute(
      "src",
      "https://example.com/p?q=1",
    );
  });

  it("Given a non-http address, When submitted, Then it is refused with a hint and nothing is probed", async () => {
    sync([3000, "a"]);
    mountWindow();
    const input = await screen.findByRole("textbox", { name: "Address" });
    api.ProbeSessionWebPreview.mockClear();
    await userEvent.clear(input);
    await userEvent.type(input, "file:///etc/passwd{Enter}");

    expect(screen.getByText("Enter an http or https address.")).toBeVisible();
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(api.ProbeSessionWebPreview).not.toHaveBeenCalled();
  });

  it("Given navigation, Then back returns to the previous address and is disabled at the start", async () => {
    sync([3000, "a"]);
    mountWindow();
    const back = await screen.findByRole("button", { name: "Back" });
    expect(back).toBeDisabled();

    const input = screen.getByRole("textbox", { name: "Address" });
    await userEvent.clear(input);
    await userEvent.type(input, "http://localhost:3000/next{Enter}");
    await waitFor(() => expect(back).toBeEnabled());

    await userEvent.click(back);
    await waitFor(() =>
      expect(screen.getByRole("textbox", { name: "Address" })).toHaveValue(
        "http://localhost:3000/",
      ),
    );
  });

  it("Given a loaded tab, When Refresh is pressed, Then the page is probed again", async () => {
    sync([3000, "a"]);
    mountWindow();
    await findFrame(":3000 a");
    api.ProbeSessionWebPreview.mockClear();

    await userEvent.click(screen.getByRole("button", { name: "Refresh" }));

    await waitFor(() =>
      expect(api.ProbeSessionWebPreview).toHaveBeenCalledTimes(1),
    );
  });

  it("Given a loaded tab, When Open in browser is pressed, Then the system browser opens the address", async () => {
    sync([3000, "a"]);
    mountWindow();
    await findFrame(":3000 a");
    await userEvent.click(
      screen.getByRole("button", { name: "Open in browser" }),
    );
    expect(runtime.BrowserOpenURL).toHaveBeenCalledWith(
      "http://localhost:3000/",
    );
  });

  it("Given the fill button, When pressed twice, Then the window maximizes and restores", async () => {
    useToolWindowLayout.setState({
      shown: {
        rightTop: "webPreview",
        rightBottom: null,
        bottomLeft: null,
        bottomRight: null,
      },
      maximized: null,
    });
    sync([3000, "a"]);
    mountWindow();
    await userEvent.click(
      await screen.findByRole("button", { name: "Fill editor" }),
    );
    expect(useToolWindowLayout.getState().maximized).toBe("webPreview");
    await userEvent.click(screen.getByRole("button", { name: "Restore" }));
    expect(useToolWindowLayout.getState().maximized).toBeNull();
  });
});

describe("WebPreviewToolWindow · remote session", () => {
  const FORWARD = "http://127.0.0.1:45123/";

  beforeEach(() => {
    api.ProbeSessionWebPreview.mockImplementation((_id: number, _url: string) =>
      Promise.resolve(probeFor(FORWARD, { forwarded: true })),
    );
  });

  it("Given a forwarded remote address, Then the bar keeps the original address, labels the device with a hover showing the forward, and the iframe loads the forward", async () => {
    sync([3000, "a"]);
    mountWindow({ remote: true, deviceName: "build-box" });

    const frame = await findFrame(":3000 a");
    expect(frame).toHaveAttribute("src", FORWARD);
    expect(screen.getByRole("textbox", { name: "Address" })).toHaveValue(
      "http://localhost:3000/",
    );
    const label = screen.getByText("build-box · via port forward");
    expect(label).toHaveAttribute("title", FORWARD);
  });

  it("Given a forwarded address, When Open in browser is pressed, Then the forward address opens, not the original", async () => {
    sync([3000, "a"]);
    mountWindow({ remote: true, deviceName: "build-box" });
    await findFrame(":3000 a");
    await userEvent.click(
      screen.getByRole("button", { name: "Open in browser" }),
    );
    expect(runtime.BrowserOpenURL).toHaveBeenCalledExactlyOnceWith(FORWARD);
  });

  it("Given a local session, Then no forward label is shown", async () => {
    api.ProbeSessionWebPreview.mockImplementation((_id: number, url: string) =>
      Promise.resolve(probeFor(url)),
    );
    sync([3000, "a"]);
    mountWindow();
    await findFrame(":3000 a");
    expect(screen.queryByText(/port forward/)).toBeNull();
  });

  it("Given the address could not be resolved, Then Open in browser does not open the wrong local port", async () => {
    api.ProbeSessionWebPreview.mockRejectedValue(new Error("device offline"));
    sync([3000, "a"]);
    mountWindow({ remote: true, deviceName: "build-box" });
    expect(await screen.findByText(/device offline/)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Open in browser" }),
    ).toBeDisabled();
  });
});

describe("WebPreviewToolWindow · cannot embed", () => {
  it("Given the preflight says the site forbids framing, Then the reason and Open in browser replace the iframe", async () => {
    api.ProbeSessionWebPreview.mockResolvedValue(
      probeFor("http://localhost:3000/", {
        embeddable: false,
        reason: "frameBlocked",
        detail: "X-Frame-Options: DENY",
      }),
    );
    sync([3000, "a"]);
    mountWindow();

    expect(
      await screen.findByText(/can't be shown in the app/),
    ).toBeInTheDocument();
    expect(screen.getByText(/X-Frame-Options: DENY/)).toBeInTheDocument();
    expect(queryFrame(":3000 a")).toBeNull();
    const buttons = screen.getAllByRole("button", { name: "Open in browser" });
    await userEvent.click(buttons[buttons.length - 1]);
    expect(runtime.BrowserOpenURL).toHaveBeenCalledWith(
      "http://localhost:3000/",
    );
  });

  it("Given a certificate failure on a remote https address, Then the reason and an entry to the port-forward settings show", async () => {
    api.ProbeSessionWebPreview.mockResolvedValue(
      probeFor("http://127.0.0.1:45123/", {
        embeddable: false,
        reason: "certificate",
        detail: "x509: certificate signed by unknown authority",
        forwarded: true,
      }),
    );
    sync([3000, "a"]);
    mountWindow({ remote: true, deviceName: "build-box" });

    expect(
      await screen.findByText(/certificate failed verification/),
    ).toBeInTheDocument();
    expect(screen.getByText(/unknown authority/)).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Open port forwarding settings" }),
    );
    const settings = useSettingsDialogStore.getState();
    expect(settings.open).toBe(true);
    expect(settings.page).toBe("remote-devices");
  });

  it("Given a forward that could not reach the https target, Then the settings entry shows too", async () => {
    api.ProbeSessionWebPreview.mockResolvedValue(
      probeFor("http://127.0.0.1:45123/", {
        embeddable: false,
        reason: "forwardFailed",
        detail: "x",
        forwarded: true,
      }),
    );
    sync([3000, "a"]);
    mountWindow({ remote: true, deviceName: "build-box" });
    expect(
      await screen.findByRole("button", {
        name: "Open port forwarding settings",
      }),
    ).toBeInTheDocument();
  });

  it("Given a local certificate failure, Then the reason shows without a forward-settings entry", async () => {
    api.ProbeSessionWebPreview.mockResolvedValue(
      probeFor("https://localhost:3000/", {
        embeddable: false,
        reason: "certificate",
        detail: "x509: bad",
      }),
    );
    sync([3000, "a"]);
    mountWindow();
    expect(
      await screen.findByText(/certificate failed verification/),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Open port forwarding settings" }),
    ).toBeNull();
  });

  it("Given the preflight says unreachable, Then the reason shows", async () => {
    api.ProbeSessionWebPreview.mockResolvedValue(
      probeFor("http://localhost:3000/", {
        embeddable: false,
        reason: "unreachable",
        detail: "connection refused",
      }),
    );
    sync([3000, "a"]);
    mountWindow();
    expect(await screen.findByText(/can't be reached/)).toBeInTheDocument();
    expect(screen.getByText(/connection refused/)).toBeInTheDocument();
  });

  it("Given an embeddable page whose iframe never loads, When the no-load timer fires, Then it shows unreachable with Open in browser", async () => {
    vi.useFakeTimers();
    sync([3000, "a"]);
    mountWindow();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(getFrame(":3000 a")).toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(WEB_PREVIEW_LOAD_TIMEOUT_MS + 100);
    });

    expect(screen.getByText(/did not load/)).toBeInTheDocument();
    expect(queryFrame(":3000 a")).toBeNull();
  });

  it("Given the iframe fires load in time, Then the no-load timer does not trigger", async () => {
    vi.useFakeTimers();
    sync([3000, "a"]);
    mountWindow();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    fireEvent.load(getFrame(":3000 a"));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(WEB_PREVIEW_LOAD_TIMEOUT_MS * 2);
    });

    expect(getFrame(":3000 a")).toBeInTheDocument();
    expect(screen.queryByText(/did not load/)).toBeNull();
  });
});
