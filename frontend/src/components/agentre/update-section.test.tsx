import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const runtimeMocks = vi.hoisted(() => ({
  BrowserOpenURL: vi.fn(),
  EventsOff: vi.fn(),
  EventsOn: vi.fn(),
}));

vi.mock("../../../wailsjs/runtime/runtime", () => runtimeMocks);

import { INITIAL_UPDATE_STATE, useUpdateStore } from "@/stores/update-store";

import { UpdateChecksumDialogHost, UpdateSection } from "./update-section";

const REPOSITORY_URL = "https://github.com/agentre-ai/agentre";

const BUG_REPORT_INFO = {
  version: "1.2.3",
  commit: "abc1234",
  os: "darwin",
  arch: "arm64",
  osLabel: "macOS 14.6 (Apple Silicon)",
};

type AppMock = Record<string, ReturnType<typeof vi.fn>>;

function installUpdateBindings(overrides?: {
  getUpdateChannel?: () => Promise<unknown>;
  getDownloadMirror?: () => Promise<unknown>;
  getAvailableMirrors?: () => Promise<unknown>;
  getDebugLogging?: () => Promise<unknown>;
  getBugReportInfo?: () => Promise<unknown>;
}): AppMock {
  const app: AppMock = {
    GetUpdateChannel:
      (overrides?.getUpdateChannel as ReturnType<typeof vi.fn>) ??
      vi.fn(() => Promise.resolve("stable")),
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
  runtimeMocks.BrowserOpenURL.mockReset();
  runtimeMocks.EventsOff.mockReset();
  runtimeMocks.EventsOn.mockReset();
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
      getUpdateChannel: vi.fn(() => Promise.reject(new Error("settings down"))),
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
});

describe("UpdateSection channel switch", () => {
  it("Given the user switches update channel, When it persists, Then a fresh check runs against the new channel", async () => {
    const app = installUpdateBindings();
    app.SetUpdateChannel = vi.fn(() => Promise.resolve());
    app.CheckForUpdate = vi.fn(() =>
      Promise.resolve({ hasUpdate: false, currentVersion: "1.2.3" }),
    );

    render(<UpdateSection />);
    await waitFor(() => expect(app.GetUpdateChannel).toHaveBeenCalled());

    fireEvent.click(screen.getByRole("combobox", { name: /channel/i }));
    fireEvent.click(await screen.findByRole("option", { name: /beta/i }));

    await waitFor(() =>
      expect(app.SetUpdateChannel).toHaveBeenCalledWith("beta"),
    );
    // 切完通道不该把结果清成「未知」让用户自己再点一次——他此刻就在等结果。
    await waitFor(() => expect(app.CheckForUpdate).toHaveBeenCalled());
  });
});

describe("UpdateChecksumDialogHost", () => {
  const INFO = {
    hasUpdate: true,
    currentVersion: "0.9.1",
    latestVersion: "v0.9.2",
    releaseNotes: "",
    releaseURL: "",
    publishedAt: "",
  };

  it("Given the download was started from the status bar panel, When the checksum file cannot be fetched, Then the confirm dialog is still there without the settings page", async () => {
    const app = installUpdateBindings();
    app.DownloadAndInstallUpdate = vi.fn(() => Promise.resolve());
    useUpdateStore.setState({
      ...INITIAL_UPDATE_STATE,
      phase: { kind: "available", info: INFO },
      checksumPrompt: { open: true, reason: "404 SHA256SUMS.txt" },
    });

    // 设置页没有被渲染 —— 对话若还挂在那一节里,用户什么都看不到。
    render(<UpdateChecksumDialogHost />);

    expect(screen.getByText("404 SHA256SUMS.txt")).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: /Skip Checksum and Continue/i }),
    );

    await waitFor(() =>
      expect(app.DownloadAndInstallUpdate).toHaveBeenCalledWith(true),
    );
    expect(useUpdateStore.getState().checksumPrompt.open).toBe(false);
  });
});
