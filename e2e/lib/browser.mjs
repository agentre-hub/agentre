export const VERIFY_VIEWPORT = Object.freeze({ width: 1440, height: 900 });

export async function applyVerificationViewport(page) {
  await page.setViewportSize(VERIFY_VIEWPORT);
}

function isRootProcess() {
  return typeof process.getuid === "function" && process.getuid() === 0;
}

export function verificationBrowserArgs({ cdpPort, browserDir, headless, isRoot = isRootProcess() }) {
  const args = [
    `--remote-debugging-port=${cdpPort}`,
    `--user-data-dir=${browserDir}`,
    `--window-size=${VERIFY_VIEWPORT.width},${VERIFY_VIEWPORT.height}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--no-service-autorun",
    "--password-store=basic",
    "--use-mock-keychain",
  ];
  // Chromium refuses to start as root with its sandbox on, and then never exposes
  // the CDP port this driver attaches to. Running as root means there is no sandbox
  // to keep, so the flag is scoped to that case instead of being always on.
  if (isRoot) args.push("--no-sandbox");
  if (headless) args.push("--headless=new");
  return args;
}
