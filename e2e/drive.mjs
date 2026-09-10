// The verification driver: one action per invocation against the browser `verify.mjs up` left
// running. This is what replaces authoring a throwaway spec for a one-off check — you look,
// act, look again, and every call is appended to the scenario's drive.log as it happens.
//
//   node e2e/drive.mjs snapshot                       # what is on screen, and how to address it
//   node e2e/drive.mjs click "testid=nav-settings"
//   node e2e/drive.mjs shot 01-settings
//   node e2e/drive.mjs sql "select status, count(*) from chat_sessions group by status"
//
// Isolation is not this file's judgement call: the session it attaches to was created by the
// launcher, and every URL it touches goes through assertSanctionedURL (lib/target.mjs).
import { appendFileSync, existsSync, mkdirSync, readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { applyVerificationViewport } from "./lib/browser.mjs";
import {
  IsolationError,
  assertSanctionedURL,
  requireLiveSession,
  resolveTarget,
} from "./lib/target.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const DEFAULT_TIMEOUT = 15_000;
const READ_ONLY_SQL = /^\s*(select|with|pragma|explain)\b/i;

function parseArgs(argv) {
  const positional = [];
  const flags = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--scenario") flags.scenario = argv[++i];
    else if (arg === "--nth") flags.nth = Number(argv[++i]);
    else if (arg === "--state") flags.state = argv[++i];
    else if (arg === "--timeout") flags.timeout = Number(argv[++i]);
    else if (arg === "--limit") flags.limit = Number(argv[++i]);
    else if (arg === "--full") flags.full = true;
    else if (arg.startsWith("--")) throw new Error(`unknown flag: ${arg}`);
    else positional.push(arg);
  }
  return { command: positional[0], rest: positional.slice(1), flags };
}

/** Where this run's evidence lands. One scenario, one directory — docs/verification.md. */
function scenarioDirs(flags) {
  const slug = flags.scenario || process.env.AGENTRE_VERIFY_SCENARIO || "_unscoped";
  const root = join(here, "scratch", slug);
  const dirs = {
    slug,
    root,
    logs: join(root, "logs"),
    screenshots: join(root, "screenshots"),
    resources: join(root, "resources"),
  };
  mkdirSync(dirs.logs, { recursive: true });
  mkdirSync(dirs.screenshots, { recursive: true });
  return dirs;
}

function record(dirs, line) {
  appendFileSync(join(dirs.logs, "drive.log"), `${line}\n`);
}

/**
 * `testid=x` / `role=button[name="Save"]` / `text=…` / `label=…` / `placeholder=…`, or any
 * Playwright selector. Prefer testid: visible text is i18n'd and moves.
 */
