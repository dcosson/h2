#!/usr/bin/env python3
"""
Show completion signoff status for plan docs in a directory.

Traverses a plans directory, finds all plan docs (excluding index,
architecture, shaping, summary, and review files), and checks each
for a "## Completion Signoff" section. Reports counts and details.

Usage:
    python signoff-status.py <plans-dir>
    python signoff-status.py docs/plans/
    python signoff-status.py docs/plans/ --format json
    python signoff-status.py docs/plans/ --format markdown
"""

import sys
import re
import json
import argparse
from pathlib import Path
from datetime import datetime


# Files to exclude from signoff tracking
EXCLUDE_PREFIXES = ("00-", "99-")
EXCLUDE_CONTAINS = ("-review-", "-shaping", "SKILL")

# Signoff section header pattern
SIGNOFF_HEADER_RE = re.compile(
    r"^##\s+Completion\s+Signoff\s*$",
    re.IGNORECASE,
)

# Field patterns within the signoff section
# Accept both bold markdown (**Status**:) and plain text (Status:) formats
STATUS_RE = re.compile(r"^\s*[-*]\s*(?:\*\*)?Status(?:\*\*)?:\s*(.+)$", re.IGNORECASE)
DATE_RE = re.compile(r"^\s*[-*]\s*(?:\*\*)?Date(?:\*\*)?:\s*(.+)$", re.IGNORECASE)
BRANCH_RE = re.compile(r"^\s*[-*]\s*(?:\*\*)?Branch(?:\*\*)?:\s*(.+)$", re.IGNORECASE)
COMMIT_RE = re.compile(r"^\s*[-*]\s*(?:\*\*)?Commit(?:\*\*)?:\s*(.+)$", re.IGNORECASE)
# Accept "Verified by" or "Signed off by"
VERIFIED_BY_RE = re.compile(r"^\s*[-*]\s*(?:\*\*)?(?:Verified|Signed\s+off)\s+by(?:\*\*)?:\s*(.+)$", re.IGNORECASE)


def should_include(path: Path) -> bool:
    """Check if a plan doc should be included in signoff tracking."""
    name = path.name
    if not name.endswith(".md"):
        return False
    for prefix in EXCLUDE_PREFIXES:
        if name.startswith(prefix):
            return False
    for substring in EXCLUDE_CONTAINS:
        if substring in name:
            return False
    return True


def parse_signoff(lines: list[str]) -> dict | None:
    """
    Find and parse a Completion Signoff section in a list of lines.

    Returns a dict with parsed fields, or None if no signoff found.
    """
    signoff_start = None
    for i, line in enumerate(lines):
        if SIGNOFF_HEADER_RE.match(line.strip()):
            signoff_start = i
            break

    if signoff_start is None:
        return None

    result = {
        "status": None,
        "date": None,
        "branch": None,
        "commit": None,
        "verified_by": None,
        "line": signoff_start + 1,  # 1-indexed
    }

    # Parse fields from lines after the header
    for line in lines[signoff_start + 1:]:
        stripped = line.strip()
        # Stop at next section header
        if stripped.startswith("## "):
            break

        m = STATUS_RE.match(stripped)
        if m:
            result["status"] = m.group(1).strip()
            continue

        m = DATE_RE.match(stripped)
        if m:
            result["date"] = m.group(1).strip()
            continue

        m = BRANCH_RE.match(stripped)
        if m:
            result["branch"] = m.group(1).strip()
            continue

        m = COMMIT_RE.match(stripped)
        if m:
            result["commit"] = m.group(1).strip()
            continue

        m = VERIFIED_BY_RE.match(stripped)
        if m:
            result["verified_by"] = m.group(1).strip()
            continue

    return result


def classify_doc(path: Path) -> str:
    """Classify a doc as 'plan' or 'test-harness'."""
    if "-test-harness" in path.name:
        return "test-harness"
    return "plan"


def scan_directory(plans_dir: Path) -> dict:
    """
    Scan a plans directory for all plan docs and their signoff status.

    Returns a dict with summary statistics and per-doc details.
    """
    if not plans_dir.is_dir():
        print(f"Error: {plans_dir} is not a directory", file=sys.stderr)
        sys.exit(1)

    # Find all matching docs, recursing into subdirectories
    all_docs = sorted(
        p for p in plans_dir.rglob("*.md") if should_include(p)
    )

    results = {
        "directory": str(plans_dir),
        "scan_date": datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
        "total": len(all_docs),
        "complete": 0,
        "partial": 0,
        "not_implemented": 0,
        "not_started": 0,
        "docs": [],
    }

    for doc_path in all_docs:
        text = doc_path.read_text(encoding="utf-8")
        lines = text.splitlines()
        signoff = parse_signoff(lines)

        doc_info = {
            "path": str(doc_path.relative_to(plans_dir)),
            "type": classify_doc(doc_path),
            "signoff": None,
        }

        if signoff is None:
            doc_info["signoff_status"] = "not_started"
            results["not_started"] += 1
        else:
            doc_info["signoff"] = signoff
            raw_status = (signoff.get("status") or "").strip()
            # Normalize status: strip parenthetical suffixes like "NOT IMPLEMENTED (0%)"
            status = raw_status.lower().split("(")[0].strip()
            if status == "complete":
                doc_info["signoff_status"] = "complete"
                results["complete"] += 1
            elif status == "partial":
                doc_info["signoff_status"] = "partial"
                results["partial"] += 1
            elif status in ("not implemented", "not_implemented"):
                doc_info["signoff_status"] = "not_implemented"
                results["not_implemented"] += 1
            else:
                # Has a signoff section but status is unclear
                doc_info["signoff_status"] = "unknown"
                results["not_started"] += 1

        results["docs"].append(doc_info)

    return results


