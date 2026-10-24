#!/usr/bin/env python3
"""查询 ARTEX 意图的优先级 × 状态分布。

默认统计所有**未暂停**任务（paused=true 或 status=paused 的任务整条跳过），
按优先级展示每个状态的意图数量（待领 / 运行 / 暂停 / 完成 / 阻塞 / 超时 / 停止 / 删除），
也就是不再只统计 open（待领）。

用法：
    python query_intent_priority.py            # 全部未暂停任务
    python query_intent_priority.py --task 2   # 只看任务 #2
    python query_intent_priority.py --json     # 输出 JSON

连接串优先级与后端一致：
    ARTEX_PG_DSN 环境变量 > ARTEX_CONFIG 指向的 config.json > 脚本同目录 config.json

依赖：psycopg 3
    pip install "psycopg[binary]"
"""

import argparse
import json
import os
import sys
import unicodedata
from datetime import datetime, timezone
from pathlib import Path
from typing import NoReturn
from urllib.parse import quote
from zoneinfo import ZoneInfo

try:
    import psycopg
except ImportError:
    print('缺少 psycopg，请先安装：pip install "psycopg[binary]"', file=sys.stderr)
    raise SystemExit(1)

try:
    sys.stdout.reconfigure(encoding="utf-8")
except Exception:
    pass

DISPLAY_TZ = ZoneInfo("Asia/Shanghai")

# 状态展示顺序与中文标签；未知状态追加在末尾。
STATE_ORDER = ["open", "running", "paused", "done", "blocked", "exhausted", "stopped", "deleted"]
STATE_LABELS = {
    "open": "待领",
    "running": "运行",
    "paused": "暂停",
    "done": "完成",
    "blocked": "阻塞",
    "exhausted": "超时/耗尽",
    "stopped": "停止",
    "deleted": "删除",
}


def die(msg: str) -> NoReturn:
    print(msg, file=sys.stderr)
    raise SystemExit(1)


def find_config() -> Path:
    env_path = os.environ.get("ARTEX_CONFIG", "").strip()
    candidates = []
    if env_path:
        candidates.append(Path(env_path))
    candidates.append(Path(__file__).resolve().parent / "config.json")
    for path in candidates:
        if path.is_file():
            return path
    die(
        "找不到 config.json（可用 ARTEX_CONFIG 指定，或设置 ARTEX_PG_DSN）。\n"
        f"已尝试：{', '.join(str(p) for p in candidates)}"
    )


def build_dsn() -> str:
    """按后端 db.DSN() 的优先级解析连接串，返回 postgres:// URL。"""
    env_dsn = os.environ.get("ARTEX_PG_DSN", "").strip()
    if env_dsn:
        return env_dsn

    path = find_config()
    try:
        cfg = json.loads(path.read_text(encoding="utf-8"))
    except Exception as exc:
        die(f"读取 {path} 失败：{exc}")

    database = cfg.get("database") or {}
    if not isinstance(database, dict):
        die(f"{path} 的 database 字段格式不正确")

    if str(database.get("dsn", "")).strip():
        return str(database["dsn"]).strip()

    user = str(database.get("user", "")).strip()
    password = str(database.get("password", ""))
    host = str(database.get("host", "127.0.0.1")).strip() or "127.0.0.1"
    port = database.get("port", 5432) or 5432
    dbname = str(database.get("dbname", "artex")).strip() or "artex"
    sslmode = str(database.get("sslmode", "disable")).strip() or "disable"
    if not user:
        die(f"{path} 缺少 database.user（或改用 database.dsn / ARTEX_PG_DSN）")
    return (
        f"postgres://{quote(user)}:{quote(password)}@{host}:{port}/{quote(dbname)}"
        f"?sslmode={quote(sslmode)}"
    )


def display_width(text: str) -> int:
    return sum(2 if unicodedata.east_asian_width(ch) in ("W", "F") else 1 for ch in text)


def pad(text: str, width: int, align: str = "left") -> str:
    gap = max(0, width - display_width(text))
    if align == "right":
        return " " * gap + text
    return text + " " * gap


def print_table(headers: list[str], rows: list[list[str]], aligns: list[str] | None = None) -> None:
    aligns = aligns or ["left"] * len(headers)
    widths = [display_width(h) for h in headers]
    for row in rows:
        for i, cell in enumerate(row):
            widths[i] = max(widths[i], display_width(cell))
    print("  ".join(pad(h, widths[i]) for i, h in enumerate(headers)))
    print("  ".join("-" * widths[i] for i in range(len(headers))))
    for row in rows:
        print("  ".join(pad(cell, widths[i], aligns[i]) for i, cell in enumerate(row)))


def to_display_tz(value: datetime) -> datetime:
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    return value.astimezone(DISPLAY_TZ)


def iso_dt(value) -> str | None:
    if value is None:
        return None
    if isinstance(value, datetime):
        return to_display_tz(value).isoformat()
    return str(value)


