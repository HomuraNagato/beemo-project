#!/usr/bin/env python3
from __future__ import annotations

import sys
from pathlib import Path


def main() -> int:
    if len(sys.argv) != 4:
        print("usage: config-value.py <config.yaml> <section> <key>", file=sys.stderr)
        return 2

    path = Path(sys.argv[1])
    section = sys.argv[2]
    key = sys.argv[3]
    current = None

    for raw_line in path.read_text(encoding="utf-8").splitlines():
        stripped = raw_line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if not raw_line.startswith((" ", "\t")) and stripped.endswith(":"):
            current = stripped[:-1]
            continue
        if current == section and stripped.startswith(key + ":"):
            value = stripped.split(":", 1)[1].strip().strip("'\"")
            print(value)
            return 0

    return 1


if __name__ == "__main__":
    raise SystemExit(main())