def format_text(results: dict) -> str:
    """Format results as human-readable text."""
    lines = []
    total = results["total"]
    complete = results["complete"]
    partial = results["partial"]
    not_started = results["not_started"]

    lines.append(f"Plan Completion Signoff Status")
    lines.append(f"{'=' * 40}")
    lines.append(f"Directory: {results['directory']}")
    lines.append(f"Scanned:   {results['scan_date']}")
    lines.append("")

    # Summary counts
    not_implemented = results.get("not_implemented", 0)
    pct_complete = (complete / total * 100) if total > 0 else 0
    lines.append(f"Total docs:        {total}")
    lines.append(f"  Complete:        {complete} ({pct_complete:.0f}%)")
    if partial > 0:
        lines.append(f"  Partial:         {partial}")
    if not_implemented > 0:
        lines.append(f"  Not implemented: {not_implemented}")
    lines.append(f"  Not started:     {not_started}")
    lines.append("")

    # Separate plan docs and test harness docs
    plan_docs = [d for d in results["docs"] if d["type"] == "plan"]
    th_docs = [d for d in results["docs"] if d["type"] == "test-harness"]

    # Complete docs
    complete_docs = [d for d in results["docs"] if d["signoff_status"] == "complete"]
    if complete_docs:
        lines.append(f"Completed ({len(complete_docs)}):")
        for d in complete_docs:
            signoff = d["signoff"]
            verified = signoff.get("verified_by", "unknown")
            date = signoff.get("date", "unknown")
            lines.append(f"  [+] {d['path']}  ({date}, {verified})")
        lines.append("")

    # Partial docs
    partial_docs = [d for d in results["docs"] if d["signoff_status"] == "partial"]
    if partial_docs:
        lines.append(f"Partial ({len(partial_docs)}):")
        for d in partial_docs:
            signoff = d["signoff"]
            verified = signoff.get("verified_by", "unknown")
            lines.append(f"  [~] {d['path']}  (verified by {verified})")
        lines.append("")

    # Not implemented docs
    not_impl_docs = [d for d in results["docs"] if d["signoff_status"] == "not_implemented"]
    if not_impl_docs:
        lines.append(f"Not implemented ({len(not_impl_docs)}):")
        for d in not_impl_docs:
            signoff = d["signoff"]
            verified = signoff.get("verified_by", "unknown")
            lines.append(f"  [-] {d['path']}  (verified by {verified})")
        lines.append("")

    # Not started docs
    not_started_docs = [d for d in results["docs"] if d["signoff_status"] in ("not_started", "unknown")]
    if not_started_docs:
        lines.append(f"Not started ({len(not_started_docs)}):")
        for d in not_started_docs:
            lines.append(f"  [ ] {d['path']}")
        lines.append("")

    # Type breakdown
    plan_complete = sum(1 for d in plan_docs if d["signoff_status"] == "complete")
    th_complete = sum(1 for d in th_docs if d["signoff_status"] == "complete")
    lines.append(f"By type:")
    lines.append(f"  Plan docs:         {plan_complete}/{len(plan_docs)} complete")
    lines.append(f"  Test harness docs: {th_complete}/{len(th_docs)} complete")

    return "\n".join(lines)


def format_markdown(results: dict) -> str:
    """Format results as markdown."""
    lines = []
    total = results["total"]
    complete = results["complete"]
    partial = results["partial"]
    not_started = results["not_started"]
    pct_complete = (complete / total * 100) if total > 0 else 0

    lines.append("# Plan Completion Signoff Status")
    lines.append("")
    lines.append(f"Scanned: `{results['directory']}` on {results['scan_date']}")
    lines.append("")

    lines.append("## Summary")
    lines.append("")
    lines.append("| Metric | Count |")
    lines.append("|--------|-------|")
    lines.append(f"| Total docs | {total} |")
    not_implemented = results.get("not_implemented", 0)
    lines.append(f"| Complete | {complete} ({pct_complete:.0f}%) |")
    if partial > 0:
        lines.append(f"| Partial | {partial} |")
    if not_implemented > 0:
        lines.append(f"| Not implemented | {not_implemented} |")
    lines.append(f"| Not started | {not_started} |")
    lines.append("")

    lines.append("## Details")
    lines.append("")
    lines.append("| Doc | Type | Status | Date | Verified By |")
    lines.append("|-----|------|--------|------|-------------|")

    for d in results["docs"]:
        status_icon = {"complete": "+", "partial": "~", "not_implemented": "-", "not_started": " ", "unknown": "?"}.get(d["signoff_status"], "?")
        status_label = d["signoff_status"].replace("_", " ").title()
        date = ""
        verified = ""
        if d["signoff"]:
            date = d["signoff"].get("date", "") or ""
            verified = d["signoff"].get("verified_by", "") or ""
        lines.append(f"| `{d['path']}` | {d['type']} | [{status_icon}] {status_label} | {date} | {verified} |")

    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(
        description="Show completion signoff status for plan docs."
    )
    parser.add_argument("directory", help="Path to the plans directory")
    parser.add_argument(
        "--format",
        choices=["text", "json", "markdown"],
        default="text",
        help="Output format (default: text)",
    )
    args = parser.parse_args()

    results = scan_directory(Path(args.directory))

    if args.format == "json":
        print(json.dumps(results, indent=2))
    elif args.format == "markdown":
        print(format_markdown(results))
    else:
        print(format_text(results))

    sys.exit(0)


if __name__ == "__main__":
    main()
