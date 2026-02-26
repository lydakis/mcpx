from __future__ import annotations

import os
import shutil
import sys

from .install import ensure_binary


def _exec(command: str, argv: list[str]) -> None:
    os.execv(command, [command, *argv])


def main() -> int:
    argv = sys.argv[1:]

    try:
        binary = ensure_binary()
        _exec(str(binary), argv)
    except Exception as error:
        fallback = shutil.which("mcpx")
        if fallback is not None:
            _exec(fallback, argv)

        sys.stderr.write(f"mcpx: {error}\n")
        return 1

    return 1


if __name__ == "__main__":
    raise SystemExit(main())
