#!/usr/bin/env python3
"""
Validate disposition tables in a markdown plan document.

Usage:
    python validate-dispositions.py <file-path>

Finds all disposition tables (Round 1, Round 2, etc.) in the file,
validates that each can be parsed correctly, and outputs PASS/FAIL
with details of any parse errors.

Exit code 0 = PASS, exit code 1 = FAIL.
"""

import sys
import re
import argparse
from pathlib import Path

# --- Shared parsing logic ---

# Recognized disposition section headers
# Handles variants:
#   ## Review Disposition
#   ## Round 1 Review Disposition
#   ## R1 Review Disposition
#   ## 15) Round 1 Review Disposition
#   ## 23. Round 1 Review Disposition
#   ## 26a) Round 2 Review Disposition
#   ## 16) Review Disposition
DISPOSITION_HEADER_RE = re.compile(
    r"^##\s+"
    r"(?:\d+[a-z]?[.)]\s*)?"  # Optional section number like "15) " or "23. " or "26a) "
    r"(?:"
    r"(?:Round\s+(\d+)\s+)?Review\s+Disposition"  # "Review Disposition" or "Round N Review Disposition"
    r"|"
    r"R(\d+)\s+Review\s+Disposition"  # "R1 Review Disposition"
    r")"
    r"\s*$",
    re.IGNORECASE,
)

# Known first-column header names
FIRST_COL_NAMES = {"finding id", "finding", "#", "id"}

# Known column names (normalized lowercase)
KNOWN_COLUMNS = {
    "finding id", "finding", "#", "id",
    "reviewer",
    "severity",
    "summary", "description",
    "disposition",
    "notes", "note", "comments", "comment",
}

# Required columns (at least these must be present)
REQUIRED_COLUMNS = {"severity", "disposition"}

# Recognized severity values (normalized lowercase, without leading/trailing whitespace)
RECOGNIZED_SEVERITIES = {
    "p0", "p1", "p2", "p3",
    "critical", "blocker", "blocking",
    "high", "medium", "low",
    "info", "informational",
    "non-blocking", "nonblocking",
    "note", "question", "gap",
    "n/a", "none", "\u2014",  # em-dash, used in "no new findings" rows
}

# Recognized disposition values (normalized lowercase)
RECOGNIZED_DISPOSITIONS = {
    "incorporated",
    "incorporate",
    "not incorporated",
    "deferred",
    "already present",
    "acknowledged",
    "n/a",
}

# Disposition values that start with these prefixes are also accepted
# (e.g., "Incorporated (Option B)")
DISPOSITION_PREFIXES = [
    "incorporated",
    "incorporate",
    "not incorporated",
]


def parse_table_row(line):
    """Parse a markdown table row into cells. Returns list of stripped cell values."""
    line = line.strip()
    if not line.startswith("|"):
        return None
    # Split on | and strip, ignoring leading/trailing empty splits
    parts = line.split("|")
    # Remove first and last empty strings from leading/trailing |
    if parts and parts[0].strip() == "":
        parts = parts[1:]
    if parts and parts[-1].strip() == "":
        parts = parts[:-1]
    return [p.strip() for p in parts]


def is_separator_row(cells):
    """Check if a row is a markdown table separator (e.g., |---|---|)."""
    return all(re.match(r"^:?-+:?$", c) for c in cells)


def normalize_col_name(name):
    """Normalize a column header name for matching."""
    return name.strip().lower()


def find_column_index(headers, candidates):
    """Find the index of a column matching any of the candidate names."""
    for i, h in enumerate(headers):
        if normalize_col_name(h) in candidates:
            return i
    return None


def classify_severity(val):
    """Check if a severity value is recognized. Returns (is_valid, normalized_value)."""
    v = val.strip().lower()
    if v in RECOGNIZED_SEVERITIES:
        return True, v
    # Handle compound like "P0/Blocker"
    parts = re.split(r"[/,]", v)
    for p in parts:
        p = p.strip()
        if p in RECOGNIZED_SEVERITIES:
            return True, p
    return False, v


def classify_disposition(val):
    """Check if a disposition value is recognized. Returns (is_valid, normalized_value)."""
    v = val.strip().lower()
    if v in RECOGNIZED_DISPOSITIONS:
        return True, v
    for prefix in DISPOSITION_PREFIXES:
        if v.startswith(prefix):
            return True, prefix
    return False, v


def extract_round_number(match):
    """Extract round number from a regex match on the disposition header."""
    # Group 1 is from "Round N Review Disposition"
    # Group 2 is from "RN Review Disposition"
    if match.group(1):
        return int(match.group(1))
    if match.group(2):
        return int(match.group(2))
    # No round number means it's the first/only round
    return None


