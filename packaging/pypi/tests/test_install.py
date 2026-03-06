from __future__ import annotations

import hashlib
import json
import os
import tarfile
import tempfile
import urllib.request
import unittest
from io import BytesIO
from pathlib import Path
from unittest.mock import patch

import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from mcpx_go import checksum_manifest
from mcpx_go import install


class ExtractBinaryTests(unittest.TestCase):
    def _build_archive(self, tmpdir: Path) -> Path:
        archive_path = tmpdir / "mcpx.tar.gz"
        binary_path = tmpdir / "mcpx"
        manpage_source = tmpdir / "mcpx.1"

        binary_path.write_text("binary")
        manpage_source.write_text("manual")

        with tarfile.open(archive_path, mode="w:gz") as tar:
            tar.add(binary_path, arcname="mcpx")
            tar.add(manpage_source, arcname="man/man1/mcpx.1")
        return archive_path

    def test_extract_binary_continues_when_manpage_install_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            tmpdir = Path(tmp)
            archive_path = self._build_archive(tmpdir)
            output_path = tmpdir / "out" / "mcpx"
            blocked_data_home = tmpdir / "blocked-data-home"
            blocked_data_home.write_text("not-a-directory")

            with patch.dict(os.environ, {"XDG_DATA_HOME": str(blocked_data_home)}, clear=False):
                install._extract_binary(archive_path, output_path)

            self.assertTrue(output_path.exists())
            self.assertEqual("binary", output_path.read_text())
            self.assertTrue(os.access(output_path, os.X_OK))

    def test_extract_binary_installs_manpage_when_data_home_is_writable(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            tmpdir = Path(tmp)
            archive_path = self._build_archive(tmpdir)
            output_path = tmpdir / "out" / "mcpx"
            data_home = tmpdir / "data-home"
            data_home.mkdir()

            with patch.dict(os.environ, {"XDG_DATA_HOME": str(data_home)}, clear=False):
                install._extract_binary(archive_path, output_path)

            manpage = data_home / "man" / "man1" / "mcpx.1"
            self.assertTrue(manpage.exists())
            self.assertEqual("manual", manpage.read_text())


class ChecksumTests(unittest.TestCase):
    def test_build_bundled_checksum_manifest_keeps_only_expected_assets(self) -> None:
        version = "1.2.3"
        digests = {
            f"mcpx_{version}_darwin_amd64.tar.gz": "a" * 64,
            f"mcpx_{version}_darwin_arm64.tar.gz": "b" * 64,
            f"mcpx_{version}_linux_amd64.tar.gz": "c" * 64,
            f"mcpx_{version}_linux_arm64.tar.gz": "d" * 64,
        }
        lines = [f"{digest}  {name}" for name, digest in digests.items()]
        lines.append(f'{"e" * 64}  mcpx_{version}_linux_amd64"oops.tar.gz')

        manifest = checksum_manifest.build_bundled_checksum_manifest(version, "\n".join(lines) + "\n")

        self.assertEqual(version, manifest["version"])
        self.assertEqual(digests, manifest["checksums"])

    def test_serialize_bundled_checksum_manifest_uses_valid_json_escaping(self) -> None:
        name = 'mcpx_1.2.3_linux_amd64"oops\\\\.tar.gz'
        text = checksum_manifest.serialize_bundled_checksum_manifest(
            {"version": "1.2.3", "checksums": {name: "a" * 64}}
        )

        parsed = json.loads(text)
        self.assertEqual("a" * 64, parsed["checksums"][name])

    def test_validate_release_url_rejects_plain_http_without_out_of_band_checksum(self) -> None:
        with self.assertRaises(RuntimeError):
            install._validate_release_url("http://example.test/releases/download/v1.2.3/checksums.txt")

    def test_validate_release_url_allows_plain_http_with_out_of_band_checksum(self) -> None:
        install._validate_release_url(
            "http://example.test/releases/download/v1.2.3/mcpx_1.2.3_linux_amd64.tar.gz",
            allow_insecure=True,
        )

    def test_release_redirect_handler_rejects_plain_http_redirect_without_out_of_band_checksum(self) -> None:
        handler = install._ReleaseRedirectHandler(allow_insecure=False)
        request = urllib.request.Request("https://example.test/releases/download/v1.2.3/checksums.txt")
        with self.assertRaises(RuntimeError):
            handler.redirect_request(
                request,
                None,
                302,
                "Found",
                {},
                "http://mirror.example.test/releases/download/v1.2.3/checksums.txt",
            )

    def test_release_redirect_handler_allows_plain_http_redirect_with_out_of_band_checksum(self) -> None:
        handler = install._ReleaseRedirectHandler(allow_insecure=True)
        request = urllib.request.Request("https://example.test/releases/download/v1.2.3/mcpx_1.2.3_linux_amd64.tar.gz")
        redirected = handler.redirect_request(
            request,
            None,
            302,
            "Found",
            {},
            "http://mirror.example.test/releases/download/v1.2.3/mcpx_1.2.3_linux_amd64.tar.gz",
        )
        self.assertIsNotNone(redirected)
        self.assertEqual(
            "http://mirror.example.test/releases/download/v1.2.3/mcpx_1.2.3_linux_amd64.tar.gz",
            redirected.full_url,
        )

    def test_expected_archive_sha256_uses_env_override(self) -> None:
        asset = "mcpx_1.2.3_linux_amd64.tar.gz"
        digest = "A" * 64
        with patch.dict(os.environ, {"MCPX_GO_BINARY_SHA256": digest}, clear=True):
            got = install.expected_archive_sha256("1.2.3", asset)
        self.assertEqual(digest.lower(), got)

    def test_expected_archive_sha256_uses_bundled_manifest(self) -> None:
        asset = "mcpx_1.2.3_linux_amd64.tar.gz"
        digest = "b" * 64
        with (
            patch.object(install, "load_bundled_checksum_manifest", return_value=("1.2.3", {asset: digest})),
            patch.dict(os.environ, {}, clear=True),
        ):
            got = install.expected_archive_sha256("1.2.3", asset)
        self.assertEqual(digest, got)

    def test_expected_archive_sha256_falls_back_to_release_checksums(self) -> None:
        asset = "mcpx_1.2.3_linux_amd64.tar.gz"
        digest = "c" * 64
        with (
            patch.object(install, "load_bundled_checksum_manifest", return_value=("0.0.0", {})),
            patch.dict(os.environ, {}, clear=True),
            patch.object(install, "download_text", return_value=f"{digest}  {asset}\n") as download_text,
        ):
            got = install.expected_archive_sha256("1.2.3", asset)
        self.assertEqual(digest, got)
        download_text.assert_called_once_with(
            "https://github.com/lydakis/mcpx/releases/download/v1.2.3/checksums.txt"
        )

    def test_expected_archive_sha256_rejects_http_checksum_url_without_out_of_band_checksum(self) -> None:
        asset = "mcpx_1.2.3_linux_amd64.tar.gz"
        with (
            patch.object(install, "load_bundled_checksum_manifest", return_value=("0.0.0", {})),
            patch.dict(
                os.environ,
                {"MCPX_GO_RELEASE_BASE_URL": "http://example.test/releases/download"},
                clear=True,
            ),
        ):
            with self.assertRaises(RuntimeError):
                install.expected_archive_sha256("1.2.3", asset)

    def test_expected_archive_sha256_errors_when_missing(self) -> None:
        asset = "mcpx_1.2.3_linux_amd64.tar.gz"
        with (
            patch.object(install, "load_bundled_checksum_manifest", return_value=("1.2.3", {})),
            patch.dict(os.environ, {}, clear=True),
            patch.object(install, "download_text", return_value=""),
        ):
            with self.assertRaises(RuntimeError):
                install.expected_archive_sha256("1.2.3", asset)

    def test_ensure_binary_checksum_mismatch_fails(self) -> None:
        class FakeResponse:
            def __init__(self, payload: bytes) -> None:
                self._stream = BytesIO(payload)

            def __enter__(self) -> "FakeResponse":
                return self

            def __exit__(self, exc_type, exc, tb) -> None:
                return None

            def read(self, n: int = -1) -> bytes:
                return self._stream.read(n)

        payload = b"not-a-real-tarball"
        with tempfile.TemporaryDirectory() as tmp:
            cache = Path(tmp) / "cache"
            with (
                patch.dict(os.environ, {"XDG_CACHE_HOME": str(cache)}, clear=False),
                patch.object(install, "resolve_platform", return_value=("linux", "amd64")),
                patch.object(install, "package_version", return_value="1.2.3"),
                patch.object(install, "resolve_expected_archive_sha256", return_value=("0" * 64, "release")),
                patch.object(install, "_open_release_url", return_value=FakeResponse(payload)),
                patch.object(install, "_extract_binary"),
            ):
                with self.assertRaises(RuntimeError):
                    install.ensure_binary(force=True)

    def test_ensure_binary_does_not_leave_temp_archive_when_release_url_validation_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            tmpdir = Path(tmp)
            cache = tmpdir / "cache"
            cache.mkdir()
            download_tmp = tmpdir / "tmp"
            download_tmp.mkdir()
            previous_tempdir = tempfile.tempdir
            tempfile.tempdir = str(download_tmp)

            try:
                with patch.dict(
                    os.environ,
                    {
                        "XDG_CACHE_HOME": str(cache),
                        "MCPX_GO_SKIP_CHECKSUM": "1",
                        "MCPX_GO_RELEASE_BASE_URL": "http://example.test/releases/download",
                    },
                    clear=False,
                ):
                    with self.assertRaises(RuntimeError):
                        install.ensure_binary(force=True)
            finally:
                tempfile.tempdir = previous_tempdir

            self.assertEqual([], list(download_tmp.iterdir()))

    def test_sha256_file(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            file_path = Path(tmp) / "blob"
            data = b"abc123"
            file_path.write_bytes(data)
            got = install.sha256_file(file_path)
        self.assertEqual(hashlib.sha256(data).hexdigest(), got)


if __name__ == "__main__":
    unittest.main()
