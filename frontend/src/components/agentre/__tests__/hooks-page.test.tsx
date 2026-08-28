import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

// Hoist mocks so the module factory can reference them (vi.mock is hoisted
// above imports); follows the shape established by
// remote-devices/device-providers-sync.test.tsx.
const appMocks = vi.hoisted(() => ({
  LoadHooks: vi.fn(),
  CreateHook: vi.fn(),
  UpdateHook: vi.fn(),
  DeleteHook: vi.fn(),
  ToggleHook: vi.fn(),
  RunHook: vi.fn(),
  ProbeInterpreters: vi.fn(),
}));

vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

import { HooksPage } from "../hooks-page";
import {
  CreateHook,
  DeleteHook,
  LoadHooks,
  ProbeInterpreters,
  RunHook,
  ToggleHook,
  UpdateHook,
} from "../../../../wailsjs/go/app/App";

const mockLoadHooks = LoadHooks as unknown as ReturnType<typeof vi.fn>;
const mockCreateHook = CreateHook as unknown as ReturnType<typeof vi.fn>;
const mockUpdateHook = UpdateHook as unknown as ReturnType<typeof vi.fn>;
const mockDeleteHook = DeleteHook as unknown as ReturnType<typeof vi.fn>;
const mockToggleHook = ToggleHook as unknown as ReturnType<typeof vi.fn>;
const mockRunHook = RunHook as unknown as ReturnType<typeof vi.fn>;
const mockProbeInterpreters = ProbeInterpreters as unknown as ReturnType<
  typeof vi.fn
>;

type AnyRecord = Record<string, unknown>;

function makeHook(over: AnyRecord = {}) {
  return {
    id: 2,
    name: "Jira urgent",
    interpreter: "bash",
    command: "echo '{\"events\":[]}'",
    scheduleExpr: "*/5 * * * *",
    timezone: "Asia/Shanghai",
    env: [{ key: "JIRA_TOKEN", value: "********", secret: true }],
    enabled: true,
    nextRunAt: 0,
    lastRunAt: Date.now() - 120_000,
    lastStatus: "ok",
    lastError: "",
    lastDurationMs: 412,
    totalCount: 37,
    createtime: 0,
    updatetime: 0,
    ...over,
  };
}

const sampleEvent = {
  id: 100,
  hookId: 2,
  title: "payment callback timeout",
  dedupeKey: "OPS-4821",
  payloadJson: '{"severity":"high"}',
  receivedAt: Date.now() - 60_000,
  createtime: 0,
};

/**
 * Resets and re-primes the mocked wailsjs bindings for a test. Mirrors the
 * old setBridge() helper's shape and default fixtures, just wired to the
 * imported mock functions instead of a window.go stub.
 */
type BridgeOverrides = {
  LoadHooks?: (...args: unknown[]) => unknown;
  CreateHook?: (...args: unknown[]) => unknown;
  UpdateHook?: (...args: unknown[]) => unknown;
  DeleteHook?: (...args: unknown[]) => unknown;
  ToggleHook?: (...args: unknown[]) => unknown;
  RunHook?: (...args: unknown[]) => unknown;
  ProbeInterpreters?: (...args: unknown[]) => unknown;
};