function locate(page, spec, flags) {
  let locator;
  const eq = spec.indexOf("=");
  const kind = eq > 0 ? spec.slice(0, eq) : "";
  const value = eq > 0 ? spec.slice(eq + 1) : spec;
  switch (kind) {
    case "testid":
      locator = page.getByTestId(value);
      break;
    case "role": {
      const match = /^([a-z]+)(?:\[name=(?:"([^"]*)"|'([^']*)')\])?$/.exec(value);
      if (!match) throw new Error(`bad role locator: ${spec} (expected role=button[name="Save"])`);
      const name = match[2] ?? match[3];
      locator = page.getByRole(match[1], name === undefined ? {} : { name });
      break;
    }
    case "text":
      locator = page.getByText(value);
      break;
    case "label":
      locator = page.getByLabel(value);
      break;
    case "placeholder":
      locator = page.getByPlaceholder(value);
      break;
    default:
      locator = page.locator(spec);
  }
  return Number.isInteger(flags.nth) ? locator.nth(flags.nth) : locator;
}

async function attach(session) {
  const target = resolveTarget();
  // A dead app and a blank page look identical through CDP: the browser answers, the page is
  // empty, and a snapshot reports "0 elements" as if the UI were simply bare. Ask the bridge
  // first so the failure names itself.
  try {
    await fetch(target.baseURL, { signal: AbortSignal.timeout(3000) });
  } catch {
    throw new Error(
      `the verification app is not serving ${target.baseURL} any more.\n` +
        "Check it with `make verify-status`, and use `node e2e/drive.mjs logs` " +
        "for why it stopped (a `wails dev` app restarts on Go/TS edits and dies if its Vite process is killed).",
    );
  }
  const { chromium } = await import("@playwright/test");
  let browser;
  try {
    browser = await chromium.connectOverCDP(session.cdpURL);
  } catch (err) {
    throw new Error(
      `cannot reach the verification browser at ${session.cdpURL} — is it still up? ` +
        `(make verify-status)\n${err.message}`,
    );
  }
  const context = browser.contexts()[0];
  if (!context) throw new Error("the verification browser has no context");
  const pages = context.pages();
  const page =
    pages.find((p) => p.url().startsWith(target.baseURL)) || pages[0] || (await context.newPage());
  await applyVerificationViewport(page);
  return { browser, page, target };
}

const COMMANDS = {
  async goto({ page, target, session, rest }) {
    const path = rest[0] ?? "/";
    const url = assertSanctionedURL(target, new URL(path, target.baseURL).toString());
    await page.goto(url, { waitUntil: "domcontentloaded" });
    return `at ${page.url()}`;
  },

  async click({ page, rest, flags }) {
    await locate(page, rest[0], flags).click({ timeout: flags.timeout ?? DEFAULT_TIMEOUT });
    return `clicked ${rest[0]} (now at ${page.url()})`;
  },

  async hover({ page, rest, flags }) {
    // 悬停是一个**独立动作**，因为有整类控件只在父行 hover 时才渲染（会话索引里
    // 的项目菜单、「新建随手对话」等）。对它们直接 click 会被 Playwright 判成不可
    // 操作；先 hover 父行、下一条命令再 click 子控件即可——浏览器实例在两次
    // drive 调用之间保留鼠标位置。
    await locate(page, rest[0], flags).hover({ timeout: flags.timeout ?? DEFAULT_TIMEOUT });
    return `hovered ${rest[0]}`;
  },

  async fill({ page, rest, flags }) {
    await locate(page, rest[0], flags).fill(rest.slice(1).join(" "), {
      timeout: flags.timeout ?? DEFAULT_TIMEOUT,
    });
    return `filled ${rest[0]}`;
  },

  async press({ page, rest, flags }) {
    // `press <key>` types into whatever has focus; `press <locator> <key>` targets an element.
    if (rest.length === 1) {
      await page.keyboard.press(rest[0]);
      return `pressed ${rest[0]}`;
    }
    await locate(page, rest[0], flags).press(rest[1], { timeout: flags.timeout ?? DEFAULT_TIMEOUT });
    return `pressed ${rest[1]} on ${rest[0]}`;
  },

  async wait({ page, rest, flags }) {
    await locate(page, rest[0], flags).waitFor({
      state: flags.state ?? "visible",
      timeout: flags.timeout ?? DEFAULT_TIMEOUT,
    });
    return `${rest[0]} is ${flags.state ?? "visible"}`;
  },

  /** What is on screen and how to address it. The first thing to run when something surprises you. */
  async snapshot({ page, rest, flags }) {
    const items = await page.evaluate(
      ({ rootSel, limit }) => {
        const root = rootSel ? document.querySelector(rootSel) : document.body;
        if (!root) return null;
        const SELECTOR =
          'button, a[href], input, select, textarea, [role], [data-testid], [contenteditable="true"], h1, h2, h3, [aria-label]';
        const out = [];
        const seen = new Set();
        for (const el of root.querySelectorAll(SELECTOR)) {
          if (out.length >= limit) break;
          const rect = el.getBoundingClientRect();
          const style = getComputedStyle(el);
          if (rect.width === 0 || rect.height === 0) continue;
          if (style.visibility === "hidden" || style.display === "none" || style.opacity === "0") continue;
          const testid = el.getAttribute("data-testid") || "";
          const role = el.getAttribute("role") || el.tagName.toLowerCase();
          const name = (el.getAttribute("aria-label") || el.getAttribute("placeholder") || el.innerText || el.value || "")
            .trim()
            .replace(/\s+/g, " ")
            .slice(0, 70);
          const key = `${role}|${testid}|${name}`;
          if (seen.has(key)) continue;
          seen.add(key);
          const state = [];
          if (el.disabled) state.push("disabled");
          if (el.getAttribute("aria-selected") === "true") state.push("selected");
          if (el.dataset.active === "true") state.push("active");
          const expanded = el.getAttribute("aria-expanded");
          if (expanded !== null) state.push(`expanded=${expanded}`);
          out.push({ role, testid, name, state: state.join(","), y: Math.round(rect.top), x: Math.round(rect.left) });
        }
        return out;
      },
      { rootSel: rest[0] ?? null, limit: flags.limit ?? 120 },
    );
    if (items === null) throw new Error(`no element matches ${rest[0]}`);
    items.sort((a, b) => a.y - b.y || a.x - b.x);
    const lines = items.map((it) => {
      const addr = it.testid ? `testid=${it.testid}` : `role=${it.role}`;
      return `  ${addr.padEnd(42)} [${it.role}] ${it.name}${it.state ? `  (${it.state})` : ""}`;
    });
    console.log(`${page.url()}\n${lines.join("\n")}`);
    return `${items.length} elements`;
  },

  async text({ page, rest, flags }) {
    const locator = rest[0] ? locate(page, rest[0], flags) : page.locator("body");
    const value = (await locator.innerText({ timeout: flags.timeout ?? DEFAULT_TIMEOUT })).trim();
    console.log(value.slice(0, 4000));
    return `${value.length} chars`;
  },

  async shot({ page, rest, flags, dirs }) {
    const name = (rest[0] || "shot").replace(/[^\w.-]/g, "-");
    const path = join(dirs.screenshots, `${name}.png`);
    const shooter = rest[1] ? locate(page, rest[1], flags) : page;
    await shooter.screenshot({ path, fullPage: rest[1] ? undefined : Boolean(flags.full) });
    console.log(path);
    return `saved ${path}`;
  },

  async eval({ page, rest }) {
    const result = await page.evaluate(rest.join(" "));
    console.log(JSON.stringify(result, null, 2));
    return "evaluated";
  },

  /**
   * The independent oracle: read the DB the app wrote, not the UI it rendered. Read-only —
   * a verification run observes state, it does not manufacture it.
   */
  async sql({ session, rest }) {
    const query = rest.join(" ");
    if (!READ_ONLY_SQL.test(query)) {
      throw new Error("only SELECT / WITH / PRAGMA / EXPLAIN are allowed — the oracle is read-only");
    }
    if (!existsSync(session.dbPath)) throw new Error(`no database at ${session.dbPath}`);
    const { DatabaseSync } = await import("node:sqlite");
    const db = new DatabaseSync(session.dbPath, { readOnly: true });
    try {
      db.exec("PRAGMA busy_timeout = 5000");
      const rows = db.prepare(query).all();
      console.log(JSON.stringify(rows, null, 2));
      return `${rows.length} rows`;
    } finally {
      db.close();
    }
  },

  async logs({ session, rest, flags }) {
    const lines = Number(rest[0]) || flags.limit || 40;
    const files = [session.logFile];
    const logDir = join(session.dataDir, "logs");
    if (existsSync(logDir)) {
      for (const name of readdirSync(logDir)) files.push(join(logDir, name));
    }
    for (const file of files) {
      if (!existsSync(file) || !statSync(file).isFile()) continue;
      const tail = readFileSync(file, "utf8").split("\n").slice(-lines).join("\n");
      console.log(`--- ${file}\n${tail}`);
    }
    return `tailed ${files.length} files`;
  },
};

async function main() {
  const { command, rest, flags } = parseArgs(process.argv.slice(2));
  if (!command || !COMMANDS[command]) {
    console.error(
      `usage: node drive.mjs <${Object.keys(COMMANDS).join("|")}> [args] ` +
        "[--scenario <slug>] [--nth N] [--state visible|hidden] [--timeout ms] [--full]",
    );
    return 2;
  }
  const session = requireLiveSession();
  const dirs = scenarioDirs(flags);
  const started = new Date();
  const printable = [command, ...rest].join(" ");

  let ctx = null;
  try {
    // `sql` and `logs` read files the app owns; they need no browser and must keep working
    // after the browser is gone (e.g. while writing up the report).
    if (command !== "sql" && command !== "logs") ctx = await attach(session);
    const summary = await COMMANDS[command]({ ...ctx, session, rest, flags, dirs });
    record(dirs, `${started.toISOString()} drive ${printable}\n    ok: ${summary}`);
    return 0;
  } catch (err) {
    record(dirs, `${started.toISOString()} drive ${printable}\n    FAILED: ${err.message.split("\n")[0]}`);
    console.error(err instanceof IsolationError ? `${err.name}: ${err.message}` : err.message);
    if (ctx) console.error("hint: `node e2e/drive.mjs snapshot` shows what is actually on screen");
    return 1;
  } finally {
    // Disconnect only this client; the browser and the app stay up for the next call.
    if (ctx) await ctx.browser.close().catch(() => {});
  }
}

main().then((code) => process.exit(code));
