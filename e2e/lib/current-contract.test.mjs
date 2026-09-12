import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import {
  existsSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { extname, join, relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { test } from "node:test";

import * as guardSuite from "./guard-suite.mjs";
import { REPO_ROOT } from "./run-context.mjs";

const read = (path) => readFileSync(join(REPO_ROOT, path), "utf8");

function localModuleDependencies(source) {
  const dependencies = new Map();
  const esmPatterns = [
    /(?:^|\n)\s*import\s+(?:[^"';()]*?\s+from\s+)?["']([^"']+)["']/gs,
    /(?:^|\n)\s*export\s+[^"';()]*?\s+from\s+["']([^"']+)["']/gs,
    /\bimport\s*\(\s*["']([^"']+)["']\s*\)/g,
  ];
  for (const pattern of esmPatterns) {
    for (const match of source.matchAll(pattern)) {
      if (match[1].startsWith(".")) dependencies.set(`esm:${match[1]}`, {
        kind: "ESM",
        specifier: match[1],
      });
    }
  }
  for (const match of source.matchAll(/\brequire\s*\(\s*["']([^"']+)["']\s*\)/g)) {
    if (match[1].startsWith(".")) dependencies.set(`commonjs:${match[1]}`, {
      kind: "CommonJS",
      specifier: match[1],
    });
  }
  return [...dependencies.values()];
}

function collectLocalModuleGraph(entryPaths) {
  const roots = Array.isArray(entryPaths) ? entryPaths : [entryPaths];
  const pending = roots.map((entryPath) => resolve(entryPath));
  const graph = new Map();

  while (pending.length > 0) {
    const modulePath = pending.pop();
    if (graph.has(modulePath)) continue;
    assert.equal(existsSync(modulePath), true, `local module is missing: ${modulePath}`);
    assert.equal(statSync(modulePath).isFile(), true, `local module is not a file: ${modulePath}`);

    const source = readFileSync(modulePath, "utf8");
    graph.set(modulePath, source);
    for (const dependency of localModuleDependencies(source)) {
      const moduleURL = new URL(dependency.specifier, pathToFileURL(modulePath));
      assert.equal(
        moduleURL.protocol,
        "file:",
        `${modulePath} has a non-file local ${dependency.kind} dependency ${dependency.specifier}`,
      );
      const dependencyPath = fileURLToPath(moduleURL);
      assert.notEqual(
        extname(dependencyPath),
        "",
        `${modulePath} local ${dependency.kind} dependency must include its extension: ${dependency.specifier}`,
      );
      const action = dependency.kind === "ESM" ? "imports" : "requires";
      assert.equal(
        existsSync(dependencyPath),
        true,
        `${modulePath} ${action} missing local ${dependency.kind} module ${dependency.specifier}`,
      );
      assert.equal(
        statSync(dependencyPath).isFile(),
        true,
        `${modulePath} local ${dependency.kind} dependency is not a file: ${dependency.specifier}`,
      );
      if (!graph.has(dependencyPath)) pending.push(dependencyPath);
    }
  }

  return graph;
}

function importsOSAccountHomeDiscovery(source) {
  const importsOS = /\bfrom\s*["'](?:node:)?os["']|\brequire\s*\(\s*["'](?:node:)?os["']\s*\)/.test(
    source,
  );
  if (!importsOS) return false;
  return (
    /\bimport\s*\{[^}]*\b(?:homedir|userInfo)\b[^}]*\}\s*from\s*["'](?:node:)?os["']/s.test(
      source,
    ) ||
    /\b(?:const|let|var)\s*\{[^}]*\b(?:homedir|userInfo)\b[^}]*\}\s*=\s*require\s*\(\s*["'](?:node:)?os["']\s*\)/s.test(
      source,
    ) ||
    /\.(?:homedir|userInfo)\s*\(/.test(source)
  );
}

function readsProductionRootEnvironment(source) {
  const names = "HOME|USERPROFILE|APPDATA|LOCALAPPDATA|XDG_CONFIG_HOME";
  const direct = new RegExp(
    `\\bprocess\\s*\\.\\s*env\\s*(?:(?:\\?\\.)?\\s*\\.\\s*(?:${names})\\b|\\[\\s*["'](?:${names})["']\\s*\\])`,
  );
  const destructured = new RegExp(
    `\\b(?:const|let|var)\\s*\\{[^}]*\\b(?:${names})\\b[^}]*\\}\\s*=\\s*process\\s*\\.\\s*env\\b`,
    "s",
  );
  if (direct.test(source) || destructured.test(source)) return true;

  for (const match of source.matchAll(
    /\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*process\s*\.\s*env\b/g,
  )) {
    const aliasLookup = new RegExp(
      `\\b${match[1]}\\s*(?:\\.\\s*(?:${names})\\b|\\[\\s*["'](?:${names})["']\\s*\\])`,
    );
    if (aliasLookup.test(source)) return true;
  }
  return false;
}

// guard-literal-patterns:start
const forbiddenRootLiteralCapabilities = [
  {
    name: "macOS installed/development data-root literal",
    pattern:
      /(?:Library[\\/]Application Support[\\/]agentre(?:-dev)?|["']Library["']\s*,\s*["']Application Support["']\s*,\s*["']agentre(?:-dev)?["'])/,
  },
  {
    name: "Linux installed/development data-root literal",
    pattern: /(?:\.config[\\/]agentre(?:-dev)?|["']\.config["']\s*,\s*["']agentre(?:-dev)?["'])/,
  },
  {
    name: "Windows installed/development data-root literal",
    pattern:
      /(?:AppData[\\/](?:Roaming|Local)[\\/]agentre(?:-dev)?|["']AppData["']\s*,\s*["'](?:Roaming|Local)["']\s*,\s*["']agentre(?:-dev)?["'])/i,
  },
  {
    name: "development data-root leaf literal",
    pattern: /["']agentre-dev["']/,
  },
];
// guard-literal-patterns:end

function sourceWithoutGuardLiteralPatternDefinitions(modulePath, source) {
  if (modulePath !== fileURLToPath(import.meta.url)) return source;
  const startMarker = "// guard-literal-patterns:start";
  const endMarker = "// guard-literal-patterns:end";
  const start = source.indexOf(startMarker);
  const end = source.indexOf(endMarker, start + startMarker.length);
  assert.notEqual(start, -1, "guard literal pattern start marker is missing");
  assert.notEqual(end, -1, "guard literal pattern end marker is missing");
  return `${source.slice(0, start)}${source.slice(end + endMarker.length)}`;
}

const legacyPaths = [
  "e2e/playwright.scratch.config.ts",
  "e2e/playwright.sync.config.ts",
  "e2e/run-e2e-sync.mjs",
  "e2e/scratch/README.md",
  "e2e/sync",
  "e2e/fixtures/git-repo.ts",
  "e2e/fixtures/sync.ts",
  "e2e/fakes/remote.go",
];

const currentGuidesAndEntries = [
  "AGENTS.md",
  "Makefile",
  "docs/architecture.md",
  "docs/develop.md",
  "docs/testing.md",
  "docs/verification.md",
  "docs/documentation.md",
  "docs/references/verification-report-template.md",
  "e2e/README.md",
  "e2e/package.json",
  "e2e/verify.mjs",
  "e2e/drive.mjs",
];

const legacyBuildTagDocs = [
  "internal/bootstrap/keychain.go",
  "internal/bootstrap/keychain_test.go",
];

const forbiddenCurrentFacts = [
  "--flavor",
  "FLAVOR=",
  "AGENTRE_VERIFY_FLAVOR",
  "AGENTRE_E2E_REUSE",
  "e2e-scratch",
  "e2e-sync",
  "test:scratch",
  "test:sync",
  "-tags e2e",
  "WindowHide",
];

test("Given the unified harness, when current entries and guides are inspected, then no legacy harness contract remains", () => {
  for (const path of legacyPaths) {
    assert.equal(existsSync(join(REPO_ROOT, path)), false, `${path} must be deleted`);
  }
  for (const path of currentGuidesAndEntries) {
    const source = read(path);
    for (const fact of forbiddenCurrentFacts) {
      assert.equal(source.includes(fact), false, `${path} still contains legacy fact ${fact}`);
    }
  }
  for (const path of legacyBuildTagDocs) {
    assert.equal(read(path).includes("-tags e2e"), false, `${path} still documents the deleted E2E build tag`);
  }
});

test("Given the automated E2E runner entry and guard roots, when their complete local module graph is inspected, then no module can discover installed or development data roots", () => {
  const entryPath = join(REPO_ROOT, "e2e", "run-e2e.mjs");
  const automatedGuardPaths = guardSuite.AUTOMATED_GUARD_TESTS.map((path) =>
    join(REPO_ROOT, "e2e", path),
  );
  const canonicalRoots = [entryPath, ...automatedGuardPaths];
  const targetTestPath = join(REPO_ROOT, "e2e", "lib", "target.test.mjs");
  assert.equal(canonicalRoots.includes(targetTestPath), false);

  const graph = collectLocalModuleGraph(canonicalRoots);
  for (const rootPath of canonicalRoots) {
    assert.equal(graph.has(rootPath), true, `${relative(REPO_ROOT, rootPath)} is not guarded`);
  }
  assert.equal(graph.has(join(REPO_ROOT, "e2e", "lib", "run-context.mjs")), true);
  assert.equal(graph.has(targetTestPath), false);

  for (const [modulePath, source] of graph) {
    assert.equal(
      importsOSAccountHomeDiscovery(source),
      false,
      `${relative(REPO_ROOT, modulePath)} imports OS account-home discovery`,
    );
    assert.equal(
      readsProductionRootEnvironment(source),
      false,
      `${relative(REPO_ROOT, modulePath)} reads production-root environment`,
    );
    const literalSource = sourceWithoutGuardLiteralPatternDefinitions(modulePath, source);
    for (const capability of forbiddenRootLiteralCapabilities) {
      assert.doesNotMatch(
        literalSource,
        capability.pattern,
        `${relative(REPO_ROOT, modulePath)} has forbidden ${capability.name}`,
      );
    }
  }
});

test("Given local ESM imports and literal relative CommonJS requires, when the runner graph is collected, then extension-bearing dependencies are followed and missing modules fail loudly", (t) => {
  const fixtureRoot = mkdtempSync(join(tmpdir(), "agentre-runner-graph-"));
  t.after(() => rmSync(fixtureRoot, { recursive: true, force: true }));
  const esmDependencyPath = join(fixtureRoot, "dependency.mjs");
  const cjsDependencyPath = join(fixtureRoot, "dependency.cjs");
  const entryPath = join(fixtureRoot, "entry.mjs");
  writeFileSync(esmDependencyPath, "export const dependency = true;\n");
  writeFileSync(cjsDependencyPath, "module.exports = true;\n");
  writeFileSync(
    entryPath,
    [
      'import { dependency } from "./dependency.mjs";\n',
      "const cjs = require",
      '("./dependency.cjs");\nvoid dependency;\nvoid cjs;\n',
    ].join(""),
  );

  const graph = collectLocalModuleGraph(entryPath);
  assert.equal(graph.has(esmDependencyPath), true);
  assert.equal(graph.has(cjsDependencyPath), true);

  writeFileSync(entryPath, 'import "./missing.mjs";\n');
  assert.throws(
    () => collectLocalModuleGraph(entryPath),
    /imports missing local ESM module \.\/missing\.mjs/,
  );

  writeFileSync(entryPath, ["require", '("./missing.cjs");\n'].join(""));
  assert.throws(
    () => collectLocalModuleGraph(entryPath),
    /requires missing local CommonJS module \.\/missing\.cjs/,
  );
});

test("Given automated E2E and formal verification guards, when their test lists are inspected, then only the explicit full suite reaches target discovery", () => {
  assert.deepEqual([...guardSuite.AUTOMATED_GUARD_TESTS].sort(), [
    "lib/app-overlay.test.mjs",
    "lib/browser.test.mjs",
    "lib/current-contract.test.mjs",
    "lib/fake-sync-server.test.mjs",
    "lib/guard-suite.test.mjs",
    "lib/run-context.test.mjs",
  ]);
  assert.equal(guardSuite.AUTOMATED_GUARD_TESTS.includes("lib/target.test.mjs"), false);
  assert.deepEqual([...guardSuite.FULL_GUARD_TESTS].sort(), [
    "lib/app-overlay.test.mjs",
    "lib/browser.test.mjs",
    "lib/current-contract.test.mjs",
    "lib/fake-sync-server.test.mjs",
    "lib/guard-suite.test.mjs",
    "lib/run-context.test.mjs",
    "lib/target.test.mjs",
  ]);
});

test("Given the canonical runner uses the guard-suite default, when guards are spawned, then only automated roots execute", async () => {
  let spawnedArgs;
  await guardSuite.runNodeGuards({
    cwd: join(REPO_ROOT, "e2e"),
    spawnProcess(_command, args) {
      spawnedArgs = args;
      const child = new EventEmitter();
      queueMicrotask(() => child.emit("exit", 0, null));
      return child;
    },
  });

  assert.deepEqual(spawnedArgs, ["--test", ...guardSuite.AUTOMATED_GUARD_TESTS]);
  assert.equal(spawnedArgs.includes("lib/target.test.mjs"), false);
});

test("Given the explicit guard entry, when it invokes the suite, then it requests every formal verification guard", () => {
  const guardRunner = read("e2e/run-guards.mjs");
  assert.match(guardRunner, /FULL_GUARD_TESTS/);
  assert.match(guardRunner, /runNodeGuards\(\{\s*tests:\s*FULL_GUARD_TESTS\s*\}\)/);
});

test("Given the dedicated E2E app generates ignored Wails bindings, when Wails appends its wailsjs output directory, then the files land in the root frontend imported by Vite", () => {
  const config = JSON.parse(read("e2e/app/wails.json"));
  const generatedBindingsDir = resolve(
    REPO_ROOT,
    "e2e",
    "app",
    config.wailsjsdir,
    "wailsjs",
  );

  assert.equal(generatedBindingsDir, join(REPO_ROOT, "frontend", "wailsjs"));
});

test("Given the replacement suite, when its public entries are inspected, then only unified automation and real verification remain", () => {
  assert.deepEqual(
    readdirSync(join(REPO_ROOT, "e2e", "tests")).filter((name) => name.endsWith(".spec.ts")).sort(),
    ["desktop.spec.ts", "remote-peer.spec.ts", "sync-client.spec.ts"],
  );

  const makefile = read("Makefile");
  assert.match(makefile, /^BACKEND_PKGS := .*\.\/e2e\/\.\.\.(?: |$)/m);
  for (const target of ["e2e:", "verify-up:", "verify-status:", "verify-down:"]) {
    assert.match(makefile, new RegExp(`^${target.replace(":", "\\:")}`, "m"));
  }
  assert.match(makefile, /^e2e:\n\tcd e2e && pnpm test$/m);

  const pkg = JSON.parse(read("e2e/package.json"));
  assert.deepEqual(Object.keys(pkg.scripts).sort(), ["drive", "setup", "test", "test:guards", "verify"]);
  assert.equal(pkg.scripts.test, "node run-e2e.mjs");
  assert.equal(pkg.scripts["test:guards"], "node run-guards.mjs");

  const runner = read("e2e/run-e2e.mjs");
  assert.match(runner, /await runNodeGuards\(\)/);
  assert.equal(runner.includes("FULL_GUARD_TESTS"), false);
  assert.equal(runner.includes("target.test.mjs"), false);
  assert.equal(runner.includes('"lib\/run-context.test.mjs"'), false);

  const verify = read("e2e/verify.mjs");
  assert.match(verify, /spawn\(wailsBin\(\), wailsArgs, \{[\s\S]*cwd: repoRoot,/);
  assert.equal(verify.includes("e2e/app"), false);
  assert.equal(verify.includes("AGENTRE_E2E_MANIFEST"), false);

  const workflow = read(".github/workflows/ci.yml");
  const e2eJob = workflow.slice(workflow.indexOf("  e2e:"));
  assert.equal((e2eJob.match(/make e2e/g) ?? []).length, 1);
  assert.match(e2eJob, /name: e2e-artifacts-\$\{\{ github\.run_id \}\}-\$\{\{ github\.run_attempt \}\}/);
  assert.match(e2eJob, /e2e\/artifacts\/\*\//);
});
