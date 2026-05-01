#!/usr/bin/env python3

import sys
from pathlib import Path


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: extract-benchmark-tldr.py report.md")

    report = Path(sys.argv[1]).read_text(encoding="utf-8").splitlines()
    out = []
    in_tldr = False
    for line in report:
        if line == "## Summary":
            in_tldr = True
            out.append(line)
            continue
        if in_tldr and line.startswith("## "):
            break
        if in_tldr or line.startswith("# "):
            out.append(line)

    print("\n".join(out).rstrip())


if __name__ == "__main__":
    main()
