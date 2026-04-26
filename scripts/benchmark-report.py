#!/usr/bin/env python3

import csv
import json
import statistics
import sys
from collections import Counter, defaultdict
from pathlib import Path


def percentile(values, pct):
    if not values:
        return None
    ordered = sorted(values)
    index = (len(ordered) - 1) * pct
    lower = int(index)
    upper = min(lower + 1, len(ordered) - 1)
    if lower == upper:
        return ordered[lower]
    weight = index - lower
    return ordered[lower] * (1 - weight) + ordered[upper] * weight


def fmt_ms(value):
    if value is None:
        return "-"
    return f"{value * 1000:.1f}"


def fmt_size(value):
    if value is None:
        return "-"
    units = ["B", "KiB", "MiB", "GiB"]
    size = float(value)
    unit = units[0]
    for unit in units:
        if abs(size) < 1024 or unit == units[-1]:
            break
        size /= 1024
    if unit == "B":
        return f"{size:.0f} {unit}"
    return f"{size:.1f} {unit}"


def fmt_size_field(value):
    if value in (None, ""):
        return "-"
    return fmt_size(float(value))


def fmt_size_per_request(row, key, successes):
    if successes == 0:
        return "-"
    value = row.get(key)
    if value in (None, ""):
        return "-"
    return fmt_size(float(value) / successes)


def fmt_rate(successes, total):
    if total == 0:
        return "-"
    return f"{successes}/{total} ({successes / total * 100:.0f}%)"


def fmt_ratio(triplet, other):
    if triplet is None or other is None or other == 0:
        return "-"
    ratio = triplet / other
    if ratio >= 1:
        return f"{ratio:.2f}x slower"
    return f"{1 / ratio:.2f}x faster"


def fmt_size_ratio(triplet, other):
    if triplet is None or other is None or other == 0:
        return "-"
    ratio = triplet / other
    if ratio >= 1:
        return f"{ratio:.2f}x larger"
    return f"{1 / ratio:.2f}x smaller"


def md_escape(value):
    return str(value).replace("|", "\\|")


def table(headers, rows):
    out = []
    out.append("| " + " | ".join(headers) + " |")
    out.append("| " + " | ".join(["---"] * len(headers)) + " |")
    for row in rows:
        out.append("| " + " | ".join(md_escape(cell) for cell in row) + " |")
    return "\n".join(out)


def read_requests(path):
    rows = []
    with open(path, newline="", encoding="utf-8") as fh:
        for row in csv.DictReader(fh):
            row["time_total"] = float(row["time_total"])
            row["size_download"] = int(float(row["size_download"]))
            row["ok"] = row["curl_exit"] == "0" and row["http_code"].startswith("2")
            rows.append(row)
    return rows


def summarize_requests(rows):
    groups = defaultdict(lambda: {"total": 0, "ok": 0, "times": [], "sizes": []})
    failures = Counter()
    for row in rows:
        key = (row["server"], row["request_name"])
        group = groups[key]
        group["total"] += 1
        if row["ok"]:
            group["ok"] += 1
            group["times"].append(row["time_total"])
            group["sizes"].append(row["size_download"])
        else:
            failures[(row["server"], row["request_name"], row["curl_exit"], row["http_code"])] += 1
    return groups, failures


def summarize_by(rows, key_fields):
    groups = defaultdict(lambda: {"total": 0, "ok": 0, "times": [], "sizes": []})
    for row in rows:
        key = tuple(row[field] for field in key_fields)
        group = groups[key]
        group["total"] += 1
        if row["ok"]:
            group["ok"] += 1
            group["times"].append(row["time_total"])
            group["sizes"].append(row["size_download"])
    return groups


def read_resources(path):
    if not path.exists():
        return {}
    with open(path, newline="", encoding="utf-8") as fh:
        return {row["server"]: row for row in csv.DictReader(fh)}


def format_duration(value):
    if value in (None, ""):
        return "-"
    return f"{float(value):.3f}s"


def format_interval(value):
    if value in (None, ""):
        return "-"
    return f"{value}s"


def request_order(rows):
    seen = []
    for row in rows:
        name = row["request_name"]
        if name not in seen:
            seen.append(name)
    return seen


