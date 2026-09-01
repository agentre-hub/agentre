import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { CtlSkillPanel } from "./ctl-skill";

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
});
