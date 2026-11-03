"""按给定提示词生成素材拼图。接口地址与密钥只从显式环境变量取得。"""
import base64
import binascii
import json
import os
from pathlib import Path
import sys
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen

LIMIT = 32 * 1024 * 1024


def https_url(url):
    parts = urlsplit(url)
    if parts.scheme != "https" or not parts.hostname or parts.username or parts.password:
        raise ValueError("HTTPS URL required")
    return url


def fetch(request):
    with urlopen(request, timeout=600) as response:
        payload = response.read(LIMIT + 1)
    if len(payload) > LIMIT:
        raise ValueError("response too large")
    return payload


def main():
    if len(sys.argv) not in (4, 5):
        sys.exit("usage: gen.py MODEL OUTPUT PROMPT [SIZE]")
    base = os.environ.get("PRESET_IMAGE_BASE_URL", "")
    key = os.environ.get("PRESET_IMAGE_API_KEY", "")
    if not base or not key:
        sys.exit("set PRESET_IMAGE_BASE_URL and PRESET_IMAGE_API_KEY")
    model, out, prompt = sys.argv[1:4]
    body = {"model": model, "prompt": prompt, "size": sys.argv[4] if len(sys.argv) == 5 else "1024x1024", "n": 1}
    req = Request(https_url(base.rstrip("/") + "/images/generations"),
                  data=json.dumps(body).encode(), headers={"Content-Type": "application/json"})
    # 只把密钥交给配置的端点，重定向不携带凭据。
    req.add_unredirected_header("Authorization", "Bearer " + key)
    result = json.loads(fetch(req))["data"][0]
    if result.get("b64_json"):
        image = base64.b64decode(result["b64_json"], validate=True)
    elif result.get("url"):
        image = fetch(https_url(result["url"]))
    else:
        raise ValueError("missing image")
    if not image:
        raise ValueError("empty image")
    Path(out).write_bytes(image)
    print("ok", out)


if __name__ == "__main__":
    try:
        main()
    except HTTPError as err:
        sys.exit(f"HTTP {err.code}")
    except (URLError, ValueError, TypeError, AttributeError, IndexError, KeyError, binascii.Error):
        # 响应与 URL 可能含签名或令牌，不直接输出正文或异常详情。
        sys.exit("image generation failed: unavailable endpoint or invalid response")
