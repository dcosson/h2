#!/usr/bin/env python3
"""
Aggregate disposition table statistics across plan documents.

Usage:
    python aggregate-dispositions.py <directory> [--format markdown|json]

Recursively finds all .md files containing disposition tables in the given
directory, parses all tables, and outputs aggregate statistics including
per-round totals, convergence table, and per-file breakdown.

Shares parsing logic with validate-dispositions.py from the plan-incorporate skill.
"""

import sys
import re
import json
import argparse
from pathlib import Path
from collections import defaultdict

# --- Shared parsing logic (duplicated from validate-dispositions.py for standalone use) ---

DISPOSITION_HEADER_RE = re.compile(
    r"^##\s+"
    r"(?:\d+[a-z]?[.)]\s*)?"
    r"(?:"
    r"(?:Round\s+(\d+)\s+)?Review\s+Disposition"
    r"|"
    r"R(\d+)\s+Review\s+Disposition"
    r")"
    r"\s*$",
    re.IGNORECASE,
)

RECOGNIZED_SEVERITIES = {
    "p0", "p1", "p2", "p3",
    "critical", "blocker", "blocking",
    "high", "medium", "low",
    "info", "informational",
    "non-blocking", "nonblocking",
    "note", "question", "gap",
    "n/a", "none", "\u2014",
}

RECOGNIZED_DISPOSITIONS = {
    "incorporated", "incorporate",
    "not incorporated",
    "deferred",
    "already present",
    "acknowledged",
    "n/a",
}

DISPOSITION_PREFIXES = [
    "incorporated", "incorporate",
    "not incorporated",
]

# Severity normalization map
SEVERITY_MAP = {
    "p0": "P0", "critical": "P0", "blocker": "P0", "blocking": "P0",
    "p1": "P1", "high": "P1",
    "p2": "P2", "medium": "P2",
    "p3": "P3", "low": "P3",
    "info": "Info", "informational": "Info",
    "non-blocking": "Info", "nonblocking": "Info",
    "note": "Info", "question": "Info", "gap": "Info",
    "n/a": "N/A", "none": "N/A", "\u2014": "N/A",
}


def parse_table_row(line):
    line = line.strip()
    if not line.startswith("|"):
        return None
    parts = line.split("|")
    if parts and parts[0].strip() == "":
        parts = parts[1:]
    if parts and parts[-1].strip() == "":
        parts = parts[:-1]
    return [p.strip() for p in parts]


def is_separator_row(cells):
    return all(re.match(r"^:?-+:?$", c) for c in cells)


def normalize_col_name(name):
    return name.strip().lower()


def find_column_index(headers, candidates):
    for i, h in enumerate(headers):
        if normalize_col_name(h) in candidates:
            return i
    return None


def classify_severity(val):
    v = val.strip().lower()
    if v in RECOGNIZED_SEVERITIES:
        return True, v
    parts = re.split(r"[/,]", v)
    for p in parts:
        p = p.strip()
        if p in RECOGNIZED_SEVERITIES:
            return True, p
    return False, v


def classify_disposition(val):
    v = val.strip().lower()
    if v in RECOGNIZED_DISPOSITIONS:
        return True, v
    for prefix in DISPOSITION_PREFIXES:
        if v.startswith(prefix):
            return True, prefix
    return False, v


def extract_round_number(match):
    if match.group(1):
        return int(match.group(1))
    if match.group(2):
        return int(match.group(2))
    return None


def normalize_severity(raw):
    """Normalize a raw severity value to a canonical form (P0/P1/P2/P3/Info/N/A)."""
    v = raw.strip().lower()
    # Handle compound like "P0/Blocker"
    parts = re.split(r"[/,]", v)
    for p in parts:
        p = p.strip()
        if p in SEVERITY_MAP:
            return SEVERITY_MAP[p]
    if v in SEVERITY_MAP:
        return SEVERITY_MAP[v]
    return raw.strip()


def normalize_disposition(raw):
    """Normalize a raw disposition value to Incorporated/Not Incorporated/Deferred/Other."""
    v = raw.strip().lower()
    if v in ("incorporated", "incorporate") or v.startswith("incorporated"):
        return "Incorporated"
    if v == "not incorporated":
        return "Not Incorporated"
    if v == "deferred":
        return "Deferred"
    if v in ("already present", "acknowledged"):
        return "Incorporated"  # These are effectively incorporated
    if v == "n/a":
        return "N/A"
    return raw.strip()


