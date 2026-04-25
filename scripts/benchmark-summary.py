#!/usr/bin/env python3

import csv
import statistics
import sys
from collections import defaultdict


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
        raise SystemExit("usage: benchmark-summary.py requests.csv summary.csv")

    groups = defaultdict(list)
    with open(sys.argv[1], newline="", encoding="utf-8") as fh:
        reader = csv.DictReader(fh)
        for row in reader:
            if row["curl_exit"] != "0":
                continue
            if not row["http_code"].startswith("2"):
                continue
            key = (row["server"], row["request_name"])
            groups[key].append(float(row["time_total"]))

    with open(sys.argv[2], "w", newline="", encoding="utf-8") as fh:
        writer = csv.writer(fh)
        writer.writerow(["server", "request_name", "count", "mean_s", "median_s", "p90_s", "p95_s", "p99_s", "min_s", "max_s"])
        for (server, request_name), values in sorted(groups.items()):
            writer.writerow([
                server,
                request_name,
                len(values),
                f"{statistics.fmean(values):.6f}",
                f"{statistics.median(values):.6f}",
                f"{percentile(values, 0.90):.6f}",
                f"{percentile(values, 0.95):.6f}",
                f"{percentile(values, 0.99):.6f}",
                f"{min(values):.6f}",
                f"{max(values):.6f}",
            ])


if __name__ == "__main__":
    main()
