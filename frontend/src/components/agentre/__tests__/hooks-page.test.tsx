import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
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

const sonnerMocks = vi.hoisted(() => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

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

  it("presents one content sheet with the index column on chrome and no separator lines", async () => {
    setBridge();
    const { container } = render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    const main = container.querySelector("main")!;
    expect(main.className).toContain("content-sheet");
    const aside = container.querySelector("aside")!;
    expect(aside.className).toContain("bg-chrome");
    expect(aside.className).not.toContain("bg-sidebar");
    // 开关的边按规格保留。
    const structural = Array.from(
      container.querySelectorAll("*:not([role='switch'])"),
    )
      .flatMap((el) => (el.getAttribute("class") ?? "").split(/\s+/))
      .filter((token) => /^(?:border(?:-[tblrxy])?|divide-.*)$/.test(token));
    expect(structural).toEqual([]);
  });

  it("marks the selected row with the raised surface and no colour bar", async () => {
    setBridge();
    const { container } = render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    const selected = container.querySelector('aside [aria-current="true"]')!;
    expect(selected.className).toContain("bg-raised");
    expect(selected.className).not.toContain("border-l");
  });

  it("renders the Script / Run Log switch as a tablist with arrow-key movement", async () => {
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    const tablist = screen.getByRole("tablist");
    expect(tablist).toHaveAttribute("data-slot", "segmented-control");
    const script = screen.getByRole("tab", { name: "Script" });
    expect(script).toHaveAttribute("aria-selected", "true");
    script.focus();
    await userEvent.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: /Run Log/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
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

/** 把当前草稿改脏：脚本正文换成一段没保存过的内容。 */
function editScript(value = "echo edited") {
  fireEvent.change(screen.getByLabelText("Script"), { target: { value } });
}

function sidebarRow(name: string): HTMLElement {
  const aside = document.querySelector("aside")!;
  return within(aside as HTMLElement)
    .getByText(name)
    .closest("button")!;
}

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

describe("HooksPage unsaved draft", () => {
  it("marks the edited hook's row with a dot only while the draft differs from the saved hook", async () => {
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    expect(screen.queryByTitle("Unsaved changes")).toBeNull();

    editScript();
    expect(
      within(sidebarRow("Jira urgent")).getByTitle("Unsaved changes"),
    ).toBeInTheDocument();
    expect(
      within(sidebarRow("RSS advisories")).queryByTitle("Unsaved changes"),
    ).toBeNull();

    // 改回已保存的内容就不再是脏的。
    editScript("echo '{\"events\":[]}'");
    expect(screen.queryByTitle("Unsaved changes")).toBeNull();
  });

  it("asks before switching away from an edited hook and stays put on Keep editing", async () => {
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");
    editScript();

    await userEvent.click(sidebarRow("RSS advisories"));
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).getByText("Discard changes to “Jira urgent”?"),
    ).toBeInTheDocument();
    expect(screen.getByText("Bash Hook")).toBeInTheDocument();

    await userEvent.click(
      within(dialog).getByRole("button", { name: "Keep editing" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(screen.getByText("Bash Hook")).toBeInTheDocument();
    expect(screen.getByLabelText("Script")).toHaveValue("echo edited");
  });

  it("switches to the other hook only after Discard changes", async () => {
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");
    editScript();

    await userEvent.click(sidebarRow("RSS advisories"));
    await userEvent.click(
      await screen.findByRole("button", { name: "Discard changes" }),
    );

    expect(await screen.findByText("Node Hook")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByTitle("Unsaved changes")).toBeNull();
  });

  it("asks before + replaces an edited draft with a new hook", async () => {
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");
    editScript();

    await userEvent.click(screen.getByTestId("hook-create"));
    expect(
      await screen.findByText("Discard changes to “Jira urgent”?"),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Name")).toHaveValue("Jira urgent");

    await userEvent.click(
      screen.getByRole("button", { name: "Discard changes" }),
    );
    await waitFor(() =>
      expect(screen.getByLabelText("Name")).toHaveValue("New Hook"),
    );
  });

  it("asks before leaving an edited new hook, but not an untouched one", async () => {
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(screen.getByTestId("hook-create"));
    await userEvent.click(sidebarRow("RSS advisories"));
    expect(await screen.findByText("Node Hook")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();

    await userEvent.click(screen.getByTestId("hook-create"));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Nightly sweep" },
    });
    await userEvent.click(sidebarRow("Jira urgent"));
    expect(
      await screen.findByText("Discard changes to “Nightly sweep”?"),
    ).toBeInTheDocument();
  });

  it("switches without asking when nothing was edited", async () => {
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(sidebarRow("RSS advisories"));
    expect(await screen.findByText("Node Hook")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("toggling changes only the hook's enabled state and keeps the edited draft", async () => {
    const app = setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");
    editScript();

    await userEvent.click(screen.getByRole("button", { name: "Disable" }));

    expect(
      await screen.findByRole("button", { name: "Enable" }),
    ).toBeInTheDocument();
    expect(app.ToggleHook).toHaveBeenCalledWith(2, false);
    expect(screen.getByLabelText("Script")).toHaveValue("echo edited");
    expect(
      within(sidebarRow("Jira urgent")).getByTitle("Unsaved changes"),
    ).toBeInTheDocument();
    expect(sonnerMocks.toast.success).toHaveBeenCalledWith("Disabled");

    // 草稿随后保存时带的是启停后的状态，不把它改回去。
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(app.UpdateHook).toHaveBeenCalledWith(
        expect.objectContaining({ command: "echo edited", enabled: false }),
      ),
    );
  });

  it("reports a failed toggle without touching the draft", async () => {
    setBridge({ ToggleHook: () => Promise.reject(new Error("db locked")) });
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");
    editScript();

    await userEvent.click(screen.getByRole("button", { name: "Disable" }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    expect(lastErrorToast()).toEqual({
      title: "Disable failed",
      detail: "db locked",
    });
    expect(screen.getByRole("button", { name: "Disable" })).toBeInTheDocument();
    expect(screen.getByLabelText("Script")).toHaveValue("echo edited");
  });
});

describe("HooksPage save and run", () => {
  it("saves the edited draft first and then dry-runs it", async () => {
    const app = setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    expect(screen.queryByRole("button", { name: "Save and run" })).toBeNull();
    editScript();
    expect(screen.queryByRole("button", { name: "Dry run" })).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Save and run" }));

    await waitFor(() =>
      expect(app.RunHook).toHaveBeenCalledWith({ id: 2, dryRun: true }),
    );
    expect(app.UpdateHook).toHaveBeenCalledWith(
      expect.objectContaining({ id: 2, command: "echo edited" }),
    );
    expect(app.UpdateHook.mock.invocationCallOrder[0]).toBeLessThan(
      app.RunHook.mock.invocationCallOrder[0],
    );
    expect(await screen.findByText("Dry run · exit 0")).toBeInTheDocument();
  });

  it("creates an edited new hook and runs the hook that was just created", async () => {
    const app = setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(screen.getByTestId("hook-create"));
    expect(screen.getByRole("button", { name: "Dry run" })).toBeDisabled();
    editScript("echo new");
    await userEvent.click(screen.getByRole("button", { name: "Save and run" }));

    await waitFor(() =>
      expect(app.RunHook).toHaveBeenCalledWith({ id: 9, dryRun: true }),
    );
    expect(app.CreateHook).toHaveBeenCalledWith(
      expect.objectContaining({ command: "echo new" }),
    );
  });

  it("does not run when the save fails", async () => {
    const app = setBridge({
      UpdateHook: () => Promise.reject(new Error("disk full")),
    });
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");
    editScript();

    await userEvent.click(screen.getByRole("button", { name: "Save and run" }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    expect(lastErrorToast()).toEqual({
      title: "Save failed",
      detail: "disk full",
    });
    expect(app.RunHook).not.toHaveBeenCalled();
    // 草稿还在，按钮仍是「保存并运行」。
    expect(screen.getByLabelText("Script")).toHaveValue("echo edited");
    expect(
      screen.getByRole("button", { name: "Save and run" }),
    ).toBeInTheDocument();
  });
});

describe("HooksPage result feedback", () => {
  it("reports a save through a toast, with no banner on the page", async () => {
    setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");
    editScript();

    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
        "Saved Hook Jira urgent",
      ),
    );
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByText("Saved Hook Jira urgent")).toBeNull();
  });

  it("says the delete failed when a delete fails", async () => {
    setBridge({ DeleteHook: () => Promise.reject(new Error("fk violation")) });
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(screen.getByRole("button", { name: "More actions" }));
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Delete" }),
    );
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    expect(lastErrorToast()).toEqual({
      title: "Delete failed",
      detail: "fk violation",
    });
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("reports a successful delete through a toast", async () => {
    const app = setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(screen.getByRole("button", { name: "More actions" }));
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Delete" }),
    );
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() => expect(app.DeleteHook).toHaveBeenCalledWith(2));
    await waitFor(() =>
      expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
        "Deleted Hook Jira urgent",
      ),
    );
  });

  it("does not call a delete failed when only the list refresh after it fails", async () => {
    const app = setBridge();
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");
    app.LoadHooks.mockImplementation(() => Promise.reject(new Error("busy")));

    await userEvent.click(screen.getByRole("button", { name: "More actions" }));
    await userEvent.click(
      await screen.findByRole("menuitem", { name: "Delete" }),
    );
    await userEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() =>
      expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
        "Deleted Hook Jira urgent",
      ),
    );
    await waitFor(() => expect(screen.queryByText("Jira urgent")).toBeNull());
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
    expect(screen.getByText("RSS advisories")).toBeInTheDocument();
  });

  it("reports a run that could not start, keeping the raw error folded", async () => {
    setBridge({ RunHook: () => Promise.reject(new Error("spawn ENOENT")) });
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(screen.getByRole("button", { name: "Dry run" }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    expect(lastErrorToast()).toEqual({
      title: "Run failed",
      detail: "spawn ENOENT",
    });
  });

  it("reports a run that exited non-zero, keeping stderr folded", async () => {
    setBridge({
      RunHook: () =>
        Promise.resolve({
          exitCode: 2,
          durationMs: 5,
          timedOut: false,
          stdout: "",
          stderr: "jq: parse error",
          parseError: "",
          events: [],
          newCount: 0,
          dupCount: 0,
          persisted: false,
        }),
    });
    render(<HooksPage />);
    await screen.findAllByText("Jira urgent");

    await userEvent.click(screen.getByRole("button", { name: "Dry run" }));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    expect(lastErrorToast()).toEqual({
      title: "Run failed",
      detail: "jq: parse error",
    });
  });
});

describe("HooksPage load failure", () => {
  it("shows its own state with Retry instead of the empty-state create prompt", async () => {
    const app = setBridge({
      LoadHooks: () => Promise.reject(new Error("database is locked")),
    });
    render(<HooksPage />);

    expect(await screen.findByText("Hooks didn't load")).toBeInTheDocument();
    expect(screen.queryByText("No Hooks yet")).toBeNull();
    expect(screen.queryByRole("button", { name: "New Hook" })).toBeNull();
    expect(screen.queryByText(/Save failed/)).toBeNull();
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();

    app.LoadHooks.mockImplementation(() =>
      Promise.resolve({ hooks: [makeHook()], events: [] }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findAllByText("Jira urgent")).not.toHaveLength(0);
    expect(screen.queryByText("Hooks didn't load")).toBeNull();
  });

  it("keeps the empty state for a list that loaded with no hooks", async () => {
    setBridge({ LoadHooks: () => Promise.resolve({ hooks: [], events: [] }) });
    render(<HooksPage />);

    expect((await screen.findAllByText("No Hooks yet")).length).toBeGreaterThan(
      0,
    );
    expect(screen.queryByText("Hooks didn't load")).toBeNull();
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