def find_disposition_tables(lines):
    tables = []
    i = 0
    while i < len(lines):
        m = DISPOSITION_HEADER_RE.match(lines[i])
        if m:
            round_num = extract_round_number(m)
            section_start = i
            i += 1
            while i < len(lines) and lines[i].strip() == "":
                i += 1
            if i < len(lines):
                header_cells = parse_table_row(lines[i])
                if header_cells:
                    header_line = i
                    i += 1
                    if i < len(lines):
                        sep_cells = parse_table_row(lines[i])
                        if sep_cells and is_separator_row(sep_cells):
                            sep_line = i
                            i += 1
                            rows = []
                            while i < len(lines):
                                row_cells = parse_table_row(lines[i])
                                if row_cells is None:
                                    break
                                if is_separator_row(row_cells):
                                    i += 1
                                    continue
                                rows.append({"line": i, "cells": row_cells})
                                i += 1
                            tables.append({
                                "round": round_num,
                                "header_line": header_line,
                                "section_header_line": section_start,
                                "header_cells": header_cells,
                                "separator_line": sep_line,
                                "rows": rows,
                                "col_count": len(header_cells),
                            })
                            continue
            tables.append({
                "round": round_num,
                "header_line": section_start,
                "section_header_line": section_start,
                "header_cells": [],
                "separator_line": None,
                "rows": [],
                "col_count": 0,
            })
        i += 1
    return tables


# --- Aggregation logic ---

def parse_file_findings(filepath):
    """
    Parse all disposition tables in a file and extract structured findings.

    Returns a list of dicts:
    {
        "file": str,
        "round": int,
        "finding_id": str,
        "reviewer": str or None,
        "severity": str (normalized),
        "summary": str,
        "disposition": str (normalized),
        "notes": str,
    }
    """
    path = Path(filepath)
    text = path.read_text(encoding="utf-8")
    lines = text.splitlines()
    tables = find_disposition_tables(lines)

    findings = []
    # Track rounds seen to assign round number to unlabeled tables
    unlabeled_round = 1

    for table in tables:
        if not table["header_cells"]:
            continue  # Empty table (no findings this round)

        round_num = table["round"]
        if round_num is None:
            round_num = unlabeled_round
        unlabeled_round = max(unlabeled_round, round_num + 1)

        headers_lower = [normalize_col_name(h) for h in table["header_cells"]]

        # Find column indices
        sev_idx = find_column_index(table["header_cells"], {"severity"})
        disp_idx = find_column_index(table["header_cells"], {"disposition"})
        summary_idx = find_column_index(table["header_cells"], {"summary", "description"})
        reviewer_idx = find_column_index(table["header_cells"], {"reviewer"})
        notes_idx = find_column_index(table["header_cells"], {"notes", "note", "comments", "comment"})
        finding_idx = find_column_index(table["header_cells"], {"finding id", "finding", "#", "id"})

        if sev_idx is None or disp_idx is None:
            continue  # Can't parse without these columns

        for row in table["rows"]:
            cells = row["cells"]
            if len(cells) != table["col_count"]:
                continue  # Skip malformed rows

            raw_sev = cells[sev_idx]
            raw_disp = cells[disp_idx]

            finding = {
                "file": str(filepath),
                "round": round_num,
                "finding_id": cells[finding_idx] if finding_idx is not None else "",
                "reviewer": cells[reviewer_idx] if reviewer_idx is not None else None,
                "severity": normalize_severity(raw_sev),
                "severity_raw": raw_sev.strip(),
                "summary": cells[summary_idx] if summary_idx is not None else "",
                "disposition": normalize_disposition(raw_disp),
                "disposition_raw": raw_disp.strip(),
                "notes": cells[notes_idx] if notes_idx is not None else "",
            }
            findings.append(finding)

    return findings


