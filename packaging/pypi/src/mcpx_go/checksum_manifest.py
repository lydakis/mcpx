from __future__ import annotations

import json
import re
from functools import lru_cache
from importlib import resources
from pathlib import Path

SUPPORTED_GOOS = ("darwin", "linux")
SUPPORTED_GOARCH = ("amd64", "arm64")
EMPTY_BUNDLED_CHECKSUM_MANIFEST = {"version": "0.0.0", "checksums": {}}
CHECKSUM_LINE_RE = re.compile(r"^([a-fA-F0-9]{64})\s+\*?(.+)$")


def normalize_sha256(value: str, label: str) -> str:
    normalized = str(value or "").strip().lower()
    if not re.fullmatch(r"[a-f0-9]{64}", normalized):
        raise RuntimeError(f"invalid SHA-256 digest for {label}")
    return normalized


def expected_release_assets(version: str) -> tuple[str, ...]:
    return tuple(
        f"mcpx_{version}_{goos}_{goarch}.tar.gz"
        for goos in SUPPORTED_GOOS
        for goarch in SUPPORTED_GOARCH
    )


def parse_release_checksums_text(text: str, version: str) -> dict[str, str]:
    expected_assets = set(expected_release_assets(version))
    checksums: dict[str, str] = {}

    for raw_line in str(text or "").splitlines():
        line = raw_line.strip()
        if not line:
            continue

        match = CHECKSUM_LINE_RE.match(line)
        if not match:
            continue

        digest = match.group(1).lower()
        name = Path(match.group(2).strip()).name
        if name not in expected_assets:
            continue
        checksums[name] = normalize_sha256(digest, name)

    return checksums


def build_bundled_checksum_manifest(version: str, text: str) -> dict[str, object]:
    parsed = parse_release_checksums_text(text, version)
    ordered_checksums: dict[str, str] = {}
    missing_assets: list[str] = []

    for asset in expected_release_assets(version):
        digest = parsed.get(asset)
        if digest is None:
            missing_assets.append(asset)
            continue
        ordered_checksums[asset] = digest

    if missing_assets:
        missing = ", ".join(missing_assets)
        raise RuntimeError(f"missing release archive checksums for version {version}: {missing}")

    return {"version": version, "checksums": ordered_checksums}


def _coerce_bundled_checksum_manifest(data: object) -> tuple[str, dict[str, str]]:
    if not isinstance(data, dict):
        raise RuntimeError("invalid bundled checksum manifest: root must be an object")

    version = data.get("version")
    if not isinstance(version, str):
        raise RuntimeError("invalid bundled checksum manifest: version must be a string")

    checksums_data = data.get("checksums")
    if not isinstance(checksums_data, dict):
        raise RuntimeError("invalid bundled checksum manifest: checksums must be an object")

    checksums: dict[str, str] = {}
    for name, digest in checksums_data.items():
        if not isinstance(name, str):
            raise RuntimeError("invalid bundled checksum manifest: checksum key must be a string")
        if not isinstance(digest, str):
            raise RuntimeError(f"invalid bundled checksum manifest: checksum for {name} must be a string")
        checksums[name] = normalize_sha256(digest, name)

    return version, checksums


def serialize_bundled_checksum_manifest(data: object) -> str:
    version, checksums = _coerce_bundled_checksum_manifest(data)
    return json.dumps({"version": version, "checksums": checksums}, indent=2) + "\n"


def parse_bundled_checksum_manifest(text: str) -> tuple[str, dict[str, str]]:
    try:
        data = json.loads(text)
    except json.JSONDecodeError as error:
        raise RuntimeError("invalid bundled checksum manifest: malformed JSON") from error
    return _coerce_bundled_checksum_manifest(data)


@lru_cache(maxsize=1)
def load_bundled_checksum_manifest() -> tuple[str, dict[str, str]]:
    try:
        manifest_path = resources.files("mcpx_go").joinpath("checksums.json")
        text = manifest_path.read_text(encoding="utf-8")
    except FileNotFoundError:
        empty = EMPTY_BUNDLED_CHECKSUM_MANIFEST
        return str(empty["version"]), dict(empty["checksums"])

    return parse_bundled_checksum_manifest(text)
