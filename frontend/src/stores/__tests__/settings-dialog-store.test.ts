import { beforeEach, describe, expect, it } from "vitest";

import {
  closeSettings,
  openSettings,
  toggleSettings,
  useSettingsDialogStore,
} from "../settings-dialog-store";

describe("useSettingsDialogStore", () => {
  beforeEach(() => {
    useSettingsDialogStore.setState({ open: false, page: "appearance" });
  });

  it("Given a fresh run, When settings opens without a page, Then it lands on Appearance", () => {
    openSettings();

    expect(useSettingsDialogStore.getState()).toMatchObject({
      open: true,
      page: "appearance",
    });
  });

  it("Given an entry with a page, When it opens settings, Then that page is selected", () => {
    openSettings("version-logs");

    expect(useSettingsDialogStore.getState()).toMatchObject({
      open: true,
      page: "version-logs",
    });
  });

  it("Given settings is already open, When an entry with a page fires, Then it switches page and stays open", () => {
    openSettings("appearance");
    openSettings("agent-backend");

    expect(useSettingsDialogStore.getState()).toMatchObject({
      open: true,
      page: "agent-backend",
    });
  });

  it("Given a page was picked earlier this run, When settings reopens without a page, Then it remembers that page", () => {
    openSettings();
    useSettingsDialogStore.getState().setPage("keyboard-shortcuts");
    closeSettings();

    openSettings();

    expect(useSettingsDialogStore.getState()).toMatchObject({
      open: true,
      page: "keyboard-shortcuts",
    });
  });

  it("toggleSettings opens when closed and closes when open, keeping the page", () => {
    openSettings("files");
    toggleSettings();
    expect(useSettingsDialogStore.getState()).toMatchObject({
      open: false,
      page: "files",
    });

    toggleSettings();
    expect(useSettingsDialogStore.getState()).toMatchObject({
      open: true,
      page: "files",
    });
  });

  it("does not persist anything to localStorage", () => {
    openSettings("sync");
    closeSettings();

    expect(localStorage.length).toBe(0);
  });
});
