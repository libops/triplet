#!/usr/bin/env python3

import csv
import json
import statistics
import sys
from collections import defaultdict


UNITS = {
    "B": 1,
    "KiB": 1024,
    "MiB": 1024**2,
    "GiB": 1024**3,
    "TiB": 1024**4,
    "KB": 1000,
    "MB": 1000**2,
    "GB": 1000**3,
    "TB": 1000**4,
}


def parse_percent(value):
    return float(value.strip().rstrip("%"))


def parse_size(value):
    first = value.split("/", 1)[0].strip()
    for unit in sorted(UNITS, key=len, reverse=True):
        if first.endswith(unit):
            return float(first[: -len(unit)].strip()) * UNITS[unit]
    return float(first)


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: benchmark-stats-summary.py container-stats.jsonl resource-summary.csv")

    groups = defaultdict(lambda: {"cpu": [], "mem": []})
    with open(sys.argv[1], encoding="utf-8") as fh:
        for line in fh:
            if not line.strip():
                continue
            row = json.loads(line)
            server = row["server"]
            stats = row["stats"]
            groups[server]["cpu"].append(parse_percent(stats["CPUPerc"]))
            groups[server]["mem"].append(parse_size(stats["MemUsage"]))

    with open(sys.argv[2], "w", newline="", encoding="utf-8") as fh:
        writer = csv.writer(fh)
        writer.writerow(["server", "samples", "mean_cpu_percent", "max_cpu_percent", "mean_mem_mib", "max_mem_mib"])
        for server, values in sorted(groups.items()):
            cpu = values["cpu"]
            mem = values["mem"]
            writer.writerow([
                server,
                len(cpu),
                f"{statistics.fmean(cpu):.3f}" if cpu else "",
                f"{max(cpu):.3f}" if cpu else "",
                f"{statistics.fmean(mem) / 1024 / 1024:.3f}" if mem else "",
                f"{max(mem) / 1024 / 1024:.3f}" if mem else "",
            ])


if __name__ == "__main__":
    main()
