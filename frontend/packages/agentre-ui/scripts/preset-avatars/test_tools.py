"""素材工具回归测试：只用临时目录；不调用生成 API 或读取本机凭据。"""
import base64
import io
import json
import os
import runpy
import sys
import urllib.request
from unittest import mock
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parent


class ToolTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def run_tool(self, *args, **env):
        return subprocess.run(
            args, cwd=self.root, env={**os.environ, **env},
            capture_output=True, text=True, timeout=30,
        )

    def generate(self, response):
        # 模拟配置与网络，不读取本机配置，也不允许真的发起下载。
        output = self.root / "sheet.png"
        key = "test-token"
        requests = []
        def respond(request, **kwargs):
            requests.append(request)
            return io.BytesIO(json.dumps(response).encode())
        with mock.patch.dict(os.environ, PRESET_IMAGE_API_KEY=key, PRESET_IMAGE_BASE_URL="https://api.example/v1"), \
             mock.patch.object(sys, "argv", ["gen.py", "model", str(output), "prompt"]), \
             mock.patch("urllib.request.urlopen", respond):
            runpy.run_path(str(SCRIPTS / "gen.py"), run_name="__main__")
        return requests, output

    def test_generation_token_is_not_forwarded_by_http_redirects(self):
        requests, output = self.generate({"data": [{"b64_json": base64.b64encode(b"image").decode()}]})
        self.assertEqual(output.read_bytes(), b"image")
        req = requests[0]
        self.assertEqual(req.get_header("Authorization"), "Bearer test-token")
        redirected = urllib.request.HTTPRedirectHandler().redirect_request(
            req, None, 302, "redirect", {}, "https://other.example/image")
        self.assertFalse(redirected.has_header("Authorization"))

    def test_generation_rejects_missing_image_and_local_file_url(self):
        for data in [{}, {"url": "file:///var/private-file"}]:
            with self.subTest(data=data), self.assertRaises(SystemExit):
                self.generate({"data": [data]})

    def test_raster_relative_brands_and_failure_close(self):
        # 浏览器端口是测试替身，记录真实脚本传来的页面与关闭动作。
        module = self.root / "browser.mjs"
        module.write_text('''
import { readFileSync, writeFileSync } from "node:fs";
export const chromium = { launch: async () => ({
  newPage: async () => ({
    goto: async (url) => writeFileSync("page.txt", readFileSync(new URL(url))),
    locator: () => ({
      evaluate: async (fn) => fn({ decode: async () => { if (process.env.BAD_LOGO) throw new Error("bad logo"); } }),
      screenshot: async () => { throw new Error("capture failed"); },
    }),
  }),
  close: async () => writeFileSync("closed.txt", "closed"),
}) };
''')
        existing_page = self.root / "_r.html"
        existing_page.write_text("unrelated HTML")
        result = self.run_tool("node", str(SCRIPTS / "raster.mjs"),
                               "--playwright", str(module), "--brands", "logos")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("capture failed", result.stderr)
        page = (self.root / "page.txt").read_text()
        with self.subTest("relative brands"):
            self.assertIn((self.root / "logos" / "claude.svg").resolve().as_uri(), page)
        with self.subTest("browser cleanup"):
            self.assertTrue((self.root / "closed.txt").exists())
        with self.subTest("unrelated HTML"):
            self.assertEqual(existing_page.read_text(), "unrelated HTML")
        with self.subTest("decode failure"):
            result = self.run_tool("node", str(SCRIPTS / "raster.mjs"), "--playwright", str(module), BAD_LOGO="1")
            self.assertIn("bad logo", result.stderr)

    @unittest.skipUnless(shutil.which("magick"), "requires ImageMagick")
    def test_sticker_dash_means_original_colour_and_preserves_working_files(self):
        source = self.root / "logo.png"
        subprocess.run(["magick", "-size", "40x40", "xc:none", "-fill", "red",
                        "-draw", "rectangle 10,10 30,30", str(source)], check=True)
        sentinel = self.root / "_l.png"
        sentinel.write_bytes(b"unrelated working file")
        result = self.run_tool("sh", str(SCRIPTS / "stk.sh"), str(source),
                               "sticker", "#eeeeee", "-")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        self.assertTrue((self.root / "sticker.webp").exists())
        self.assertEqual(sentinel.read_bytes(), b"unrelated working file")

    def test_sticker_stops_after_first_failed_command(self):
        magick = self.root / "magick"
        magick.write_text('#!/bin/sh\necho called >> "$CALL_LOG"\nexit 7\n')
        magick.chmod(0o755)
        result = self.run_tool("sh", str(SCRIPTS / "stk.sh"), "missing.png",
                               "sticker", "#eeeeee", "",
                               PATH=f"{self.root}:{os.environ['PATH']}",
                               CALL_LOG=str(self.root / "calls"))
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / "calls").read_text().splitlines(), ["called"])

    @unittest.skipUnless(shutil.which("magick"), "requires ImageMagick")
    def test_slice_uses_private_scratch_and_produces_sixteen_tiles(self):
        from PIL import Image, ImageDraw
        source = self.root / "sheet.png"
        image = Image.new("RGB", (420, 420), "white")
        draw = ImageDraw.Draw(image)
        for row in range(4):
            for col in range(4):
                x, y = 10 + col * 100, 10 + row * 100
                draw.rectangle((x, y, x + 89, y + 89), fill="#ddddff")
                draw.rectangle((x + 30, y + 30, x + 60, y + 60), fill="#334477")
        image.save(source)
        # 用替身检查临时路径，而不是碰全局 /tmp/presetgen 的历史数据。
        magick = self.root / "magick"
        real_magick = shutil.which("magick")
        magick.write_text(f'''#!/bin/sh
for arg in "$@"; do
  case "$arg" in /tmp/presetgen/*) echo shared-scratch >&2; exit 9;; esac
done
exec "{real_magick}" "$@"
''')
        magick.chmod(0o755)
        result = self.run_tool("python3", str(SCRIPTS / "slice2.py"), str(source),
                               "out", "team", PATH=f"{self.root}:{os.environ['PATH']}")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(list((self.root / "out").glob("*.webp"))), 16)

    @unittest.skipUnless(shutil.which("magick"), "requires ImageMagick")
    def test_slice_rejects_sheet_without_four_rows_and_columns(self):
        from PIL import Image
        source = self.root / "blank.png"
        Image.new("RGB", (420, 420), "white").save(source)
        result = self.run_tool("python3", str(SCRIPTS / "slice2.py"), str(source), "out", "team")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(list((self.root / "out").glob("*.webp")), [])

    def test_clean_solid_background_has_no_component_to_keep(self):
        from PIL import Image
        source = self.root / "source"
        source.mkdir()
        Image.new("RGB", (128, 128), "#eeeeee").save(source / "team-blank.webp")
        result = self.run_tool("python3", str(SCRIPTS / "clean_edges.py"),
                               str(source), "out")
        self.assertEqual(result.returncode, 0, result.stderr)
        with Image.open(self.root / "out" / "team-blank.webp") as image:
            self.assertEqual(image.size, (128, 128))
            self.assertEqual(len(image.getcolors()), 1)


if __name__ == "__main__":
    unittest.main()
