import assert from "node:assert/strict";
import { existsSync, readdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { AUTOMATED_GUARD_TESTS, FULL_GUARD_TESTS } from "./guard-suite.mjs";

// here 是 e2e/lib；清单里的条目是相对 e2e/ 写的（runNodeGuards 就是拿它们当
// `node --test` 的参数、cwd 落在 e2e/），所以比对文件是否存在要回到 e2e/ 这一层。
const here = dirname(fileURLToPath(import.meta.url));
const e2eRoot = resolve(here, "..");
const thisFile = "lib/guard-suite.test.mjs";

/**
 * 这一条守的是「测试文件有没有真的被跑」。
 *
 * 清单是手写的，所以「写了一个守卫但忘了登记」不会有任何症状：`node --test` 只跑被点名的
 * 文件，文件在磁盘上躺着、内容是绿的、CI 全绿，而它一次都没执行过。本仓刚发生过一次
 * （`lib/browser.test.mjs` 从未进过清单，`make e2e` 与 `pnpm run test:guards` 都不跑它）。
 *
 * 判据取「磁盘上的 `*.test.mjs`」与「清单里的条目」互相包含，两边都查：漏登记的文件会让
 * 第一条红，写错的路径会让第二条红（否则只会得到一个 `Cannot find module` 之类的次生
 * 症状）。
 */
test("Given the guard suite, When its lists are read, Then every test file on disk is registered", () => {
  const onDisk = readdirSync(here)
    .filter((name) => name.endsWith(".test.mjs"))
    .map((name) => `lib/${name}`)
    .sort();

  // 自证不空过：目录里一个测试文件都没读到时，「漏登记的为空」是没有意义的。
  assert.ok(
    onDisk.length > 0,
    `${here} 下一个 *.test.mjs 都没读到，这条守卫会静默全绿`,
  );

  const registered = new Set(FULL_GUARD_TESTS);
  const orphans = onDisk.filter((file) => !registered.has(file));
  assert.deepEqual(
    orphans,
    [],
    "这些测试文件在磁盘上但从不在任何清单里，等于一次都不跑：" + orphans.join(", "),
  );
});

/**
 * 反向：清单里的每个条目都要真的有文件。写错一个字母的症状是 `node --test` 报一个与
 * 「路径写错」无关的错，排查时先去怀疑被测代码，而不是怀疑清单。
 */
test("Given the guard suite, When its lists are read, Then every registered entry exists", () => {
  for (const file of FULL_GUARD_TESTS) {
    assert.ok(existsSync(resolve(e2eRoot, file)), `清单里的 ${file} 在磁盘上不存在`);
  }
});

/**
 * `make e2e` 走的是 AUTOMATED 那一份（`run-e2e.mjs` 用默认参数），完整那份是它的超集。
 * 一旦有人把完整清单改瘦，`make e2e` 会跟着悄悄少跑几条 —— 这条把包含关系钉住。
 */
test("Given the guard suite, When the two lists are compared, Then every automated guard is also in the full run", () => {
  const full = new Set(FULL_GUARD_TESTS);
  const missing = AUTOMATED_GUARD_TESTS.filter((file) => !full.has(file));
  assert.deepEqual(missing, [], "这些自动化守卫不在完整清单里：" + missing.join(", "));
});

/**
 * 本文件自己必须在清单里，否则上面三条一条都不会被执行 —— 一条「不跑的守卫」比没有守卫
 * 更糟，因为它看起来是绿的。
 *
 * 说清它守不到的那一半：如果有人把本文件从**两份**清单里都删掉，这几行也不会运行，没有
 * 任何东西会因此变红。它守的是「清单漏了别的文件」与「本文件被挪出完整清单」。
 */
test("Given the completeness guard itself, When the lists are read, Then it is registered so that it actually runs", () => {
  assert.ok(
    FULL_GUARD_TESTS.includes(thisFile),
    `${thisFile} 不在完整清单里：它自己就不会被跑，上面三条断言全部作废`,
  );
});
