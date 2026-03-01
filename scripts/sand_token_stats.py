#!/usr/bin/env python3
"""Aggregate per-day input/output tokens per model for *-sand agents.

Reads h2 session event logs from:
  <h2_dir>/sessions/*-sand/events.jsonl

Usage:
  python3 scripts/sand_token_stats.py
  python3 scripts/sand_token_stats.py --h2-dir /Users/dcosson/h2home --days 7
"""

from __future__ import annotations

import argparse
import json
import os
from collections import defaultdict
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Dict, Iterable, Tuple


@dataclass
class Totals:
    input_tokens: int = 0
    output_tokens: int = 0
    turns: int = 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Per-day input/output token totals per model for *-sand agents."
    )
    parser.add_argument(
        "--h2-dir",
        default=os.environ.get("H2_DIR", str(Path.home() / "h2home")),
        help="h2 home directory (default: $H2_DIR or ~/h2home)",
    )
    parser.add_argument(
        "--days",
        type=int,
        default=7,
        help="Number of trailing days to include (default: 7)",
    )
    return parser.parse_args()


def load_command_fallback(session_dir: Path) -> str:
    meta_path = session_dir / "session.metadata.json"
    if not meta_path.exists():
        return "unknown"
    try:
        with meta_path.open("r", encoding="utf-8") as fh:
            meta = json.load(fh)
    except Exception:
        return "unknown"
    command = str(meta.get("command", "")).strip()
    return command if command else "unknown"


def iter_events(path: Path) -> Iterable[dict]:
    with path.open("r", encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                yield json.loads(line)
            except json.JSONDecodeError:
                continue


def parse_ts(raw: str) -> datetime | None:
    if not raw:
        return None
    try:
        return datetime.fromisoformat(raw.replace("Z", "+00:00"))
    except ValueError:
        return None


def main() -> int:
    args = parse_args()
    h2_dir = Path(args.h2_dir).expanduser().resolve()
    sessions_dir = h2_dir / "sessions"
    if not sessions_dir.exists():
        raise SystemExit(f"sessions dir not found: {sessions_dir}")

    cutoff = datetime.now(timezone.utc) - timedelta(days=args.days)
    totals: Dict[Tuple[str, str], Totals] = defaultdict(Totals)

    for session_dir in sorted(sessions_dir.glob("*-sand")):
        events_path = session_dir / "events.jsonl"
        if not events_path.exists():
            continue

        current_model = load_command_fallback(session_dir)
        for event in iter_events(events_path):
            event_type = event.get("type")
            data = event.get("data", {})
            ts = parse_ts(str(event.get("timestamp", "")))
            if ts is None:
                continue
            if ts.astimezone(timezone.utc) < cutoff:
                continue

            if event_type == "session_started":
                model = str(data.get("Model", "")).strip()
                if model:
                    current_model = model
                continue

            if event_type != "turn_completed":
                continue

            in_tok = int(data.get("InputTokens", 0) or 0)
            out_tok = int(data.get("OutputTokens", 0) or 0)
            day = ts.date().isoformat()
            key = (day, current_model if current_model else "unknown")
            bucket = totals[key]
            bucket.input_tokens += in_tok
            bucket.output_tokens += out_tok
            bucket.turns += 1

    print("date,model,input_tokens,output_tokens,turns")
    for day, model in sorted(totals.keys()):
        bucket = totals[(day, model)]
        print(
            f"{day},{model},{bucket.input_tokens},{bucket.output_tokens},{bucket.turns}"
        )

    return 0


if __name__ == "__main__":
    raise SystemExit(main())