function setBridge(over: BridgeOverrides = {}) {
  vi.clearAllMocks();
  const hookA = makeHook();
  const hookB = makeHook({
    id: 3,
    name: "RSS advisories",
    interpreter: "node",
    enabled: false,
    lastStatus: "",
  });
  mockLoadHooks.mockResolvedValue({
    hooks: [hookA, hookB],
    events: [sampleEvent],
  });
  mockCreateHook.mockImplementation((req: AnyRecord) =>
    Promise.resolve(makeHook({ id: 9, ...req })),
  );
  mockUpdateHook.mockImplementation((req: AnyRecord) =>
    Promise.resolve(makeHook(req)),
  );
  mockDeleteHook.mockResolvedValue(undefined);
  mockToggleHook.mockImplementation((id: number, enabled: boolean) =>
    Promise.resolve(makeHook({ id, enabled })),
  );
  mockRunHook.mockResolvedValue({
    exitCode: 0,
    durationMs: 412,
    timedOut: false,
    stdout: '{"events":[]}',
    stderr: "",
    parseError: "",
    events: [sampleEvent],
    newCount: 1,
    dupCount: 1,
    persisted: false,
  });
  mockProbeInterpreters.mockResolvedValue([
    { key: "bash", path: "/bin/bash", installed: true },
    { key: "node", path: "/usr/bin/node", installed: true },
    { key: "python", path: "/usr/bin/python3", installed: true },
    { key: "pwsh", path: "", installed: false },
  ]);
  // Apply any per-test overrides on top of the defaults above.
  if (over.LoadHooks) mockLoadHooks.mockImplementation(over.LoadHooks);
  if (over.CreateHook) mockCreateHook.mockImplementation(over.CreateHook);
  if (over.UpdateHook) mockUpdateHook.mockImplementation(over.UpdateHook);
  if (over.DeleteHook) mockDeleteHook.mockImplementation(over.DeleteHook);
  if (over.ToggleHook) mockToggleHook.mockImplementation(over.ToggleHook);
  if (over.RunHook) mockRunHook.mockImplementation(over.RunHook);
  if (over.ProbeInterpreters)
    mockProbeInterpreters.mockImplementation(over.ProbeInterpreters);
  return {
    LoadHooks: mockLoadHooks,
    CreateHook: mockCreateHook,
    UpdateHook: mockUpdateHook,
    DeleteHook: mockDeleteHook,
    ToggleHook: mockToggleHook,
    RunHook: mockRunHook,
    ProbeInterpreters: mockProbeInterpreters,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("HooksPage", () => {
  it("grows to fill the available width as a flex child (flex-1)", async () => {
    setBridge();
    const { container } = render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    // The page mounts directly into AppLayout's horizontal flex row, so its
    // root must grow (flex-1) to fill the space left of the nav rail — without
    // it the page collapses to its content width and leaves a gap on the right.
    const root = container.firstChild as HTMLElement;
    expect(root).toHaveClass("flex-1");
    expect(root).toHaveClass("min-w-0");
  });

  it("loads and lists hooks, auto-selecting the first into the header", async () => {
    setBridge();
    render(<HooksPage />);

    // both hooks appear in the list
    expect(await screen.findAllByText("Jira urgent")).not.toHaveLength(0);
    expect(screen.getByText("RSS advisories")).toBeInTheDocument();
    // disabled hook shows the "Off" label instead of a status dot
    expect(screen.getByText("Off")).toBeInTheDocument();
    // header reflects the selected hook + kind + tabs
    expect(screen.getByText("Bash Hook")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Script" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /Run Log/ })).toBeInTheDocument();
  });

  it("renders hook timestamps as millisecond epochs", async () => {
    // lastRunAt / receivedAt 与库里其它时间列同为毫秒 epoch。按秒解读会让
    // Date.now()/1000 - <毫秒值> 变成一个巨大负数,被 Math.max(0, …) 夹成 0,
    // 于是每一条「上次运行 / 收到于」都固定显示成刚刚发生。
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    expect(screen.getByText(/2m ago/)).toBeInTheDocument();
  });

  it("edits cron + command and saves via UpdateHook", async () => {
    const app = setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    fireEvent.change(screen.getByLabelText("cron expression"), {
      target: { value: "0 * * * *" },
    });
    fireEvent.change(screen.getByLabelText("Script"), {
      target: { value: "echo hi" },
    });
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(app.UpdateHook).toHaveBeenCalled());
    expect(app.UpdateHook).toHaveBeenCalledWith(
      expect.objectContaining({
        id: 2,
        scheduleExpr: "0 * * * *",
        command: "echo hi",
      }),
    );
  });

  it("adds a secret env var that round-trips into the save payload", async () => {
    const app = setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(screen.getByRole("button", { name: "Add variable" }));
    const keyInputs = screen.getAllByLabelText("KEY");
    const valueInputs = screen.getAllByLabelText("value");
    fireEvent.change(keyInputs[keyInputs.length - 1], {
      target: { value: "API_KEY" },
    });
    fireEvent.change(valueInputs[valueInputs.length - 1], {
      target: { value: "s3cr3t" },
    });
    const secretSwitches = screen.getAllByLabelText("Secret");
    await userEvent.click(secretSwitches[secretSwitches.length - 1]);

    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(app.UpdateHook).toHaveBeenCalled());
    const arg = app.UpdateHook.mock.calls[0][0] as { env: Array<AnyRecord> };
    expect(arg.env).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          key: "API_KEY",
          value: "s3cr3t",
          secret: true,
        }),
      ]),
    );
  });

  it("dry-runs and shows the inline result", async () => {
    const app = setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(screen.getByRole("button", { name: "Dry run" }));
    await waitFor(() =>
      expect(app.RunHook).toHaveBeenCalledWith({ id: 2, dryRun: true }),
    );
    expect(await screen.findByText("Dry run · exit 0")).toBeInTheDocument();
    expect(screen.getByText("412ms · not persisted")).toBeInTheDocument();
  });

  it("shows produced events in the Run Log tab with payload detail", async () => {
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(screen.getByRole("tab", { name: /Run Log/ }));
    // first event auto-selects → its title shows in both list + detail
    expect(
      (await screen.findAllByText("payment callback timeout")).length,
    ).toBeGreaterThan(0);
    expect(screen.getByText(/OPS-4821/)).toBeInTheDocument();
    expect(screen.getByText(/"severity":"high"/)).toBeInTheDocument();
  });

  it("flags failure events distinctly and keeps the failure log in the payload", async () => {
    const failureEvent = {
      id: 200,
      hookId: 2,
      kind: "failure",
      title: "execution timed out",
      dedupeKey: "",
      payloadJson:
        '{"exitCode":124,"timedOut":true,"stderr":"deadline exceeded"}',
      receivedAt: Date.now() - 30_000,
      createtime: 0,
    };
    setBridge({
      LoadHooks: () =>
        Promise.resolve({
          hooks: [makeHook()],
          events: [failureEvent, sampleEvent],
        }),
    });
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");
    await userEvent.click(screen.getByRole("tab", { name: /Run Log/ }));

    // The failure row is marked "Failed" (so it reads apart from script output)…
    expect(
      (await screen.findAllByText("execution timed out")).length,
    ).toBeGreaterThan(0);
    expect(screen.getAllByText("Failed").length).toBeGreaterThan(0);
    // …and the retained stderr is inspectable in the payload detail.
    expect(screen.getByText(/deadline exceeded/)).toBeInTheDocument();
    // Plain output rows carry no failure marker.
    expect(screen.getByText("payment callback timeout")).toBeInTheDocument();
  });

  it("toggles the selected hook", async () => {
    const app = setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(screen.getByRole("button", { name: "Disable" }));
    await waitFor(() => expect(app.ToggleHook).toHaveBeenCalledWith(2, false));
  });

  it("creates a new hook from the + button", async () => {
    const app = setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(screen.getByRole("button", { name: "New Hook" }));
    expect(screen.getByLabelText("Name")).toHaveValue("New Hook");
    await userEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(app.CreateHook).toHaveBeenCalled());
  });
});

