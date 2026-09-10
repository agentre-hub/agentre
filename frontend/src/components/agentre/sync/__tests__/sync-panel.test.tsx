import "@testing-library/jest-dom/vitest";

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  SyncStatus: vi.fn(),
  SyncListLostChanges: vi.fn(),
  SyncRestoreLostChange: vi.fn(),
  SyncRecreateFromLostChange: vi.fn(),
  SyncDiscardLostChange: vi.fn(),
  SyncNow: vi.fn(),
}));

vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);

const sonnerMocks = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("sonner", () => sonnerMocks);

import { SyncPanel } from "../sync-panel";

const NOW = 1_700_000_000_000;

const IDLE_STATUS = {
  enabled: true,
  accountID: 1,
  cursor: 42,
  lastSuccessAt: NOW - 2 * 60_000,
  pendingCount: 0,
  deferredCount: 0,
  lostChangeCount: 0,
  lastError: "",
};

function overwrittenRow(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    id: 1,
    entityType: "project",
    entitySyncID: "sync-project-1",
    reason: "overwritten",
    payloadJSON: JSON.stringify({ name: "agentre-server" }),
    originDevice: "Work Mac mini",
    baseVersion: 3,
    occurredAt: NOW - 60_000,
    ...overrides,
  };
}

function discardedRow() {
  return {
    id: 2,
    entityType: "project_location",
    entitySyncID: "sync-loc-1",
    reason: "discarded",
    payloadJSON: JSON.stringify({ path: "/home/dev/agentre-hub" }),
    originDevice: "",
    baseVersion: 0,
    occurredAt: NOW - 3 * 60_000,
  };
}

function rejectedRow() {
  return {
    id: 3,
    entityType: "project",
    entitySyncID: "sync-project-2",
    reason: "rejected",
    payloadJSON: JSON.stringify({ name: "agentre-hub" }),
    originDevice: "",
    baseVersion: 5,
    occurredAt: NOW - 5 * 60_000,
  };
}

