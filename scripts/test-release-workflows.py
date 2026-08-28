#!/usr/bin/env python3
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[1]
PLATFORMS = {
    "darwin-amd64",
    "darwin-arm64",
    "linux-amd64",
    "linux-arm64",
    "windows-amd64",
    "windows-arm64",
}
def job_block(text: str, name: str) -> str:
    match = re.search(rf"^  {re.escape(name)}:\n", text, re.MULTILINE)
    if not match:
        raise AssertionError(f"missing job {name}")
    next_job = re.search(r"^  [a-zA-Z0-9_-]+:\n", text[match.end():], re.MULTILINE)
    end = match.end() + next_job.start() if next_job else len(text)
    return text[match.start():end]


def check_desktop_build_keychain(path: str) -> None:
    """Given a Linux desktop build, require an isolated keychain before Wails runs."""
    text = (ROOT / path).read_text()
    build = job_block(text, "build-desktop")
    for token in (
        "- name: Prepare isolated keychain (Linux)",
        "AGENTRE_KEYCHAIN_DIR: ${{ runner.temp }}/agentre-build-keychain",
        "if: matrix.goos == 'linux'",
        'mkdir -m 700 "$AGENTRE_KEYCHAIN_DIR"',
        'echo "AGENTRE_KEYCHAIN_DIR=$AGENTRE_KEYCHAIN_DIR" >> "$GITHUB_ENV"',
    ):
        if token not in build:
            raise AssertionError(f"{path} Linux desktop build missing {token!r}")
    if build.index("Prepare isolated keychain (Linux)") > build.index("Build desktop app"):
        raise AssertionError(f"{path} prepares the Linux keychain after the desktop build")


def check_workflow(path: str, prefix: str) -> None:
    text = (ROOT / path).read_text()
    if "-X agentre/internal/buildinfo.CommitID" in text:
        raise AssertionError(f"{path} still uses the abbreviated buildinfo ldflag target")

    build = job_block(text, "build-agentred")
    found = set(re.findall(r"platform:\s*(darwin|linux|windows)-(amd64|arm64)", build))
    found = {"-".join(parts) for parts in found}
    if found != PLATFORMS:
        raise AssertionError(f"{path} agentred matrix is {sorted(found)}, want {sorted(PLATFORMS)}")
    for token in (
        "make agentred-package",
        "AGENTRED_GOOS=${{ matrix.goos }}",
        "AGENTRED_GOARCH=${{ matrix.goarch }}",
        f"name: {prefix}-agentred-${{{{ matrix.platform }}}}",
        "path: build/agentred-dist/*",
    ):
        if token not in build:
            raise AssertionError(f"{path} build-agentred job missing {token!r}")

    release = job_block(text, "release")
    for token in (
        "build-agentred",
        "scripts/install.sh",
        "scripts/install.ps1",
        "> SHA256SUMS",
        "cp SHA256SUMS SHA256SUMS.txt",
        "cmp SHA256SUMS SHA256SUMS.txt",
    ):
        if token not in release:
            raise AssertionError(f"{path} release job missing {token!r}")
    if prefix == "nightly" and "releases/download/nightly" not in release:
        raise AssertionError(f"{path} nightly installers do not target the nightly release channel")


def main() -> None:
    makefile = (ROOT / "Makefile").read_text()
    for token in (
        "BUILDINFO_PKG := github.com/agentre-hub/agentre/internal/buildinfo",
        "-X $(BUILDINFO_PKG).CommitID=$(COMMIT_ID)",
        "agentred-package:",
        "agentred-$(VERSION)-$(AGENTRED_GOOS)-$(AGENTRED_GOARCH)",
    ):
        if token not in makefile:
            raise AssertionError(f"Makefile missing {token!r}")

    check_workflow(".github/workflows/release.yml", "release")
    check_workflow(".github/workflows/nightly.yml", "nightly")
    for path in (
        ".github/workflows/release.yml",
        ".github/workflows/nightly.yml",
        ".github/workflows/manual-build.yml",
    ):
        check_desktop_build_keychain(path)

    ci = (ROOT / ".github/workflows/ci.yml").read_text()
    for token in ("scripts/test-install.sh", "scripts/test-install.ps1", "scripts/test-release-workflows.py"):
        if token not in ci:
            raise AssertionError(f"CI does not run {token}")
    windows = job_block(ci, "agentred-installer-windows")
    for package in ("./cmd/agentred", "./internal/pkg/agentredipc"):
        if package not in windows:
            raise AssertionError(f"Windows CI does not run the platform tests in {package}")

    print("release workflow contract tests passed")


if __name__ == "__main__":
    main()