describe("HooksPage interpreter dropdown (probe-driven)", () => {
  it("lists probed interpreters and disables not-installed ones", async () => {
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    // Open the interpreter select
    fireEvent.click(screen.getByRole("combobox", { name: "Interpreter" }));

    // Wait for the probed options to appear
    const pwshOption = await screen.findByText("PowerShell");
    // The not-installed option should be rendered inside a disabled SelectItem
    const optionEl = pwshOption.closest("[data-disabled]");
    expect(optionEl).not.toBeNull();
  });

  it("shows not-installed label next to uninstalled interpreter", async () => {
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    fireEvent.click(screen.getByRole("combobox", { name: "Interpreter" }));
    await screen.findByText("PowerShell");
    expect(await screen.findByText("Not installed")).toBeInTheDocument();
  });

  it("submits interpreterPath in create payload", async () => {
    const app = setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    // Click the + button to open the create form
    fireEvent.click(screen.getByTestId("hook-create"));

    // Fill in the binary path field
    const pathInput = await screen.findByLabelText("Binary path");
    fireEvent.change(pathInput, { target: { value: "/opt/py/bin/python3" } });

    // Submit
    fireEvent.click(screen.getByTestId("hook-save"));
    await waitFor(() =>
      expect(app.CreateHook).toHaveBeenCalledWith(
        expect.objectContaining({ interpreterPath: "/opt/py/bin/python3" }),
      ),
    );
  });
});
