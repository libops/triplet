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
    "kB": 1000,
    "MB": 1000**2,
    "GB": 1000**3,
    "TB": 1000**4,
}


def parse_percent(value):
    return float(value.strip().rstrip("%"))


def parse_size(value):
    first = value.split("/", 1)[0].strip()
    return parse_size_value(first)


def parse_size_value(value):
    value = value.strip()
    if not value:
        return 0
    for unit in sorted(UNITS, key=len, reverse=True):
        if value.endswith(unit):
            return float(value[: -len(unit)].strip()) * UNITS[unit]
    return float(value)


def parse_size_pair(value):
    left, _, right = value.partition("/")
    return parse_size_value(left), parse_size_value(right)


def delta(values):
    if not values:
        return ""
    return max(values) - min(values)


def percentile(values, pct):
    if not values:
        return ""
    ordered = sorted(values)
    index = (len(ordered) - 1) * pct
    lower = int(index)
    upper = min(lower + 1, len(ordered) - 1)
    if lower == upper:
        return ordered[lower]
    weight = index - lower
    return ordered[lower] * (1 - weight) + ordered[upper] * weight


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: benchmark-stats-summary.py container-stats.jsonl resource-summary.csv")

    groups = defaultdict(lambda: {"cpu": [], "mem": [], "block_read": [], "block_write": [], "net_rx": [], "net_tx": []})
    with open(sys.argv[1], encoding="utf-8") as fh:
        for line in fh:
            if not line.strip():
                continue
            row = json.loads(line)
            server = row["server"]
            stats = row["stats"]
            groups[server]["cpu"].append(parse_percent(stats["CPUPerc"]))
            groups[server]["mem"].append(parse_size(stats["MemUsage"]))
            if "BlockIO" in stats:
                read, write = parse_size_pair(stats["BlockIO"])
                groups[server]["block_read"].append(read)
                groups[server]["block_write"].append(write)
            if "NetIO" in stats:
                rx, tx = parse_size_pair(stats["NetIO"])
                groups[server]["net_rx"].append(rx)
                groups[server]["net_tx"].append(tx)

    with open(sys.argv[2], "w", newline="", encoding="utf-8") as fh:
        writer = csv.writer(fh)
        writer.writerow([
            "server",
            "samples",
            "mean_cpu_percent",
            "p90_cpu_percent",
            "p95_cpu_percent",
            "p99_cpu_percent",
            "max_cpu_percent",
            "mean_mem_mib",
            "p90_mem_mib",
            "p95_mem_mib",
            "p99_mem_mib",
            "max_mem_mib",
            "block_read_delta_bytes",
            "block_write_delta_bytes",
            "block_read_max_bytes",
            "block_write_max_bytes",
            "net_rx_delta_bytes",
            "net_tx_delta_bytes",
            "net_rx_max_bytes",
            "net_tx_max_bytes",
        ])
        for server, values in sorted(groups.items()):
            cpu = values["cpu"]
            mem = values["mem"]
            block_read = values["block_read"]
            block_write = values["block_write"]
            net_rx = values["net_rx"]
            net_tx = values["net_tx"]
            writer.writerow([
                server,
                len(cpu),
                f"{statistics.fmean(cpu):.3f}" if cpu else "",
                f"{percentile(cpu, 0.90):.3f}" if cpu else "",
                f"{percentile(cpu, 0.95):.3f}" if cpu else "",
                f"{percentile(cpu, 0.99):.3f}" if cpu else "",
                f"{max(cpu):.3f}" if cpu else "",
                f"{statistics.fmean(mem) / 1024 / 1024:.3f}" if mem else "",
                f"{percentile(mem, 0.90) / 1024 / 1024:.3f}" if mem else "",
                f"{percentile(mem, 0.95) / 1024 / 1024:.3f}" if mem else "",
                f"{percentile(mem, 0.99) / 1024 / 1024:.3f}" if mem else "",
                f"{max(mem) / 1024 / 1024:.3f}" if mem else "",
                f"{delta(block_read):.0f}" if block_read else "",
                f"{delta(block_write):.0f}" if block_write else "",
                f"{max(block_read):.0f}" if block_read else "",
                f"{max(block_write):.0f}" if block_write else "",
                f"{delta(net_rx):.0f}" if net_rx else "",
                f"{delta(net_tx):.0f}" if net_tx else "",
                f"{max(net_rx):.0f}" if net_rx else "",
                f"{max(net_tx):.0f}" if net_tx else "",
            ])


if __name__ == "__main__":
    main()