describe("SyncPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.spyOn(Date, "now").mockReturnValue(NOW);
    appMocks.SyncListLostChanges.mockResolvedValue([]);
  });

  describe("sync status card (goal ①: last success time / pending count / unreachable reason)", () => {
    it("shows last successful sync time and a 0-pending row when up to date", async () => {
      appMocks.SyncStatus.mockResolvedValue(IDLE_STATUS);
      render(<SyncPanel />);

      expect(await screen.findByText("Sync status")).toBeInTheDocument();
      expect(screen.getByText("Up to date")).toBeInTheDocument();
      expect(screen.getByText("Last successful sync")).toBeInTheDocument();
      expect(screen.getByText("2m ago")).toBeInTheDocument();
      expect(screen.getByText("Pending")).toBeInTheDocument();
      expect(screen.getByText("0")).toBeInTheDocument();
    });

    it("shows a pending-count badge and suppresses the redundant pending row while changes are queued", async () => {
      appMocks.SyncStatus.mockResolvedValue({
        ...IDLE_STATUS,
        pendingCount: 3,
        lastSuccessAt: NOW - 14 * 60_000,
      });
      render(<SyncPanel />);

      expect(await screen.findByText("3 pending")).toBeInTheDocument();
      expect(screen.getByText("14m ago")).toBeInTheDocument();
      expect(screen.queryByText("Pending")).not.toBeInTheDocument();
    });

    it("shows the unreachable reason and a retry button that re-syncs", async () => {
      const user = userEvent.setup();
      appMocks.SyncStatus.mockResolvedValueOnce({
        ...IDLE_STATUS,
        lastError: "server: unreachable",
      });
      appMocks.SyncNow.mockResolvedValueOnce(undefined);
      appMocks.SyncStatus.mockResolvedValueOnce(IDLE_STATUS);
      render(<SyncPanel />);

      expect(await screen.findByText("Unreachable")).toBeInTheDocument();
      expect(
        screen.getByText("Can't reach the account server"),
      ).toBeInTheDocument();

      await user.click(screen.getByRole("button", { name: "Retry now" }));

      expect(appMocks.SyncNow).toHaveBeenCalledTimes(1);
      await waitFor(() =>
        expect(screen.getByText("Up to date")).toBeInTheDocument(),
      );
    });
  });

  describe("lost changes list (goal ②/③: three reasons + empty state)", () => {
    it("shows the empty-state copy when nothing is lost", async () => {
      appMocks.SyncStatus.mockResolvedValue(IDLE_STATUS);
      render(<SyncPanel />);

      expect(
        await screen.findByText("Every change synced successfully."),
      ).toBeInTheDocument();
    });

    it("lists all three loss reasons with their origin device where applicable", async () => {
      appMocks.SyncStatus.mockResolvedValue({
        ...IDLE_STATUS,
        lostChangeCount: 3,
      });
      appMocks.SyncListLostChanges.mockResolvedValue([
        overwrittenRow(),
        discardedRow(),
        rejectedRow(),
      ]);
      render(<SyncPanel />);

      const rows = await screen.findAllByTestId("lost-change-row");
      expect(rows).toHaveLength(3);

      expect(within(rows[0]).getByText(/Overwritten/)).toBeInTheDocument();
      expect(
        within(rows[0]).getByText(/from "Work Mac mini"/),
      ).toBeInTheDocument();

      expect(
        within(rows[1]).getByText(/Reference missing/),
      ).toBeInTheDocument();
      expect(
        within(rows[1]).getByText(/A dependency didn't arrive in time/),
      ).toBeInTheDocument();

      expect(within(rows[2]).getByText(/Upload timed out/)).toBeInTheDocument();
      expect(
        within(rows[2]).getByText(/the server's version was kept/),
      ).toBeInTheDocument();
    });
  });

  describe("expand + restore (goal ④)", () => {
    it("expands to show the payload content and a restore action with its hint", async () => {
      const user = userEvent.setup();
      appMocks.SyncStatus.mockResolvedValue({
        ...IDLE_STATUS,
        lostChangeCount: 1,
      });
      appMocks.SyncListLostChanges.mockResolvedValue([overwrittenRow()]);
      render(<SyncPanel />);

      const row = await screen.findByTestId("lost-change-row");
      await user.click(within(row).getByRole("button", { name: /Expand/ }));

      // 折叠态标题也含 "agentre-server",用带引号的 JSON 片段专门断言展开出来
      // 的正文,不是折叠标题的巧合命中。
      expect(within(row).getByText(/"agentre-server"/)).toBeInTheDocument();
      expect(
        within(row).getByRole("button", { name: "Restore my version" }),
      ).toBeInTheDocument();
      expect(
        within(row).getByText(
          "Restoring is a normal edit and syncs to your other machines as usual.",
        ),
      ).toBeInTheDocument();
    });

    it("restoring successfully removes the row from the list", async () => {
      const user = userEvent.setup();
      appMocks.SyncStatus.mockResolvedValue({
        ...IDLE_STATUS,
        lostChangeCount: 1,
      });
      appMocks.SyncListLostChanges.mockResolvedValueOnce([overwrittenRow()]);
      appMocks.SyncListLostChanges.mockResolvedValueOnce([]);
      appMocks.SyncRestoreLostChange.mockResolvedValueOnce({
        Restored: true,
        TargetDeleted: false,
      });
      render(<SyncPanel />);

      const row = await screen.findByTestId("lost-change-row");
      await user.click(within(row).getByRole("button", { name: /Expand/ }));
      await user.click(
        within(row).getByRole("button", { name: "Restore my version" }),
      );

      expect(appMocks.SyncRestoreLostChange).toHaveBeenCalledWith(1);
      await waitFor(() =>
        expect(screen.queryByTestId("lost-change-row")).not.toBeInTheDocument(),
      );
    });
  });

  describe("restore hits a tombstone (goal ⑤: R5a)", () => {
    it("switches the row to the deleted state with a create-new action, in place", async () => {
      const user = userEvent.setup();
      appMocks.SyncStatus.mockResolvedValue({
        ...IDLE_STATUS,
        lostChangeCount: 1,
      });
      appMocks.SyncListLostChanges.mockResolvedValue([overwrittenRow()]);
      appMocks.SyncRestoreLostChange.mockResolvedValueOnce({
        Restored: false,
        TargetDeleted: true,
      });
      render(<SyncPanel />);

      const row = await screen.findByTestId("lost-change-row");
      await user.click(within(row).getByRole("button", { name: /Expand/ }));
      await user.click(
        within(row).getByRole("button", { name: "Restore my version" }),
      );

      expect(await within(row).findByText("Deleted")).toBeInTheDocument();
      expect(
        within(row).getByText(
          'This Project was deleted on "Work Mac mini"; restoring won\'t bring it back.',
        ),
      ).toBeInTheDocument();
      expect(
        within(row).getByRole("button", { name: "Create new from this" }),
      ).toBeInTheDocument();
      expect(
        within(row).getByRole("button", { name: "Discard this record" }),
      ).toBeInTheDocument();
      expect(
        within(row).queryByRole("button", { name: "Restore my version" }),
      ).not.toBeInTheDocument();
    });

    it("Create new from this recreates the object and the row disappears", async () => {
      const user = userEvent.setup();
      appMocks.SyncStatus.mockResolvedValue({
        ...IDLE_STATUS,
        lostChangeCount: 1,
      });
      appMocks.SyncListLostChanges.mockResolvedValueOnce([overwrittenRow()]);
      appMocks.SyncListLostChanges.mockResolvedValueOnce([overwrittenRow()]);
      appMocks.SyncListLostChanges.mockResolvedValueOnce([]);
      appMocks.SyncRestoreLostChange.mockResolvedValueOnce({
        Restored: false,
        TargetDeleted: true,
      });
      appMocks.SyncRecreateFromLostChange.mockResolvedValueOnce(undefined);
      render(<SyncPanel />);

      const row = await screen.findByTestId("lost-change-row");
      await user.click(within(row).getByRole("button", { name: /Expand/ }));
      await user.click(
        within(row).getByRole("button", { name: "Restore my version" }),
      );
      await within(row).findByText("Deleted");

      await user.click(
        within(row).getByRole("button", { name: "Create new from this" }),
      );

      expect(appMocks.SyncRecreateFromLostChange).toHaveBeenCalledWith(1);
      await waitFor(() =>
        expect(screen.queryByTestId("lost-change-row")).not.toBeInTheDocument(),
      );
    });

    it("Discard this record drops it without recreating", async () => {
      const user = userEvent.setup();
      appMocks.SyncStatus.mockResolvedValue({
        ...IDLE_STATUS,
        lostChangeCount: 1,
      });
      appMocks.SyncListLostChanges.mockResolvedValueOnce([overwrittenRow()]);
      appMocks.SyncListLostChanges.mockResolvedValueOnce([overwrittenRow()]);
      appMocks.SyncListLostChanges.mockResolvedValueOnce([]);
      appMocks.SyncRestoreLostChange.mockResolvedValueOnce({
        Restored: false,
        TargetDeleted: true,
      });
      appMocks.SyncDiscardLostChange.mockResolvedValueOnce(undefined);
      render(<SyncPanel />);

      const row = await screen.findByTestId("lost-change-row");
      await user.click(within(row).getByRole("button", { name: /Expand/ }));
      await user.click(
        within(row).getByRole("button", { name: "Restore my version" }),
      );
      await within(row).findByText("Deleted");

      await user.click(
        within(row).getByRole("button", { name: "Discard this record" }),
      );

      expect(appMocks.SyncDiscardLostChange).toHaveBeenCalledWith(1);
      expect(appMocks.SyncRecreateFromLostChange).not.toHaveBeenCalled();
      await waitFor(() =>
        expect(screen.queryByTestId("lost-change-row")).not.toBeInTheDocument(),
      );
    });
  });
});
