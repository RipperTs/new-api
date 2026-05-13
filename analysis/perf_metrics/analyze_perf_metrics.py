#!/usr/bin/env python3
import argparse
import csv
import json
import os
from datetime import datetime
from pathlib import Path
from typing import Any, Dict, List, Optional

import pymysql


LOG_TYPE_CONSUME = 2


def env_int(name: str, default: int) -> int:
    value = os.getenv(name)
    return int(value) if value else default


def get_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Analyze recent API performance metrics from logs.")
    parser.add_argument("--host", default=os.getenv("DB_HOST", "127.0.0.1"))
    parser.add_argument("--port", type=int, default=env_int("DB_PORT", 3306))
    parser.add_argument("--database", default=os.getenv("DB_NAME", "proxy_api"))
    parser.add_argument("--user", default=os.getenv("DB_USER", "proxy_api"))
    parser.add_argument("--password", default=os.getenv("DB_PASSWORD", ""))
    parser.add_argument("--days", type=int, default=30)
    parser.add_argument("--output-dir", default="analysis/perf_metrics/reports")
    parser.add_argument("--top", type=int, default=10)
    return parser.parse_args()


def connect(args: argparse.Namespace):
    if not args.password:
        raise SystemExit("缺少数据库密码，请通过 DB_PASSWORD 或 --password 传入。")
    return pymysql.connect(
        host=args.host,
        port=args.port,
        user=args.user,
        password=args.password,
        database=args.database,
        charset="utf8mb4",
        connect_timeout=10,
        read_timeout=120,
        cursorclass=pymysql.cursors.DictCursor,
    )


def fetch_one(cur, sql: str, params: tuple = ()) -> Dict[str, Any]:
    cur.execute(sql, params)
    return cur.fetchone() or {}


def fetch_all(cur, sql: str, params: tuple = ()) -> List[Dict[str, Any]]:
    cur.execute(sql, params)
    return list(cur.fetchall())


def percentile_nearest(cur, start_ts: int, percentile: float, positive_tokens_only: bool = False) -> Optional[int]:
    positive_filter = "AND (prompt_tokens + completion_tokens) > 0" if positive_tokens_only else ""
    count_row = fetch_one(
        cur,
        f"""
        SELECT COUNT(*) AS cnt
        FROM logs
        WHERE type = %s AND created_at >= %s {positive_filter}
        """,
        (LOG_TYPE_CONSUME, start_ts),
    )
    count = int(count_row.get("cnt") or 0)
    if count == 0:
        return None

    offset = max(0, int((count - 1) * percentile))
    row = fetch_one(
        cur,
        f"""
        SELECT prompt_tokens
        FROM logs
        WHERE type = %s AND created_at >= %s {positive_filter}
        ORDER BY prompt_tokens
        LIMIT 1 OFFSET %s
        """,
        (LOG_TYPE_CONSUME, start_ts, offset),
    )
    return int(row["prompt_tokens"]) if row else None


def to_k(value: Optional[float]) -> Optional[float]:
    if value is None:
        return None
    return round(float(value) / 1000, 3)


def normalize_row(row: Dict[str, Any]) -> Dict[str, Any]:
    result = {}
    for key, value in row.items():
        if isinstance(value, datetime):
            result[key] = value.isoformat(sep=" ")
        elif isinstance(value, bytes):
            result[key] = value.decode("utf-8", errors="replace")
        else:
            result[key] = value
    return result