def server_order(rows):
    names = sorted({row["server"] for row in rows})
    if "triplet" in names:
        names.remove("triplet")
        names.insert(0, "triplet")
    return names


def build_report(requests_csv, resource_csv, run_json):
    rows = read_requests(requests_csv)
    groups, failures = summarize_requests(rows)
    image_groups = summarize_by(rows, ["server", "image"])
    resources = read_resources(resource_csv)
    servers = server_order(rows)
    requests = request_order(rows)

    with open(run_json, encoding="utf-8") as fh:
        run = json.load(fh)
    image_count = len({row["image"] for row in rows})
    expected_per_server = image_count * len(requests) * int(run.get("passes", 0) or 0)
    expected_total = expected_per_server * len(servers)
    measured_requests = len(rows)

    lines = [
        f"# Benchmark Report: {run.get('run_id', '-')}",
        "",
        "## Run",
        "",
        table(
            ["Metric", "Value"],
            [
                ["Started", run.get("started_at", "-")],
                ["Mode", run.get("mode", "-")],
                ["Images", str(len({row["image"] for row in rows}))],
                ["Request types", str(len(requests))],
                ["Passes", str(run.get("passes", "-"))],
                ["Warmup passes", str(run.get("warmup_passes", "-"))],
                ["Concurrency", str(run.get("concurrency", "-"))],
                ["Measured duration", format_duration(run.get("measured_duration_seconds"))],
                ["Measured requests", str(measured_requests)],
                ["Expected requests/server", str(expected_per_server)],
                ["Expected requests total", str(expected_total)],
                ["Stats interval", format_interval(run.get("stats_interval_seconds"))],
                ["Triplet image", run.get("triplet_image", "-")],
                ["Cantaloupe image", run.get("cantaloupe_image", "-")],
                ["Triplet cache", run.get("triplet_cache", "-")],
                ["Cantaloupe cache", run.get("cantaloupe_cache", "-")],
                ["Triplet color management", run.get("triplet_color_management", "-")],
                ["Triplet load access", run.get("triplet_load_access", "-")],
                ["pprof", "enabled" if run.get("profile_enabled") else "disabled"],
            ],
        ),
        "",
        "## Request Comparison",
        "",
    ]

    if len(servers) == 2 and "triplet" in servers:
        other = next(server for server in servers if server != "triplet")
        comparison_rows = []
        for request_name in requests:
            triplet = groups.get(("triplet", request_name), {"total": 0, "ok": 0, "times": [], "sizes": []})
            peer = groups.get((other, request_name), {"total": 0, "ok": 0, "times": [], "sizes": []})
            triplet_median = statistics.median(triplet["times"]) if triplet["times"] else None
            peer_median = statistics.median(peer["times"]) if peer["times"] else None
            triplet_size = statistics.fmean(triplet["sizes"]) if triplet["sizes"] else None
            peer_size = statistics.fmean(peer["sizes"]) if peer["sizes"] else None
            comparison_rows.append(
                [
                    request_name,
                    fmt_rate(triplet["ok"], triplet["total"]),
                    fmt_ms(triplet_median),
                    fmt_ms(percentile(triplet["times"], 0.90)),
                    fmt_ms(percentile(triplet["times"], 0.95)),
                    fmt_ms(percentile(triplet["times"], 0.99)),
                    fmt_size(triplet_size),
                    fmt_rate(peer["ok"], peer["total"]),
                    fmt_ms(peer_median),
                    fmt_ms(percentile(peer["times"], 0.90)),
                    fmt_ms(percentile(peer["times"], 0.95)),
                    fmt_ms(percentile(peer["times"], 0.99)),
                    fmt_size(peer_size),
                    fmt_ratio(triplet_median, peer_median),
                    fmt_size_ratio(triplet_size, peer_size),
                ]
            )
        lines.extend(
            [
                table(
                    [
                        "Request",
                        "Triplet OK",
                        "Triplet med ms",
                        "Triplet p90 ms",
                        "Triplet p95 ms",
                        "Triplet p99 ms",
                        "Triplet bytes",
                        f"{other} OK",
                        f"{other} med ms",
                        f"{other} p90 ms",
                        f"{other} p95 ms",
                        f"{other} p99 ms",
                        f"{other} bytes",
                        "Triplet vs peer",
                        "Bytes vs peer",
                    ],
                    comparison_rows,
                ),
                "",
            ]
        )
    else:
        latency_rows = []
        for request_name in requests:
            for server in servers:
                stats = groups.get((server, request_name), {"total": 0, "ok": 0, "times": [], "sizes": []})
                latency_rows.append(
                    [
                        request_name,
                        server,
                        fmt_rate(stats["ok"], stats["total"]),
                        fmt_ms(statistics.median(stats["times"]) if stats["times"] else None),
                        fmt_ms(percentile(stats["times"], 0.90)),
                        fmt_ms(percentile(stats["times"], 0.95)),
                        fmt_ms(percentile(stats["times"], 0.99)),
                        fmt_ms(statistics.fmean(stats["times"]) if stats["times"] else None),
                        fmt_size(statistics.fmean(stats["sizes"]) if stats["sizes"] else None),
                    ]
                )
        lines.extend(
            [
                table(
                    ["Request", "Server", "Success", "Median ms", "p90 ms", "p95 ms", "p99 ms", "Mean ms", "Mean bytes"],
                    latency_rows,
                ),
                "",
            ]
        )

    lines.extend(["## Per Image", ""])

    images = sorted({row["image"] for row in rows})
    if len(servers) == 2 and "triplet" in servers:
        other = next(server for server in servers if server != "triplet")
        per_image_rows = []
        for image in images:
            triplet = image_groups.get(("triplet", image), {"total": 0, "ok": 0, "times": [], "sizes": []})
            peer = image_groups.get((other, image), {"total": 0, "ok": 0, "times": [], "sizes": []})
            triplet_median = statistics.median(triplet["times"]) if triplet["times"] else None
            peer_median = statistics.median(peer["times"]) if peer["times"] else None
            triplet_size = statistics.fmean(triplet["sizes"]) if triplet["sizes"] else None
            peer_size = statistics.fmean(peer["sizes"]) if peer["sizes"] else None
            per_image_rows.append(
                [
                    image,
                    fmt_rate(triplet["ok"], triplet["total"]),
                    fmt_ms(triplet_median),
                    fmt_ms(percentile(triplet["times"], 0.99)),
                    fmt_size(triplet_size),
                    fmt_rate(peer["ok"], peer["total"]),
                    fmt_ms(peer_median),
                    fmt_ms(percentile(peer["times"], 0.99)),
                    fmt_size(peer_size),
                    fmt_ratio(triplet_median, peer_median),
                    fmt_size_ratio(triplet_size, peer_size),
                ]
            )
        lines.extend(
            [
                table(
                    [
                        "Image",
                        "Triplet OK",
                        "Triplet med ms",
                        "Triplet p99 ms",
                        "Triplet bytes",
                        f"{other} OK",
                        f"{other} med ms",
                        f"{other} p99 ms",
                        f"{other} bytes",
                        "Triplet vs peer",
                        "Bytes vs peer",
                    ],
                    per_image_rows,
                ),
                "",
            ]
        )
    else:
        per_image_rows = []
        for image in images:
            for server in servers:
                stats = image_groups.get((server, image), {"total": 0, "ok": 0, "times": [], "sizes": []})
                per_image_rows.append(
                    [
                        image,
                        server,
                        fmt_rate(stats["ok"], stats["total"]),
                        fmt_ms(statistics.median(stats["times"]) if stats["times"] else None),
                        fmt_ms(percentile(stats["times"], 0.99)),
                        fmt_size(statistics.fmean(stats["sizes"]) if stats["sizes"] else None),
                    ]
                )
        lines.extend([table(["Image", "Server", "Success", "Median ms", "p99 ms", "Mean bytes"], per_image_rows), ""])

    lines.extend(["## Overall", ""])

    overall_rows = []
    ok_by_server = {}
    for server in servers:
        server_rows = [row for row in rows if row["server"] == server]
        ok_rows = [row for row in server_rows if row["ok"]]
        ok_by_server[server] = len(ok_rows)
        times = [row["time_total"] for row in ok_rows]
        sizes = [row["size_download"] for row in ok_rows]
        overall_rows.append(
            [
                server,
                fmt_rate(len(ok_rows), len(server_rows)),
                fmt_ms(statistics.median(times) if times else None),
                fmt_ms(percentile(times, 0.90)),
                fmt_ms(percentile(times, 0.95)),
                fmt_ms(percentile(times, 0.99)),
                fmt_ms(statistics.fmean(times) if times else None),
                fmt_size(statistics.fmean(sizes) if sizes else None),
            ]
        )
    lines.extend(
        [
            table(["Server", "Success", "Median ms", "p90 ms", "p95 ms", "p99 ms", "Mean ms", "Mean bytes"], overall_rows),
            "",
            "## Resources",
            "",
        ]
    )

    resource_rows = []
    measured_duration = float(run.get("measured_duration_seconds") or 0)
    for server in servers:
        row = resources.get(server, {})
        successes = ok_by_server.get(server, 0)
        cpu_per_request = "-"
        mib_per_request = "-"
        if successes:
            try:
                mean_cpu = float(row.get("mean_cpu_percent", ""))
                cpu_per_request = f"{(mean_cpu / 100 * measured_duration / successes):.4f}"
            except ValueError:
                pass
            try:
                mean_mem = float(row.get("mean_mem_mib", ""))
                mib_per_request = f"{(mean_mem / successes):.3f}"
            except ValueError:
                pass
        resource_rows.append(
            [
                server,
                row.get("samples", "-"),
                cpu_per_request,
                row.get("mean_cpu_percent", "-"),
                row.get("p90_cpu_percent", "-"),
                row.get("p95_cpu_percent", "-"),
                row.get("p99_cpu_percent", "-"),
                row.get("max_cpu_percent", "-"),
                mib_per_request,
                row.get("mean_mem_mib", "-"),
                row.get("p90_mem_mib", "-"),
                row.get("p95_mem_mib", "-"),
                row.get("p99_mem_mib", "-"),
                row.get("max_mem_mib", "-"),
            ]
        )
    lines.extend(
        [
            table(
                ["Server", "Samples", "CPU sec / OK req", "Mean CPU %", "p90 CPU %", "p95 CPU %", "p99 CPU %", "Max CPU %", "Mean Mem MiB / OK req", "Mean Mem MiB", "p90 Mem MiB", "p95 Mem MiB", "p99 Mem MiB", "Max Mem MiB"],
                resource_rows,
            ),
            "",
        ]
    )

    io_rows = []
    for server in servers:
        row = resources.get(server, {})
        successes = ok_by_server.get(server, 0)
        io_rows.append(
            [
                server,
                fmt_size_field(row.get("block_read_delta_bytes")),
                fmt_size_field(row.get("block_write_delta_bytes")),
                fmt_size_per_request(row, "block_read_delta_bytes", successes),
                fmt_size_per_request(row, "block_write_delta_bytes", successes),
                fmt_size_field(row.get("net_rx_delta_bytes")),
                fmt_size_field(row.get("net_tx_delta_bytes")),
                fmt_size_per_request(row, "net_rx_delta_bytes", successes),
                fmt_size_per_request(row, "net_tx_delta_bytes", successes),
            ]
        )
    lines.extend(
        [
            "## I/O",
            "",
            table(
                ["Server", "Block read", "Block write", "Block read / OK req", "Block write / OK req", "Net rx", "Net tx", "Net rx / OK req", "Net tx / OK req"],
                io_rows,
            ),
            "",
        ]
    )

    if failures:
        failure_rows = [
            [server, request_name, curl_exit, http_code, count]
            for (server, request_name, curl_exit, http_code), count in sorted(failures.items())
        ]
        lines.extend(
            [
                "## Failures",
                "",
                table(["Server", "Request", "curl exit", "HTTP", "Count"], failure_rows),
                "",
            ]
        )

    return "\n".join(lines).rstrip() + "\n"


def main():
    if len(sys.argv) != 5:
        raise SystemExit("usage: benchmark-report.py requests.csv resource-summary.csv run.json report.md")

    report = build_report(Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3]))
    Path(sys.argv[4]).write_text(report, encoding="utf-8")


if __name__ == "__main__":
    main()