def fmt_dt(value) -> str:
    if value is None:
        return "-"
    if isinstance(value, datetime):
        return to_display_tz(value).strftime("%Y-%m-%d %H:%M")
    return str(value)


def ordered_states(seen: set[str]) -> list[str]:
    out = [state for state in STATE_ORDER if state in seen]
    out.extend(sorted(state for state in seen if state not in STATE_ORDER))
    return out


def state_label(state: str) -> str:
    return STATE_LABELS.get(state, state)


def empty_bucket() -> dict[str, int]:
    return {}


def bucket_total(bucket: dict[str, int]) -> int:
    return sum(bucket.values())


def bucket_row(bucket: dict[str, int], states: list[str], total_ref: int | None = None) -> list[str]:
    row = [str(bucket.get(state, 0)) for state in states]
    total = bucket_total(bucket)
    row.append(str(total))
    if total_ref is not None:
        row.append(f"{total_ref * 100.0 / total:.1f}%" if total else "0.0%")
    return row


def main() -> None:
    parser = argparse.ArgumentParser(description="查询 ARTEX 意图的优先级 × 状态分布（跳过暂停任务）")
    parser.add_argument("--task", type=int, metavar="ID", help="只统计指定任务 id")
    parser.add_argument("--json", action="store_true", help="以 JSON 输出")
    args = parser.parse_args()

    dsn = build_dsn()
    where_task = ""
    params: tuple = ()
    if args.task is not None:
        where_task = " AND t.id = %s"
        params = (args.task,)

    # 排除暂停任务：paused 布尔与 status='paused' 都算。
    active_task = " AND NOT t.paused AND t.status <> 'paused'"

    with psycopg.connect(dsn) as conn:
        with conn.cursor() as cur:
            cur.execute(
                """
                SELECT t.id,
                       COALESCE(NULLIF(t.name, ''), t.description, '') AS label,
                       t.status,
                       t.paused
                FROM tasks t
                WHERE t.deleted_at IS NULL
                """
                + active_task
                + where_task
                + """
                ORDER BY t.id
                """,
                params,
            )
            tasks = cur.fetchall()

            cur.execute(
                """
                SELECT t.id,
                       COALESCE(NULLIF(t.name, ''), t.description, '') AS label
                FROM tasks t
                WHERE t.deleted_at IS NULL
                  AND (t.paused OR t.status = 'paused')
                """
                + where_task
                + """
                ORDER BY t.id
                """,
                params,
            )
            paused_tasks = cur.fetchall()

            cur.execute(
                """
                SELECT t.id, en.priority, en.state, count(*) AS n,
                       min(en.created_at) AS oldest,
                       max(en.created_at) AS newest
                FROM tasks t
                JOIN exploration_nodes en ON en.exploration_id = t.exploration_id
                WHERE t.deleted_at IS NULL
                """
                + active_task
                + where_task
                + """
                  AND en.kind = 'intent'
                GROUP BY t.id, en.priority, en.state
                ORDER BY t.id, en.priority DESC
                """,
                params,
            )
            intent_rows = cur.fetchall()

    # matrix[task_id][priority][state] = count；oldest/newest 只用于待领提示。
    matrix: dict[int, dict[int, dict[str, int]]] = {}
    oldest: dict[int, datetime] = {}
    newest: dict[int, datetime] = {}
    seen_states: set[str] = set()
    for task_id, priority, state, n, old, new in intent_rows:
        t_id, p = int(task_id), int(priority)
        matrix.setdefault(t_id, {}).setdefault(p, {})[state] = int(n)
        seen_states.add(state)
        # 最早/最近创建只看待领（open），不要把历史 done/deleted 混进来。
        if state == "open":
            if old and (p not in oldest or old < oldest[p]):
                oldest[p] = old
            if new and (p not in newest or new > newest[p]):
                newest[p] = new

    states = ordered_states(seen_states)

    def aggregate(task_id: int | None = None) -> dict[int, dict[str, int]]:
        out: dict[int, dict[str, int]] = {}
        for t_id, priorities in matrix.items():
            if task_id is not None and t_id != task_id:
                continue
            for priority, bucket in priorities.items():
                dst = out.setdefault(priority, {})
                for state, n in bucket.items():
                    dst[state] = dst.get(state, 0) + n
        return out

    def task_totals(task_id: int) -> dict[str, int]:
        out: dict[str, int] = {}
        for bucket in matrix.get(task_id, {}).values():
            for state, n in bucket.items():
                out[state] = out.get(state, 0) + n
        return out

    overall = aggregate()
    total_by_state: dict[str, int] = {}
    for bucket in overall.values():
        for state, n in bucket.items():
            total_by_state[state] = total_by_state.get(state, 0) + n
    total_open = total_by_state.get("open", 0)
    total_running = total_by_state.get("running", 0)
    total_intents = sum(total_by_state.values())

    if args.json:
        payload = {
            "excluded_paused_tasks": [
                {"id": int(r[0]), "label": r[1]} for r in paused_tasks
            ],
            "total_open": total_open,
            "total_running": total_running,
            "total_intents": total_intents,
            "total_by_state": total_by_state,
            "tasks": [
                {
                    "id": int(r[0]),
                    "label": r[1],
                    "status": r[2],
                    "paused": bool(r[3]),
                    "states": task_totals(int(r[0])),
                }
                for r in tasks
            ],
            "by_priority": {
                str(priority): {
                    **bucket,
                    "total": bucket_total(bucket),
                    "oldest_open": iso_dt(oldest.get(priority)),
                    "newest_open": iso_dt(newest.get(priority)),
                }
                for priority, bucket in sorted(overall.items(), reverse=True)
            },
            "per_task": {
                str(t_id): {
                    str(priority): {
                        **bucket,
                        "total": bucket_total(bucket),
                    }
                    for priority, bucket in sorted(priorities.items(), reverse=True)
                }
                for t_id, priorities in sorted(matrix.items())
            },
        }
        print(json.dumps(payload, ensure_ascii=False, indent=2))
        return

    scope = f"任务 #{args.task}" if args.task is not None else "全部未暂停任务"
    print(f"\nARTEX 意图优先级 × 状态分布 · {scope}")
    print(f"查询时间：{datetime.now().strftime('%Y-%m-%d %H:%M:%S')}（时间均为北京时间）")
    if paused_tasks:
        skipped = "、".join(f"#{int(r[0])}({r[1]})" for r in paused_tasks)
        print(f"已跳过暂停任务：{skipped}")
    print()

    if not tasks:
        if args.task is not None and paused_tasks:
            print(f"任务 #{args.task} 已暂停，按规则不统计。")
            return
        print("没有可统计的未暂停任务。")
        return

    print("任务概览")
    task_rows = []
    for r in tasks:
        t_id = int(r[0])
        totals = task_totals(t_id)
        total = sum(totals.values())
        open_n = totals.get("open", 0)
        running_n = totals.get("running", 0)
        task_rows.append(
            [
                f"#{t_id}",
                (r[1] or "")[:48],
                str(r[2] or ""),
                str(open_n),
                str(running_n),
                str(total - open_n - running_n),
                str(total),
            ]
        )
    print_table(
        ["ID", "任务", "状态", "待领", "运行", "其他", "合计"],
        task_rows,
        ["left", "left", "left", "right", "right", "right", "right"],
    )

    print(f"\n{scope}：待领 {total_open}，运行 {total_running}，意图总计 {total_intents}\n")

    print("优先级 × 状态（全部任务）")
    if not overall:
        print("（没有意图记录）")
    else:
        headers = ["优先级"] + [state_label(s) for s in states] + ["合计", "待领占比"]
        rows = []
        for priority in sorted(overall, reverse=True):
            bucket = overall[priority]
            rows.append([str(priority)] + bucket_row(bucket, states, bucket.get("open", 0)))
        rows.append(["合计"] + bucket_row(total_by_state, states, total_open))
        aligns = ["right"] * len(headers)
        print_table(headers, rows, aligns)

    print("\n待领优先级明细")
    if not overall or total_open == 0:
        print("（没有待领意图）")
    else:
        rows = []
        for priority in sorted(overall, reverse=True):
            n = overall[priority].get("open", 0)
            if n == 0:
                continue
            rows.append(
                [
                    str(priority),
                    str(n),
                    f"{n * 100.0 / total_open:.1f}%",
                    fmt_dt(oldest.get(priority)),
                    fmt_dt(newest.get(priority)),
                ]
            )
        print_table(["优先级", "待领", "占待领比", "最早创建", "最近创建"], rows, ["right", "right", "right", "left", "left"])

    if args.task is None and matrix:
        print("\n按任务拆分（优先级 × 状态）")
        for t_id in sorted(matrix):
            label = next((r[1] for r in tasks if int(r[0]) == t_id), "")
            totals = task_totals(t_id)
            print(f"\n任务 #{t_id}（{label}，待领 {totals.get('open', 0)}，意图总计 {sum(totals.values())}）")
            headers = ["优先级"] + [state_label(s) for s in states] + ["合计", "待领占比"]
            rows = []
            for priority in sorted(matrix[t_id], reverse=True):
                bucket = matrix[t_id][priority]
                rows.append([str(priority)] + bucket_row(bucket, states, bucket.get("open", 0)))
            rows.append(["合计"] + bucket_row(totals, states, totals.get("open", 0)))
            print_table(headers, rows, ["right"] * len(headers))

    print("\n领取顺序：priority DESC, id ASC（同优先级先领 id 小的）")


if __name__ == "__main__":
    main()