def fetch_context(cur, start_ts: int, positive_tokens_only: bool = False) -> Dict[str, Any]:
    positive_filter = "AND (prompt_tokens + completion_tokens) > 0" if positive_tokens_only else ""
    context = fetch_one(
        cur,
        f"""
        SELECT
            COUNT(*) AS request_count,
            MAX(prompt_tokens) AS max_context_tokens,
            AVG(prompt_tokens) AS avg_context_tokens,
            MAX(prompt_tokens + completion_tokens) AS max_total_tokens,
            AVG(prompt_tokens + completion_tokens) AS avg_total_tokens
        FROM logs
        WHERE type = %s AND created_at >= %s {positive_filter}
        """,
        (LOG_TYPE_CONSUME, start_ts),
    )
    context.update(
        {
            "p50_context_tokens": percentile_nearest(cur, start_ts, 0.50, positive_tokens_only),
            "p90_context_tokens": percentile_nearest(cur, start_ts, 0.90, positive_tokens_only),
            "p95_context_tokens": percentile_nearest(cur, start_ts, 0.95, positive_tokens_only),
            "p99_context_tokens": percentile_nearest(cur, start_ts, 0.99, positive_tokens_only),
        }
    )
    context["max_context_k"] = to_k(context.get("max_context_tokens"))
    context["avg_context_k"] = to_k(context.get("avg_context_tokens"))
    context["p50_context_k"] = to_k(context.get("p50_context_tokens"))
    context["p90_context_k"] = to_k(context.get("p90_context_tokens"))
    context["p95_context_k"] = to_k(context.get("p95_context_tokens"))
    context["p99_context_k"] = to_k(context.get("p99_context_tokens"))
    return normalize_row(context)


def fetch_request_peaks(cur, start_ts: int, positive_tokens_only: bool = False) -> Dict[str, Any]:
    positive_filter = "AND (prompt_tokens + completion_tokens) > 0" if positive_tokens_only else ""
    peak_rps = fetch_one(
        cur,
        f"""
        SELECT created_at AS ts, COUNT(*) AS requests
        FROM logs
        WHERE type = %s AND created_at >= %s {positive_filter}
        GROUP BY created_at
        ORDER BY requests DESC
        LIMIT 1
        """,
        (LOG_TYPE_CONSUME, start_ts),
    )
    peak_rpm = fetch_one(
        cur,
        f"""
        SELECT (created_at DIV 60) * 60 AS minute_ts, COUNT(*) AS rpm
        FROM logs
        WHERE type = %s AND created_at >= %s {positive_filter}
        GROUP BY minute_ts
        ORDER BY rpm DESC
        LIMIT 1
        """,
        (LOG_TYPE_CONSUME, start_ts),
    )
    if peak_rps.get("ts"):
        peak_rps["time"] = datetime.fromtimestamp(int(peak_rps["ts"])).isoformat(sep=" ")
    if peak_rpm.get("minute_ts"):
        peak_rpm["time"] = datetime.fromtimestamp(int(peak_rpm["minute_ts"])).isoformat(sep=" ")
    return {
        "requests_per_second": normalize_row(peak_rps),
        "requests_per_minute": normalize_row(peak_rpm),
    }


