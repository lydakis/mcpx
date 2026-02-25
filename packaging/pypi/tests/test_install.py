from __future__ import annotations

import os
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

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


if __name__ == "__main__":
    unittest.main()
