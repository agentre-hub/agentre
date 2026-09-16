import assert from "node:assert/strict";
import test from "node:test";

import {
  VERIFY_VIEWPORT,
  applyVerificationViewport,
  verificationBrowserArgs,
} from "./browser.mjs";

test("Given a verification page, When its viewport is prepared, Then screenshots use the standard 1440x900 viewport", async () => {
  const calls = [];
  const page = {
    setViewportSize(size) {
      calls.push(size);
    },
  };

  await applyVerificationViewport(page);

  assert.deepEqual(VERIFY_VIEWPORT, { width: 1440, height: 900 });
  assert.deepEqual(calls, [{ width: 1440, height: 900 }]);
});

test("Given a headless verification browser launch, When its arguments are built, Then its initial window is large and headless", () => {
  const args = verificationBrowserArgs({ cdpPort: 34301, browserDir: "/tmp/agentre-browser", headless: true });

  assert.ok(args.includes("--window-size=1440,900"));
  assert.ok(args.includes("--headless=new"));
});

test("Given a headed verification browser launch, When its arguments are built, Then it keeps the same viewport without headless mode", () => {
  const args = verificationBrowserArgs({ cdpPort: 34301, browserDir: "/tmp/agentre-browser", headless: false });

  assert.ok(args.includes("--window-size=1440,900"));
  assert.ok(!args.includes("--headless=new"));
});

test("Given a verification browser launched as root, When its arguments are built, Then the sandbox is disabled so Chromium exposes CDP at all", () => {
  const args = verificationBrowserArgs({
    cdpPort: 34301,
    browserDir: "/tmp/agentre-browser",
    headless: true,
    isRoot: true,
  });

  assert.ok(args.includes("--no-sandbox"));
});

test("Given a verification browser launched as a regular user, When its arguments are built, Then the sandbox stays on", () => {
  const args = verificationBrowserArgs({
    cdpPort: 34301,
    browserDir: "/tmp/agentre-browser",
    headless: true,
    isRoot: false,
  });

  assert.ok(!args.includes("--no-sandbox"));
});
