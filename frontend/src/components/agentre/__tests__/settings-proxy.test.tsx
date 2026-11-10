import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  GetAppSetting: vi.fn(),
  GetGatewayStatus: vi.fn(),
  RestartGateway: vi.fn(),
  UpdateAppSettings: vi.fn(),
}));
vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

const sonnerMocks = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

import i18n from "../../../i18n";
import { SettingsProxyPanel } from "../settings-proxy";

const BIND_ERR = "listen tcp 127.0.0.1:52401: bind: address already in use";

const running = {
  status: "running",
  listenURL: "http://127.0.0.1:52401",
  reason: "",
  routes: [],
};
const stopped = {
  status: "stopped",
  listenURL: "",
  reason: BIND_ERR,
  routes: [],
};

function bridge(gateway: object = running) {
  appMocks.GetAppSetting.mockImplementation(({ key }: { key: string }) =>
    Promise.resolve({
      key,
      value: key === "proxy.listen_host" ? "127.0.0.1" : "52401",
    }),
  );
  appMocks.GetGatewayStatus.mockResolvedValue(gateway);
  appMocks.UpdateAppSettings.mockResolvedValue({});
  appMocks.RestartGateway.mockResolvedValue({ status: gateway });
}

/** 最近一条失败 toast 的标题、正文与折叠详情里的原文。 */
function lastErrorToast(): {
  title: string;
  body: string;
  detail: string | null;
} {
  const calls = sonnerMocks.toast.error.mock.calls;
  const [title, options] = calls[calls.length - 1];
  if (!options?.description) return { title, body: "", detail: null };
  const { container, unmount } = render(<>{options.description}</>);
  const detail = container.querySelector("details pre")?.textContent ?? null;
  const body = container.querySelector("p")?.textContent ?? "";
  unmount();
  return { title, body, detail };
}

async function renderLoaded() {
  const user = userEvent.setup();
  render(<SettingsProxyPanel />);
  const port = await screen.findByDisplayValue("52401");
  await waitFor(() => expect(port).toBeEnabled());
  return { user, port };
}

beforeEach(async () => {
  await i18n.changeLanguage("en");
  vi.clearAllMocks();
});

describe("SettingsProxyPanel results", () => {
  it("Given apply succeeds and the gateway runs, Then a success toast is raised and no banner is drawn on the page", async () => {
    bridge();
    const { user, port } = await renderLoaded();

    await user.clear(port);
    await user.type(port, "52402");
    await user.click(screen.getByRole("button", { name: "Apply and Restart" }));

    await waitFor(() =>
      expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
        "Applied and restarted proxy",
      ),
    );
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByText("Applied and restarted proxy")).toBeNull();
  });

  it("Given restart succeeds, Then a success toast is raised", async () => {
    bridge();
    const { user } = await renderLoaded();

    await user.click(screen.getByRole("button", { name: "Restart Proxy" }));

    await waitFor(() =>
      expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
        "Local HTTP proxy restarted",
      ),
    );
  });
});

describe("SettingsProxyPanel machine text", () => {
  it("Given the settings save but the gateway stays stopped, Then the toast states the fact and the gateway reason only sits in the folded detail", async () => {
    bridge();
    appMocks.GetGatewayStatus.mockResolvedValueOnce(running).mockResolvedValue(
      stopped,
    );
    const { user, port } = await renderLoaded();

    await user.clear(port);
    await user.type(port, "52402");
    await user.click(screen.getByRole("button", { name: "Apply and Restart" }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, detail } = lastErrorToast();
    expect(title).toBe("Settings saved, but the proxy didn't start");
    expect(title).not.toContain(BIND_ERR);
    expect(detail).toBe(BIND_ERR);
    expect(sonnerMocks.toast.success).not.toHaveBeenCalled();
  });

  it("Given the gateway is stopped with a reason, Then the URL readout only states that it is not started", async () => {
    bridge(stopped);
    await renderLoaded();

    const readout = await screen.findByText("Not started");
    expect(readout.textContent).toBe("Not started");
    expect(screen.queryByText(new RegExp(BIND_ERR))).toBeNull();
  });

  it("Given restart leaves the gateway stopped, Then the toast states the fact and the reason only sits in the detail", async () => {
    bridge();
    appMocks.RestartGateway.mockResolvedValue({ status: stopped });
    const { user } = await renderLoaded();

    await user.click(screen.getByRole("button", { name: "Restart Proxy" }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, detail } = lastErrorToast();
    expect(title).toBe("The proxy still isn't running");
    expect(detail).toBe(BIND_ERR);
  });

  it("Given the restart call itself rejects, Then the toast is a localized fact with the raw error folded", async () => {
    bridge();
    appMocks.RestartGateway.mockRejectedValue(new Error("rpc boom"));
    const { user } = await renderLoaded();

    await user.click(screen.getByRole("button", { name: "Restart Proxy" }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, detail } = lastErrorToast();
    expect(title).toBe("The proxy couldn't be restarted");
    expect(detail).toBe("rpc boom");
  });

  it("Given saving the settings rejects with a coded error, Then the backend sentence is the body and the page keeps the typed value", async () => {
    bridge();
    appMocks.UpdateAppSettings.mockRejectedValue(
      new Error("agentre-code:10000 Invalid setting\nport out of range"),
    );
    const { user, port } = await renderLoaded();

    await user.clear(port);
    await user.type(port, "52402");
    await user.click(screen.getByRole("button", { name: "Apply and Restart" }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, body, detail } = lastErrorToast();
    expect(title).toBe("Settings weren't saved");
    expect(body).toBe("Invalid setting");
    expect(detail).toBe("port out of range");
    expect(port).toHaveValue("52402");
  });

  it("Given loading the settings fails, Then a localized toast with the raw error folded and a retry that reloads", async () => {
    bridge();
    appMocks.GetGatewayStatus.mockRejectedValueOnce(new Error("load boom"));
    render(<SettingsProxyPanel />);

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, detail } = lastErrorToast();
    expect(title).toBe("Couldn't load the proxy settings");
    expect(detail).toBe("load boom");
    expect(screen.queryByText("load boom")).toBeNull();

    const options = sonnerMocks.toast.error.mock.calls[0][1];
    options.action.onClick();
    expect(await screen.findByDisplayValue("52401")).toBeInTheDocument();
  });
});

describe("SettingsProxyPanel port validation", () => {
  it("Given the user is still typing an invalid port, Then no error shows until the field loses focus, then it revalidates live", async () => {
    bridge();
    const { user, port } = await renderLoaded();

    await user.clear(port);
    await user.type(port, "a");
    expect(screen.queryByText("Port must be numeric")).toBeNull();
    expect(port).not.toHaveAttribute("aria-invalid", "true");

    await user.tab();
    expect(screen.getByText("Port must be numeric")).toBeInTheDocument();
    expect(port).toHaveAttribute("aria-invalid", "true");

    await user.clear(port);
    await user.type(port, "8");
    expect(screen.queryByText("Port must be numeric")).toBeNull();
    expect(port).not.toHaveAttribute("aria-invalid", "true");
  });

  it("Given an invalid port that was never blurred, When the user submits, Then the error shows and nothing is saved", async () => {
    bridge();
    const { user, port } = await renderLoaded();

    await user.clear(port);
    await user.type(port, "99999");
    expect(screen.queryByText("Port must be between 0 and 65535")).toBeNull();

    await user.click(screen.getByRole("button", { name: "Apply and Restart" }));

    expect(
      screen.getByText("Port must be between 0 and 65535"),
    ).toBeInTheDocument();
    expect(appMocks.UpdateAppSettings).not.toHaveBeenCalled();
  });
});