def analyze(conn, days: int, top: int) -> Dict[str, Any]:
    with conn.cursor() as cur:
        start_row = fetch_one(cur, "SELECT UNIX_TIMESTAMP(NOW() - INTERVAL %s DAY) AS start_ts", (days,))
        start_ts = int(start_row["start_ts"])

        overview = fetch_one(
            cur,
            """
            SELECT
                COUNT(*) AS request_count,
                MIN(created_at) AS first_ts,
                MAX(created_at) AS last_ts,
                COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
                COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
                COALESCE(SUM(prompt_tokens + completion_tokens), 0) AS total_tokens,
                COALESCE(SUM(quota), 0) AS total_quota,
                AVG(use_time) AS avg_use_time,
                MAX(use_time) AS max_use_time
            FROM logs
            WHERE type = %s AND created_at >= %s
            """,
            (LOG_TYPE_CONSUME, start_ts),
        )

        context = fetch_context(cur, start_ts)
        positive_context = fetch_context(cur, start_ts, positive_tokens_only=True)

        peak_second = fetch_one(
            cur,
            """
            SELECT
                created_at AS ts,
                COUNT(*) AS requests,
                SUM(prompt_tokens + completion_tokens) AS tokens,
                SUM(prompt_tokens) AS prompt_tokens,
                SUM(completion_tokens) AS completion_tokens
            FROM logs
            WHERE type = %s AND created_at >= %s
            GROUP BY created_at
            ORDER BY tokens DESC
            LIMIT 1
            """,
            (LOG_TYPE_CONSUME, start_ts),
        )

        peak_minute = fetch_one(
            cur,
            """
            SELECT
                (created_at DIV 60) * 60 AS minute_ts,
                COUNT(*) AS rpm,
                SUM(prompt_tokens + completion_tokens) AS tpm,
                SUM(prompt_tokens) AS prompt_tpm,
                SUM(completion_tokens) AS completion_tpm
            FROM logs
            WHERE type = %s AND created_at >= %s
            GROUP BY minute_ts
            ORDER BY tpm DESC
            LIMIT 1
            """,
            (LOG_TYPE_CONSUME, start_ts),
        )

        request_peaks = fetch_request_peaks(cur, start_ts)
        positive_request_peaks = fetch_request_peaks(cur, start_ts, positive_tokens_only=True)

        by_model = fetch_all(
            cur,
            """
            SELECT
                model_name,
                COUNT(*) AS requests,
                SUM(prompt_tokens + completion_tokens) AS total_tokens,
                SUM(prompt_tokens) AS prompt_tokens,
                SUM(completion_tokens) AS completion_tokens,
                MAX(prompt_tokens) AS max_context_tokens,
                AVG(prompt_tokens) AS avg_context_tokens,
                AVG(use_time) AS avg_use_time
            FROM logs
            WHERE type = %s AND created_at >= %s
            GROUP BY model_name
            ORDER BY total_tokens DESC
            LIMIT %s
            """,
            (LOG_TYPE_CONSUME, start_ts, top),
        )

        by_channel = fetch_all(
            cur,
            """
            SELECT
                channel_id,
                COUNT(*) AS requests,
                SUM(prompt_tokens + completion_tokens) AS total_tokens,
                SUM(prompt_tokens) AS prompt_tokens,
                SUM(completion_tokens) AS completion_tokens,
                MAX(prompt_tokens) AS max_context_tokens,
                AVG(prompt_tokens) AS avg_context_tokens,
                AVG(use_time) AS avg_use_time
            FROM logs
            WHERE type = %s AND created_at >= %s
            GROUP BY channel_id
            ORDER BY total_tokens DESC
            LIMIT %s
            """,
            (LOG_TYPE_CONSUME, start_ts, top),
        )

    for row in by_model + by_channel:
        row["avg_context_k"] = to_k(row.get("avg_context_tokens"))
        row["max_context_k"] = to_k(row.get("max_context_tokens"))

    overview = normalize_row(overview)
    if overview.get("first_ts"):
        overview["first_time"] = datetime.fromtimestamp(int(overview["first_ts"])).isoformat(sep=" ")
    if overview.get("last_ts"):
        overview["last_time"] = datetime.fromtimestamp(int(overview["last_ts"])).isoformat(sep=" ")

    if peak_second.get("ts"):
        peak_second["time"] = datetime.fromtimestamp(int(peak_second["ts"])).isoformat(sep=" ")
    if peak_minute.get("minute_ts"):
        peak_minute["time"] = datetime.fromtimestamp(int(peak_minute["minute_ts"])).isoformat(sep=" ")

    return {
        "generated_at": datetime.now().isoformat(sep=" "),
        "days": days,
        "start_ts": start_ts,
        "overview": overview,
        "context": context,
        "positive_token_context": positive_context,
        "peaks": {
            "tokens_per_second": normalize_row(peak_second),
            "requests_per_second": request_peaks["requests_per_second"],
            "tokens_per_minute": normalize_row(peak_minute),
            "requests_per_minute": request_peaks["requests_per_minute"],
        },
        "positive_token_request_peaks": positive_request_peaks,
        "top_models": [normalize_row(row) for row in by_model],
        "top_channels": [normalize_row(row) for row in by_channel],
    }


