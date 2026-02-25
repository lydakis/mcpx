from __future__ import annotations

import os
import platform
import shutil
import tarfile
import tempfile
import urllib.error
import urllib.request
from pathlib import Path

DEFAULT_RELEASE_BASE_URL = "https://github.com/lydakis/mcpx/releases/download"
DEFAULT_RELEASE_TAG_PREFIX = "v"


def package_version() -> str:
    from importlib.metadata import PackageNotFoundError, version

    try:
        return version("mcpx-go")
    except PackageNotFoundError:
        return "0.0.0"


def resolve_platform() -> tuple[str, str]:
    os_name = platform.system().lower()
    machine = platform.machine().lower()

    os_map = {
        "darwin": "darwin",
        "linux": "linux",
    }
    arch_map = {
        "x86_64": "amd64",
        "amd64": "amd64",
        "arm64": "arm64",
        "aarch64": "arm64",
    }

    if os_name not in os_map:
        raise RuntimeError(f"unsupported platform: {os_name}")
    if machine not in arch_map:
        raise RuntimeError(f"unsupported architecture: {machine}")

    return os_map[os_name], arch_map[machine]


def cache_root() -> Path:
    xdg_cache_home = os.environ.get("XDG_CACHE_HOME")
    if xdg_cache_home:
        return Path(xdg_cache_home)
    return Path.home() / ".cache"


def data_root() -> Path:
    xdg_data_home = os.environ.get("XDG_DATA_HOME")
    if xdg_data_home:
        return Path(xdg_data_home)
    return Path.home() / ".local" / "share"


def binary_path(version: str | None = None) -> Path:
    selected_version = version or package_version()
    return cache_root() / "mcpx-go" / selected_version / "mcpx"


def manpage_path() -> Path:
    return data_root() / "man" / "man1" / "mcpx.1"


def release_asset_url(version: str, goos: str, goarch: str) -> str:
    base = os.environ.get("MCPX_GO_RELEASE_BASE_URL", DEFAULT_RELEASE_BASE_URL).rstrip("/")
    tag_prefix = os.environ.get("MCPX_GO_RELEASE_TAG_PREFIX", DEFAULT_RELEASE_TAG_PREFIX)
    tag = f"{tag_prefix}{version}"
    asset = f"mcpx_{version}_{goos}_{goarch}.tar.gz"
    return f"{base}/{tag}/{asset}"


def _extract_member(tar: tarfile.TarFile, member: tarfile.TarInfo, output_path: Path, mode: int) -> None:
    extracted = tar.extractfile(member)
    if extracted is None:
        raise RuntimeError(f"failed to read {member.name} from archive")

    output_path.parent.mkdir(parents=True, exist_ok=True)
    with output_path.open("wb") as out:
        shutil.copyfileobj(extracted, out)
    output_path.chmod(mode)


def _extract_binary(archive_path: Path, output_path: Path) -> None:
    with tarfile.open(archive_path, mode="r:gz") as tar:
        binary_member = next(
            (item for item in tar.getmembers() if item.isfile() and Path(item.name).name == "mcpx"),
            None,
        )
        if binary_member is None:
            raise RuntimeError("archive did not contain mcpx binary")

        _extract_member(tar, binary_member, output_path, 0o755)

        man_member = next(
            (item for item in tar.getmembers() if item.isfile() and Path(item.name).name == "mcpx.1"),
            None,
        )
        if man_member is not None:
            try:
                _extract_member(tar, man_member, manpage_path(), 0o644)
            except (OSError, RuntimeError):
                # Man page install is optional; keep the binary usable.
                pass


def ensure_binary(force: bool = False) -> Path:
    target = binary_path()
    if target.exists() and not force:
        return target

    if os.environ.get("MCPX_GO_SKIP_DOWNLOAD") == "1":
        raise RuntimeError("bundled binary download skipped by MCPX_GO_SKIP_DOWNLOAD=1")

    goos, goarch = resolve_platform()
    version = package_version()
    url = release_asset_url(version, goos, goarch)

    target.parent.mkdir(parents=True, exist_ok=True)

    with tempfile.NamedTemporaryFile(prefix="mcpx-go-", suffix=".tar.gz", delete=False) as tmp:
        tmp_path = Path(tmp.name)

    try:
        with urllib.request.urlopen(url) as response, tmp_path.open("wb") as out:
            shutil.copyfileobj(response, out)
    except urllib.error.HTTPError as error:
        tmp_path.unlink(missing_ok=True)
        raise RuntimeError(f"failed to download {url}: HTTP {error.code}") from error
    except urllib.error.URLError as error:
        tmp_path.unlink(missing_ok=True)
        raise RuntimeError(f"failed to download {url}: {error.reason}") from error

    try:
        _extract_binary(tmp_path, target)
    finally:
        tmp_path.unlink(missing_ok=True)

    return target
