import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const runtimeMocks = vi.hoisted(() => ({
  BrowserOpenURL: vi.fn(),
  EventsOff: vi.fn(),
  EventsOn: vi.fn(),
}));

vi.mock("../../../wailsjs/runtime/runtime", () => runtimeMocks);

const sonnerMocks = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

/** 最近一条失败 toast 的标题与折叠详情里的原文。 */
function lastErrorToast(): { title: string; detail: string | null } {
  const calls = sonnerMocks.toast.error.mock.calls;
  const [title, options] = calls[calls.length - 1];
  if (!options?.description) return { title, detail: null };
  const { container, unmount } = render(<>{options.description}</>);
  const detail = container.querySelector("details pre")?.textContent ?? null;
  unmount();
  return { title, detail };
}

import { Info } from "../../../wailsjs/go/app/App";
import { UpdateSection } from "./update-section";
import { INITIAL_UPDATE_STATE, useUpdateStore } from "@/stores/update-store";

const REPOSITORY_URL = "https://github.com/agentre-hub/agentre";

const BUG_REPORT_INFO = {
  version: "1.2.3",
  commit: "abc1234",
  os: "darwin",
  arch: "arm64",
  osLabel: "macOS 14.6 (Apple Silicon)",
};

type AppMock = Record<string, ReturnType<typeof vi.fn>>;

// Info 走 vite 别名到共享 wails mock（src/__tests__/mocks/wailsApp.ts），按渠道改它的返回值。
function mockInfo(channel: string) {
  vi.mocked(Info).mockResolvedValue({
    name: "agentre",
    version: "1.2.3",
    commit: "abc1234",
    env: "test",
    runtimeMode: "interactive",
    channel,
  });
}

function installUpdateBindings(overrides?: {
  getDownloadMirror?: () => Promise<unknown>;
  getAvailableMirrors?: () => Promise<unknown>;
  getDebugLogging?: () => Promise<unknown>;
  getBugReportInfo?: () => Promise<unknown>;
}): AppMock {
  const app: AppMock = {
    GetDownloadMirror:
      (overrides?.getDownloadMirror as ReturnType<typeof vi.fn>) ??
      vi.fn(() => Promise.resolve("")),
    GetAvailableMirrors:
      (overrides?.getAvailableMirrors as ReturnType<typeof vi.fn>) ??
      vi.fn(() => Promise.resolve([{ id: "github", name: "GitHub", url: "" }])),
    GetDebugLogging:
      (overrides?.getDebugLogging as ReturnType<typeof vi.fn>) ??
      vi.fn(() => Promise.resolve(false)),
    GetBugReportInfo:
      (overrides?.getBugReportInfo as ReturnType<typeof vi.fn>) ??
      vi.fn(() => Promise.resolve(BUG_REPORT_INFO)),
    OpenLogsDir: vi.fn(() => Promise.resolve()),
    SetDebugLogging: vi.fn(() => Promise.resolve()),
  };
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { app: { App: app } },
  });
  return app;
}

beforeEach(() => {
  sonnerMocks.toast.success.mockReset();
  sonnerMocks.toast.error.mockReset();
  runtimeMocks.BrowserOpenURL.mockReset();
  runtimeMocks.EventsOff.mockReset();
  runtimeMocks.EventsOn.mockReset();
  useUpdateStore.setState({ ...INITIAL_UPDATE_STATE });
  mockInfo("stable");
  installUpdateBindings();
});

describe("UpdateSection repository address", () => {
  it("Given the update page loads, When users inspect current version, Then the repository address is visible and opens externally", async () => {
    render(<UpdateSection />);

    const link = await screen.findByRole("link", { name: REPOSITORY_URL });
    expect(link).toHaveAttribute("href", REPOSITORY_URL);

    fireEvent.click(link);

    expect(runtimeMocks.BrowserOpenURL).toHaveBeenCalledWith(REPOSITORY_URL);
  });

  it("Given update settings fail to load, When the page settles, Then the repository address remains available", async () => {
    installUpdateBindings({
      getDownloadMirror: vi.fn(() =>
        Promise.reject(new Error("settings down")),
      ),
    });

    render(<UpdateSection />);

    // 等的是「仓库地址仍在」这件事本身，而不是某个恰好发生在它之前的副作用。
    expect(
      await screen.findByRole("link", { name: REPOSITORY_URL }),
    ).toBeInTheDocument();
  });
});

describe("UpdateSection bug report", () => {
  it("Given diagnostics are available, When the user clicks Report Bug, Then a prefilled GitHub issue opens", async () => {
    render(<UpdateSection />);

    fireEvent.click(await screen.findByRole("button", { name: /report bug/i }));

    await waitFor(() => expect(runtimeMocks.BrowserOpenURL).toHaveBeenCalled());
    const opened = runtimeMocks.BrowserOpenURL.mock.calls.at(-1)?.[0] as string;
    const url = new URL(opened);
    expect(url.origin + url.pathname).toBe(`${REPOSITORY_URL}/issues/new`);
    expect(url.searchParams.get("template")).toBe("bug_report.yml");
    expect(url.searchParams.get("labels")).toBe("bug");
    expect(url.searchParams.get("version")).toBe("1.2.3 (abc1234)");
    expect(url.searchParams.get("os")).toBe("macOS 14.6 (Apple Silicon)");
  });

  it("Given diagnostics fail to load, When the user clicks Report Bug, Then the bare template still opens", async () => {
    installUpdateBindings({
      getBugReportInfo: vi.fn(() => Promise.reject(new Error("no info"))),
    });

    render(<UpdateSection />);

    fireEvent.click(await screen.findByRole("button", { name: /report bug/i }));

    await waitFor(() => expect(runtimeMocks.BrowserOpenURL).toHaveBeenCalled());
    const opened = runtimeMocks.BrowserOpenURL.mock.calls.at(-1)?.[0] as string;
    const url = new URL(opened);
    expect(url.searchParams.get("template")).toBe("bug_report.yml");
    expect(url.searchParams.get("version")).toBeNull();
  });
});

