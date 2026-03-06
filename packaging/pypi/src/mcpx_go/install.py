from __future__ import annotations

import hashlib
import os
import platform
import shutil
import tarfile
import tempfile
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

from .checksum_manifest import load_bundled_checksum_manifest
from .checksum_manifest import normalize_sha256
from .checksum_manifest import parse_release_checksums_text as parse_checksums_text

DEFAULT_RELEASE_BASE_URL = "https://github.com/lydakis/mcpx/releases/download"
DEFAULT_RELEASE_TAG_PREFIX = "v"


def package_version() -> str:
    from importlib.metadata import PackageNotFoundError, version

    try:
        return version("mcpx-go")
    except PackageNotFoundError:
        # Source-tree fallback for local/dev execution before install metadata exists.
        try:
            from . import __version__
        except Exception:
            return "0.0.0"
        return str(__version__)


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


def release_asset_name(version: str, goos: str, goarch: str) -> str:
    return f"mcpx_{version}_{goos}_{goarch}.tar.gz"


def release_base_url() -> str:
    return os.environ.get("MCPX_GO_RELEASE_BASE_URL", DEFAULT_RELEASE_BASE_URL).rstrip("/")


def release_tag(version: str) -> str:
    tag_prefix = os.environ.get("MCPX_GO_RELEASE_TAG_PREFIX", DEFAULT_RELEASE_TAG_PREFIX)
    return f"{tag_prefix}{version}"


def release_asset_url(version: str, goos: str, goarch: str) -> str:
    base = release_base_url()
    tag = release_tag(version)
    asset = release_asset_name(version, goos, goarch)
    return f"{base}/{tag}/{asset}"


def release_checksums_url(version: str) -> str:
    return f"{release_base_url()}/{release_tag(version)}/checksums.txt"


def _validate_release_url(url: str, allow_insecure: bool = False) -> None:
    scheme = urllib.parse.urlsplit(url).scheme.lower()
    if scheme == "https":
        return
    if scheme == "http" and allow_insecure:
        return
    if scheme == "http":
        raise RuntimeError(f"insecure release URL requires an out-of-band checksum: {url}")
    raise RuntimeError(f"unsupported URL scheme for release download: {url}")


class _ReleaseRedirectHandler(urllib.request.HTTPRedirectHandler):
    def __init__(self, allow_insecure: bool) -> None:
        self._allow_insecure = allow_insecure

    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[override]
        _validate_release_url(newurl, allow_insecure=self._allow_insecure)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def _open_release_url(url: str, allow_insecure: bool = False):
    _validate_release_url(url, allow_insecure=allow_insecure)
    opener = urllib.request.build_opener(_ReleaseRedirectHandler(allow_insecure))
    return opener.open(url)


def download_text(url: str) -> str:
    try:
        with _open_release_url(url) as response:
            return response.read().decode("utf-8")
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"failed to download {url}: HTTP {error.code}") from error
    except urllib.error.URLError as error:
        raise RuntimeError(f"failed to download {url}: {error.reason}") from error


def expected_archive_sha256(version: str, asset: str) -> str | None:
    sha256, _source = resolve_expected_archive_sha256(version, asset)
    return sha256


def resolve_expected_archive_sha256(version: str, asset: str) -> tuple[str | None, str]:
    if os.environ.get("MCPX_GO_SKIP_CHECKSUM") == "1":
        return None, "skip"

    env_checksum = os.environ.get("MCPX_GO_BINARY_SHA256")
    if env_checksum and env_checksum.strip():
        return normalize_sha256(env_checksum, "MCPX_GO_BINARY_SHA256"), "env"

    bundled_version, bundled_checksums = load_bundled_checksum_manifest()
    if bundled_version == version:
        expected = bundled_checksums.get(asset)
        if isinstance(expected, str):
            return normalize_sha256(expected, asset), "bundled"

    checksums_url = release_checksums_url(version)
    checksums = parse_checksums_text(download_text(checksums_url), version)
    expected = checksums.get(asset)
    if not isinstance(expected, str):
        raise RuntimeError(f"missing checksum for {asset} in release checksums at {checksums_url}")
    return normalize_sha256(expected, asset), "release"


def allow_insecure_archive_download(checksum_source: str) -> bool:
    return checksum_source in {"env", "bundled"}


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 64), b""):
            digest.update(chunk)
    return digest.hexdigest()


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
    asset = release_asset_name(version, goos, goarch)
    expected_sha256, checksum_source = resolve_expected_archive_sha256(version, asset)
    url = release_asset_url(version, goos, goarch)
    tmp_path: Path | None = None

    try:
        try:
            with _open_release_url(url, allow_insecure=allow_insecure_archive_download(checksum_source)) as response:
                target.parent.mkdir(parents=True, exist_ok=True)
                with tempfile.NamedTemporaryFile(prefix="mcpx-go-", suffix=".tar.gz", delete=False) as tmp:
                    tmp_path = Path(tmp.name)
                with tmp_path.open("wb") as out:
                    shutil.copyfileobj(response, out)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"failed to download {url}: HTTP {error.code}") from error
        except urllib.error.URLError as error:
            raise RuntimeError(f"failed to download {url}: {error.reason}") from error

        if expected_sha256:
            if tmp_path is None:
                raise RuntimeError("download failed before archive staging completed")
            actual_sha256 = sha256_file(tmp_path)
            if actual_sha256 != expected_sha256:
                raise RuntimeError(
                    f"checksum mismatch for {asset}: expected {expected_sha256}, got {actual_sha256}"
                )
        if tmp_path is None:
            raise RuntimeError("download failed before archive staging completed")
        _extract_binary(tmp_path, target)
    finally:
        if tmp_path is not None:
            tmp_path.unlink(missing_ok=True)

    return target