def aggregate_findings(findings):
    """
    Compute aggregate statistics from a list of findings.

    Returns a dict with:
    - per_round: {round_num: {total, by_severity, by_disposition, files}}
    - overall: {total, by_severity, by_disposition}
    - convergence: [{round, total, incorporated, not_incorporated, trend}]
    - per_file: {filepath: {total, by_round, by_severity, by_disposition}}
    """
    per_round = defaultdict(lambda: {
        "total": 0,
        "by_severity": defaultdict(int),
        "by_disposition": defaultdict(int),
        "files": defaultdict(int),
    })
    per_file = defaultdict(lambda: {
        "total": 0,
        "by_round": defaultdict(int),
        "by_severity": defaultdict(int),
        "by_disposition": defaultdict(int),
    })
    overall = {
        "total": 0,
        "by_severity": defaultdict(int),
        "by_disposition": defaultdict(int),
    }

    for f in findings:
        rnd = f["round"]
        sev = f["severity"]
        disp = f["disposition"]
        fpath = f["file"]

        per_round[rnd]["total"] += 1
        per_round[rnd]["by_severity"][sev] += 1
        per_round[rnd]["by_disposition"][disp] += 1
        per_round[rnd]["files"][fpath] += 1

        per_file[fpath]["total"] += 1
        per_file[fpath]["by_round"][rnd] += 1
        per_file[fpath]["by_severity"][sev] += 1
        per_file[fpath]["by_disposition"][disp] += 1

        overall["total"] += 1
        overall["by_severity"][sev] += 1
        overall["by_disposition"][disp] += 1

    # Build convergence table
    convergence = []
    sorted_rounds = sorted(per_round.keys())
    prev_total = None
    for rnd in sorted_rounds:
        stats = per_round[rnd]
        total = stats["total"]
        incorporated = stats["by_disposition"].get("Incorporated", 0)
        not_incorporated = stats["by_disposition"].get("Not Incorporated", 0)
        deferred = stats["by_disposition"].get("Deferred", 0)
        na = stats["by_disposition"].get("N/A", 0)

        if prev_total is not None and prev_total > 0:
            pct_change = ((total - prev_total) / prev_total) * 100
            if pct_change <= 0:
                trend = f"\u2193{abs(pct_change):.0f}%"
            else:
                trend = f"\u2191{pct_change:.0f}%"
        else:
            trend = "\u2014"

        convergence.append({
            "round": rnd,
            "total": total,
            "incorporated": incorporated,
            "not_incorporated": not_incorporated,
            "deferred": deferred,
            "na": na,
            "trend": trend,
        })
        prev_total = total

    return {
        "per_round": {k: dict(v) for k, v in per_round.items()},
        "overall": overall,
        "convergence": convergence,
        "per_file": {k: dict(v) for k, v in per_file.items()},
    }


def format_markdown(stats, findings):
    """Format aggregated stats as human-readable markdown."""
    lines = []
    lines.append("# Disposition Table Aggregate Summary")
    lines.append("")

    overall = stats["overall"]
    lines.append(f"**Total findings across all files and rounds:** {overall['total']}")
    lines.append("")

    # Overall severity breakdown
    lines.append("## Overall Severity Breakdown")
    lines.append("")
    lines.append("| Severity | Count | Percentage |")
    lines.append("|----------|-------|------------|")
    sev_order = ["P0", "P1", "P2", "P3", "Info", "N/A"]
    for sev in sev_order:
        count = overall["by_severity"].get(sev, 0)
        if count > 0:
            pct = (count / overall["total"] * 100) if overall["total"] > 0 else 0
            lines.append(f"| {sev} | {count} | {pct:.1f}% |")
    # Any unlisted severities
    for sev, count in sorted(overall["by_severity"].items()):
        if sev not in sev_order and count > 0:
            pct = (count / overall["total"] * 100) if overall["total"] > 0 else 0
            lines.append(f"| {sev} | {count} | {pct:.1f}% |")
    lines.append("")

    # Overall disposition breakdown
    lines.append("## Overall Disposition Breakdown")
    lines.append("")
    lines.append("| Disposition | Count | Percentage |")
    lines.append("|-------------|-------|------------|")
    disp_order = ["Incorporated", "Not Incorporated", "Deferred", "N/A"]
    for disp in disp_order:
        count = overall["by_disposition"].get(disp, 0)
        if count > 0:
            pct = (count / overall["total"] * 100) if overall["total"] > 0 else 0
            lines.append(f"| {disp} | {count} | {pct:.1f}% |")
    for disp, count in sorted(overall["by_disposition"].items()):
        if disp not in disp_order and count > 0:
            pct = (count / overall["total"] * 100) if overall["total"] > 0 else 0
            lines.append(f"| {disp} | {count} | {pct:.1f}% |")
    lines.append("")

    # Incorporation rate (excluding N/A findings)
    real_findings = overall["total"] - overall["by_disposition"].get("N/A", 0)
    real_incorporated = overall["by_disposition"].get("Incorporated", 0)
    if real_findings > 0:
        rate = (real_incorporated / real_findings) * 100
        lines.append(f"**Incorporation rate (excluding N/A):** {real_incorporated}/{real_findings} ({rate:.1f}%)")
        lines.append("")

    # Convergence table
    lines.append("## Convergence Table")
    lines.append("")
    lines.append("| Round | Total Findings | Incorporated | Not Incorporated | Deferred | N/A | Trend |")
    lines.append("|-------|---------------|-------------|-----------------|----------|-----|-------|")
    for c in stats["convergence"]:
        lines.append(
            f"| R{c['round']} | {c['total']} | {c['incorporated']} | "
            f"{c['not_incorporated']} | {c['deferred']} | {c['na']} | {c['trend']} |"
        )
    lines.append("")

    # Per-round details
    lines.append("## Per-Round Details")
    lines.append("")
    for rnd in sorted(stats["per_round"].keys()):
        rdata = stats["per_round"][rnd]
        lines.append(f"### Round {rnd}")
        lines.append("")
        lines.append(f"**Total findings:** {rdata['total']}")
        lines.append("")

        # Severity
        lines.append("| Severity | Count |")
        lines.append("|----------|-------|")
        for sev in sev_order:
            count = rdata["by_severity"].get(sev, 0)
            if count > 0:
                lines.append(f"| {sev} | {count} |")
        for sev, count in sorted(rdata["by_severity"].items()):
            if sev not in sev_order and count > 0:
                lines.append(f"| {sev} | {count} |")
        lines.append("")

        # Disposition
        lines.append("| Disposition | Count |")
        lines.append("|-------------|-------|")
        for disp in disp_order:
            count = rdata["by_disposition"].get(disp, 0)
            if count > 0:
                lines.append(f"| {disp} | {count} |")
        for disp, count in sorted(rdata["by_disposition"].items()):
            if disp not in disp_order and count > 0:
                lines.append(f"| {disp} | {count} |")
        lines.append("")

        # Files in this round
        lines.append(f"**Files with findings:** {len(rdata['files'])}")
        lines.append("")

    # Per-file breakdown
    lines.append("## Per-File Breakdown")
    lines.append("")
    lines.append("| File | Total | " + " | ".join(f"R{r}" for r in sorted(stats["per_round"].keys())) + " |")
    sep = "|------|-------|" + "|".join("----" for _ in stats["per_round"]) + "|"
    lines.append(sep)
    for fpath in sorted(stats["per_file"].keys()):
        fdata = stats["per_file"][fpath]
        fname = Path(fpath).name
        row = f"| {fname} | {fdata['total']} |"
        for rnd in sorted(stats["per_round"].keys()):
            row += f" {fdata['by_round'].get(rnd, 0)} |"
        lines.append(row)
    lines.append("")

    return "\n".join(lines)