describe("UpdateSection open logs", () => {
  it("Given the version page, When the user clicks Open Logs, Then the logs folder is opened", async () => {
    const app = installUpdateBindings();

    render(<UpdateSection />);

    fireEvent.click(await screen.findByRole("button", { name: /open logs/i }));

    await waitFor(() => expect(app.OpenLogsDir).toHaveBeenCalledTimes(1));
  });

  it("Given opening the logs folder fails, When the user clicks Open Logs, Then the toast title is the localized fact and the raw text only sits in the folded detail", async () => {
    const raw = 'exec: "open": executable file not found in $PATH';
    const app = installUpdateBindings();
    app.OpenLogsDir.mockImplementation(() => Promise.reject(new Error(raw)));

    render(<UpdateSection />);

    fireEvent.click(await screen.findByRole("button", { name: /open logs/i }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, detail } = lastErrorToast();
    expect(title).toBe("Could not open the logs folder");
    expect(detail).toBe(raw);
  });
});

describe("UpdateSection debug logging", () => {
  it("Given debug logging is persisted on, When the page loads, Then the switch reflects it", async () => {
    installUpdateBindings({
      getDebugLogging: vi.fn(() => Promise.resolve(true)),
    });

    render(<UpdateSection />);

    await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());
  });

  it("Given debug logging is off, When the user toggles the switch on, Then SetDebugLogging(true) is persisted", async () => {
    const app = installUpdateBindings();

    render(<UpdateSection />);

    await waitFor(() => expect(app.GetDebugLogging).toHaveBeenCalled());

    fireEvent.click(screen.getByRole("switch"));

    await waitFor(() => expect(app.SetDebugLogging).toHaveBeenCalledWith(true));
  });

  it("Given persisting debug logging fails, When the user toggles the switch on, Then the switch rolls back and the toast title is the localized fact with the raw text folded", async () => {
    const raw = "write settings: database is locked";
    const app = installUpdateBindings();
    app.SetDebugLogging.mockImplementation(() =>
      Promise.reject(new Error(raw)),
    );

    render(<UpdateSection />);

    await waitFor(() => expect(app.GetDebugLogging).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("switch"));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, detail } = lastErrorToast();
    expect(title).toBe("Could not turn on debug logging");
    expect(detail).toBe(raw);
    expect(screen.getByRole("switch")).not.toBeChecked();
  });

  it("Given debug logging is on and persisting fails, When the user toggles it off, Then the toast says it could not be turned off", async () => {
    const app = installUpdateBindings({
      getDebugLogging: vi.fn(() => Promise.resolve(true)),
    });
    app.SetDebugLogging.mockImplementation(() =>
      Promise.reject(new Error("boom")),
    );

    render(<UpdateSection />);

    await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());
    fireEvent.click(screen.getByRole("switch"));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    expect(lastErrorToast().title).toBe("Could not turn off debug logging");
    expect(screen.getByRole("switch")).toBeChecked();
  });
});

describe("UpdateSection build channel", () => {
  it("Given the update page loads, When users look for an update channel, Then there is no channel selector to change", async () => {
    render(<UpdateSection />);

    expect(
      await screen.findByRole("link", { name: REPOSITORY_URL }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByText(/v1\.2\.3/)).toBeInTheDocument(),
    );
    expect(screen.queryByRole("combobox", { name: /channel/i })).toBeNull();
    expect(screen.queryByText(/update channel/i)).toBeNull();
  });

  it.each([
    ["beta", "Beta"],
    ["nightly", "Nightly"],
    ["dev", "Dev"],
  ])(
    "Given a %s build, When the version shows, Then the %s label sits beside it",
    async (channel, label) => {
      mockInfo(channel);

      render(<UpdateSection />);

      const version = await screen.findByText(/v1\.2\.3/);
      const badge = await screen.findByTestId("update-build-channel");
      expect(badge).toHaveTextContent(new RegExp(`^${label}$`));
      expect(version.parentElement).toContainElement(badge);
    },
  );

  it("Given a stable build, When the version shows, Then no channel label is shown", async () => {
    render(<UpdateSection />);

    await screen.findByText(/v1\.2\.3/);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /check for updates/i }),
      ).toBeInTheDocument(),
    );
    expect(screen.queryByTestId("update-build-channel")).toBeNull();
  });

  it("Given a Dev build, When the page settles, Then there is no Check for Updates entry and no check is requested", async () => {
    mockInfo("dev");
    const app = installUpdateBindings();
    app.CheckForUpdate = vi.fn(() => Promise.resolve({ hasUpdate: false }));
    app.MaybeCheckForUpdate = vi.fn(() => Promise.resolve(null));

    render(<UpdateSection />);

    await screen.findByTestId("update-build-channel");
    expect(
      screen.queryByRole("button", { name: /check for updates/i }),
    ).toBeNull();
    // 镜像设置保留；诊断入口与版本号无关，照常可用。
    expect(
      screen.getByRole("button", { name: /report bug/i }),
    ).toBeInTheDocument();
    expect(app.CheckForUpdate).not.toHaveBeenCalled();
    expect(app.MaybeCheckForUpdate).not.toHaveBeenCalled();
  });
});
