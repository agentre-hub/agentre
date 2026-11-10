import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

import { CtlSkillPanel } from "./ctl-skill";

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

type AppMock = Record<string, ReturnType<typeof vi.fn>>;

const NOT_INSTALLED = {
  pluginId: "agrctl@agentre",
  pluginInstalled: false,
  pluginPath: "/Users/tester/.claude/plugins/marketplaces/agentre/agrctl",
  universalInstalled: false,
  universalPath: "/Users/tester/.agents/skills/agrctl",
};

const INSTALLED = {
  ...NOT_INSTALLED,
  pluginInstalled: true,
  universalInstalled: true,
};

function installCtlSkillBindings(overrides?: Partial<AppMock>): AppMock {
  const app: AppMock = {
    GetCtlSkillStatus: vi.fn(() => Promise.resolve(NOT_INSTALLED)),
    InstallCtlSkill: vi.fn(() => Promise.resolve(INSTALLED)),
    UninstallCtlSkill: vi.fn(() => Promise.resolve(NOT_INSTALLED)),
    ...overrides,
  };
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { app: { App: app } },
  });
  return app;
}

beforeEach(() => {
  sonnerMocks.toast.error.mockClear();
  installCtlSkillBindings();
});

describe("CtlSkillPanel", () => {
  it("Given the skill is not installed, When the page loads, Then it shows both target paths and the read-only host list", async () => {
    render(<CtlSkillPanel />);

    await screen.findByText(NOT_INSTALLED.pluginPath);
    expect(screen.getByText(NOT_INSTALLED.universalPath)).toBeInTheDocument();
    expect(screen.getByText("Codex")).toBeInTheDocument();
    expect(screen.getByText("Cursor")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install" })).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Uninstall" }),
    ).not.toBeInTheDocument();
  });

  it("Given the skill is not installed, When the user clicks install, Then it calls the binding exactly once and flips to installed", async () => {
    const user = userEvent.setup();
    const app = installCtlSkillBindings();
    render(<CtlSkillPanel />);

    const installButton = await screen.findByRole("button", {
      name: "Install",
    });
    await user.click(installButton);

    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Uninstall" }),
      ).toBeInTheDocument(),
    );
    expect(app.InstallCtlSkill).toHaveBeenCalledTimes(1);
  });

  it("Given the skill is installed, When the user clicks uninstall, Then it calls the binding exactly once and flips back to not installed", async () => {
    const user = userEvent.setup();
    const app = installCtlSkillBindings({
      GetCtlSkillStatus: vi.fn(() => Promise.resolve(INSTALLED)),
    });
    render(<CtlSkillPanel />);

    const uninstallButton = await screen.findByRole("button", {
      name: "Uninstall",
    });
    await user.click(uninstallButton);

    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Install" }),
      ).toBeInTheDocument(),
    );
    expect(app.UninstallCtlSkill).toHaveBeenCalledTimes(1);
  });

  it("Given the install binding rejects, When the user clicks install, Then the panel stays not installed and the button becomes clickable again", async () => {
    const user = userEvent.setup();
    const app = installCtlSkillBindings({
      InstallCtlSkill: vi.fn(() => Promise.reject(new Error("boom"))),
    });
    render(<CtlSkillPanel />);

    const installButton = await screen.findByRole("button", {
      name: "Install",
    });
    await user.click(installButton);

    // 失败不能把按钮永久留在 pending：finally 必须把它放开，用户才能重试。
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Install" })).toBeEnabled(),
    );
    expect(
      screen.queryByRole("button", { name: "Uninstall" }),
    ).not.toBeInTheDocument();
    expect(app.InstallCtlSkill).toHaveBeenCalledTimes(1);
  });

  it("Given the install binding rejects with machine text, When the user clicks install, Then the toast title is the localized fact and the raw text only sits in the folded detail", async () => {
    const raw = "mkdir /Users/tester/.agents/skills: permission denied";
    const user = userEvent.setup();
    installCtlSkillBindings({
      InstallCtlSkill: vi.fn(() => Promise.reject(new Error(raw))),
    });
    render(<CtlSkillPanel />);

    await user.click(await screen.findByRole("button", { name: "Install" }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, detail } = lastErrorToast();
    expect(title).toBe("Failed to install the ctl skill");
    expect(detail).toBe(raw);
  });

  it("Given the uninstall binding rejects with machine text, When the user clicks uninstall, Then the toast title is the localized fact and the raw text only sits in the folded detail", async () => {
    const raw = "remove /Users/tester/.agents/skills/agrctl: busy";
    const user = userEvent.setup();
    installCtlSkillBindings({
      GetCtlSkillStatus: vi.fn(() => Promise.resolve(INSTALLED)),
      UninstallCtlSkill: vi.fn(() => Promise.reject(raw)),
    });
    render(<CtlSkillPanel />);

    await user.click(await screen.findByRole("button", { name: "Uninstall" }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    const { title, detail } = lastErrorToast();
    expect(title).toBe("Failed to uninstall the ctl skill");
    expect(detail).toBe(raw);
  });
});