def format_json(stats, findings):
    """Format aggregated stats as JSON."""
    # Convert defaultdicts to regular dicts for JSON serialization
    def to_dict(obj):
        if isinstance(obj, defaultdict):
            return {k: to_dict(v) for k, v in obj.items()}
        if isinstance(obj, dict):
            return {k: to_dict(v) for k, v in obj.items()}
        return obj

    output = {
        "overall": to_dict(stats["overall"]),
        "convergence": stats["convergence"],
        "per_round": {},
        "per_file": {},
    }

    for rnd, rdata in sorted(stats["per_round"].items()):
        output["per_round"][f"R{rnd}"] = to_dict(rdata)

    for fpath, fdata in sorted(stats["per_file"].items()):
        output["per_file"][Path(fpath).name] = to_dict(fdata)

    return json.dumps(output, indent=2, ensure_ascii=False)


def main():
    parser = argparse.ArgumentParser(
        description="Aggregate disposition table statistics across plan documents."
    )
    parser.add_argument("directory", help="Directory to search for .md files")
    parser.add_argument(
        "--format", choices=["markdown", "json"], default="markdown",
        help="Output format (default: markdown)"
    )
    args = parser.parse_args()

    dirpath = Path(args.directory)
    if not dirpath.is_dir():
        print(f"Error: '{args.directory}' is not a directory", file=sys.stderr)
        sys.exit(1)

    # Find all .md files recursively
    md_files = sorted(dirpath.rglob("*.md"))

    all_findings = []
    files_with_tables = 0

    for md_file in md_files:
        findings = parse_file_findings(md_file)
        if findings:
            files_with_tables += 1
            all_findings.extend(findings)

    if not all_findings:
        if args.format == "json":
            print(json.dumps({"message": "No disposition tables found", "total": 0}))
        else:
            print("No disposition tables found in any .md files.")
        sys.exit(0)

    stats = aggregate_findings(all_findings)

    if args.format == "json":
        print(format_json(stats, all_findings))
    else:
        print(f"_Scanned {len(md_files)} files, found disposition tables in {files_with_tables} files._\n")
        print(format_markdown(stats, all_findings))


if __name__ == "__main__":
    main()