def write_outputs(report: Dict[str, Any], output_dir: str) -> Dict[str, str]:
    out_dir = Path(output_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    suffix = datetime.now().strftime("%Y%m%d_%H%M%S")
    json_path = out_dir / f"perf_metrics_{suffix}.json"
    csv_path = out_dir / f"perf_metrics_{suffix}.csv"

    json_path.write_text(json.dumps(report, ensure_ascii=False, indent=2, default=str), encoding="utf-8")

    rows = [
        ("request_count", report["overview"].get("request_count")),
        ("total_tokens", report["overview"].get("total_tokens")),
        ("prompt_tokens", report["overview"].get("prompt_tokens")),
        ("completion_tokens", report["overview"].get("completion_tokens")),
        ("peak_tokens_per_second", report["peaks"]["tokens_per_second"].get("tokens")),
        ("peak_tokens_per_second_time", report["peaks"]["tokens_per_second"].get("time")),
        ("peak_requests_per_second", report["peaks"]["requests_per_second"].get("requests")),
        ("peak_requests_per_second_time", report["peaks"]["requests_per_second"].get("time")),
        ("peak_tpm", report["peaks"]["tokens_per_minute"].get("tpm")),
        ("peak_tpm_time", report["peaks"]["tokens_per_minute"].get("time")),
        ("peak_rpm", report["peaks"]["requests_per_minute"].get("rpm")),
        ("peak_rpm_time", report["peaks"]["requests_per_minute"].get("time")),
        ("max_context_k", report["context"].get("max_context_k")),
        ("avg_context_k", report["context"].get("avg_context_k")),
        ("p50_context_k", report["context"].get("p50_context_k")),
        ("p90_context_k", report["context"].get("p90_context_k")),
        ("p95_context_k", report["context"].get("p95_context_k")),
        ("p99_context_k", report["context"].get("p99_context_k")),
        ("positive_token_request_count", report["positive_token_context"].get("request_count")),
        ("positive_token_avg_context_k", report["positive_token_context"].get("avg_context_k")),
        ("positive_token_p50_context_k", report["positive_token_context"].get("p50_context_k")),
        ("positive_token_peak_rps", report["positive_token_request_peaks"]["requests_per_second"].get("requests")),
        ("positive_token_peak_rpm", report["positive_token_request_peaks"]["requests_per_minute"].get("rpm")),
    ]
    with csv_path.open("w", newline="", encoding="utf-8") as file:
        writer = csv.writer(file)
        writer.writerow(["metric", "value"])
        writer.writerows(rows)

    return {"json": str(json_path), "csv": str(csv_path)}


def print_summary(report: Dict[str, Any], paths: Dict[str, str]) -> None:
    overview = report["overview"]
    context = report["context"]
    positive_context = report["positive_token_context"]
    peaks = report["peaks"]
    positive_peaks = report["positive_token_request_peaks"]

    print("最近 {days} 天性能指标".format(days=report["days"]))
    print(f"时间范围: {overview.get('first_time')} ~ {overview.get('last_time')}")
    print(f"请求数: {overview.get('request_count'):,}")
    print(f"总 Tokens: {overview.get('total_tokens'):,}")
    print(f"输入/输出 Tokens: {overview.get('prompt_tokens'):,} / {overview.get('completion_tokens'):,}")
    print(f"峰值 Tokens/秒: {peaks['tokens_per_second'].get('tokens'):,} @ {peaks['tokens_per_second'].get('time')}")
    print(f"峰值 RPS: {peaks['requests_per_second'].get('requests'):,} @ {peaks['requests_per_second'].get('time')}")
    print(f"峰值 TPM: {peaks['tokens_per_minute'].get('tpm'):,} @ {peaks['tokens_per_minute'].get('time')}")
    print(f"峰值 RPM: {peaks['requests_per_minute'].get('rpm'):,} @ {peaks['requests_per_minute'].get('time')}")
    print(f"上下文最高/平均: {context.get('max_context_k')}K / {context.get('avg_context_k')}K")
    print(
        "上下文 P50/P90/P95/P99: "
        f"{context.get('p50_context_k')}K / {context.get('p90_context_k')}K / "
        f"{context.get('p95_context_k')}K / {context.get('p99_context_k')}K"
    )
    print(
        "有效 Token 请求上下文平均/P50/P95: "
        f"{positive_context.get('avg_context_k')}K / {positive_context.get('p50_context_k')}K / "
        f"{positive_context.get('p95_context_k')}K"
    )
    print(
        "有效 Token 请求峰值 RPS/RPM: "
        f"{positive_peaks['requests_per_second'].get('requests')} / "
        f"{positive_peaks['requests_per_minute'].get('rpm')}"
    )
    print(f"JSON: {paths['json']}")
    print(f"CSV: {paths['csv']}")


def main() -> None:
    args = get_args()
    conn = connect(args)
    try:
        report = analyze(conn, args.days, args.top)
    finally:
        conn.close()

    paths = write_outputs(report, args.output_dir)
    print_summary(report, paths)


if __name__ == "__main__":
    main()