def find_disposition_tables(lines):
    """
    Find all disposition tables in a list of lines.

    Returns a list of dicts:
    {
        "round": int or None,
        "header_line": int (0-indexed),
        "header_cells": [...],
        "separator_line": int,
        "rows": [{"line": int, "cells": [...]}],
        "col_count": int,
    }
    """
    tables = []
    i = 0
    while i < len(lines):
        m = DISPOSITION_HEADER_RE.match(lines[i])
        if m:
            round_num = extract_round_number(m)
            section_start = i
            i += 1
            # Skip blank lines between header and table
            while i < len(lines) and lines[i].strip() == "":
                i += 1
            # Look for the table header row
            if i < len(lines):
                header_cells = parse_table_row(lines[i])
                if header_cells:
                    header_line = i
                    i += 1
                    # Next should be separator
                    if i < len(lines):
                        sep_cells = parse_table_row(lines[i])
                        if sep_cells and is_separator_row(sep_cells):
                            sep_line = i
                            i += 1
                            # Parse data rows
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
            # If we fell through, no valid table found after this header
            # Still record it as a table with no rows (might be "No new findings")
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


def validate_table(table):
    """
    Validate a single disposition table.

    Returns a list of error strings. Empty list = valid.
    """
    errors = []
    round_label = f"Round {table['round']}" if table["round"] else "Review Disposition"
    line_prefix = f"line {table['section_header_line'] + 1}"

    # Empty table (no header cells) - might be "No new findings" text
    if not table["header_cells"]:
        # This is OK - some rounds have no findings
        return errors

    headers_lower = [normalize_col_name(h) for h in table["header_cells"]]

    # Check for required columns
    sev_idx = find_column_index(table["header_cells"], {"severity"})
    disp_idx = find_column_index(table["header_cells"], {"disposition"})

    if sev_idx is None:
        errors.append(
            f"{round_label} ({line_prefix}): Missing 'Severity' column. "
            f"Found columns: {table['header_cells']}"
        )
    if disp_idx is None:
        errors.append(
            f"{round_label} ({line_prefix}): Missing 'Disposition' column. "
            f"Found columns: {table['header_cells']}"
        )

    if errors:
        # Can't validate rows without knowing column positions
        return errors

    expected_cols = table["col_count"]

    for row in table["rows"]:
        row_line = row["line"] + 1  # 1-indexed for display
        cells = row["cells"]

        # Check column count
        if len(cells) != expected_cols:
            errors.append(
                f"{round_label} (line {row_line}): Expected {expected_cols} columns, "
                f"got {len(cells)}. Row: {'|'.join(cells)}"
            )
            continue

        # Validate severity
        sev_val = cells[sev_idx]
        sev_valid, sev_norm = classify_severity(sev_val)
        if not sev_valid:
            errors.append(
                f"{round_label} (line {row_line}): Unrecognized severity '{sev_val}'. "
                f"Expected one of: P0, P1, P2, P3, Critical, Blocker, High, Medium, Low, Info, etc."
            )

        # Validate disposition
        disp_val = cells[disp_idx]
        disp_valid, disp_norm = classify_disposition(disp_val)
        if not disp_valid:
            errors.append(
                f"{round_label} (line {row_line}): Unrecognized disposition '{disp_val}'. "
                f"Expected: 'Incorporated', 'Not Incorporated', 'Deferred', 'Already Present', "
                f"or 'Incorporated (...)'"
            )

    return errors


def validate_file(filepath):
    """
    Validate all disposition tables in a file.

    Returns (passed: bool, tables_found: int, errors: list[str]).
    """
    path = Path(filepath)
    if not path.exists():
        return False, 0, [f"File not found: {filepath}"]
    if not path.is_file():
        return False, 0, [f"Not a file: {filepath}"]

    text = path.read_text(encoding="utf-8")
    lines = text.splitlines()

    tables = find_disposition_tables(lines)
    if not tables:
        return True, 0, []  # No tables is OK (file may not have been reviewed yet)

    all_errors = []
    for table in tables:
        errs = validate_table(table)
        all_errors.extend(errs)

    passed = len(all_errors) == 0
    return passed, len(tables), all_errors


def main():
    parser = argparse.ArgumentParser(
        description="Validate disposition tables in a markdown plan document."
    )
    parser.add_argument("file", help="Path to the markdown file to validate")
    args = parser.parse_args()

    passed, table_count, errors = validate_file(args.file)

    if table_count == 0:
        print(f"PASS: No disposition tables found in {args.file}")
        sys.exit(0)

    if passed:
        print(f"PASS: {table_count} disposition table(s) validated successfully in {args.file}")
        sys.exit(0)
    else:
        print(f"FAIL: {len(errors)} error(s) in {table_count} disposition table(s) in {args.file}")
        print()
        for err in errors:
            print(f"  ERROR: {err}")
        print()
        print("Fix the above errors and re-run validation.")
        sys.exit(1)


if __name__ == "__main__":
    main()
