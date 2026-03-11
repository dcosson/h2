#!/usr/bin/env python3
"""
Pipeline audit: verify that all workflow steps completed for beads.

Reconstructs the full development pipeline for each bead by parsing git
trailers, plan docs, review docs, and bead metadata. Reports which steps
were completed and which are missing.

Usage:
    pipeline-audit.py --bead <bead-id>              # audit a single bead
    pipeline-audit.py --history <N>                  # scan last N commits for beads
    pipeline-audit.py --all                          # audit all beads from bd list
    pipeline-audit.py --all --format json            # JSON output
    pipeline-audit.py --all --required-only          # only show pass/fail for required steps

Pipeline steps tracked:
    [Required] Plan exists
    [Required] Plan review (at least 1 round)
    [Info]     Plan seam review
    [Required] Plan complete (all review findings dispositioned)
    [Info]     Implementation commits
    [Required] Code review (at least 1 round)
    [Info]     Code review rounds count
    [Info]     Plan signoff review
    [Required] Plan completion signoff
"""

import sys
import re
import json
import argparse
import subprocess
from pathlib import Path
from collections import defaultdict


# --- Git helpers ---

def git(*args, cwd=None):
    """Run a git command and return stdout lines."""
    result = subprocess.run(
        ["git"] + list(args),
        capture_output=True, text=True, cwd=cwd,
    )
    if result.returncode != 0:
        return []
    return result.stdout.strip().splitlines()


def git_log_beads(history_count=None, cwd=None):
    """
    Scan git log for commits with Bead: trailers.
    Returns {bead_id: [list of commit info dicts]}.
    """
    fmt = "--format=%H%x00%s%x00%B"
    args = ["log", fmt]
    if history_count:
        args.append(f"-{history_count}")
    args.append("--all")

    lines = git(*args, cwd=cwd)

    # Reassemble multi-line output (body can contain newlines)
    raw = "\n".join(lines)
    # Split on commit boundaries: each starts with a 40-char hex hash
    commit_pattern = re.compile(r"([0-9a-f]{40})\x00([^\x00]*)\x00(.*?)(?=(?:[0-9a-f]{40}\x00)|\Z)", re.DOTALL)

    beads = defaultdict(list)
    for m in commit_pattern.finditer(raw):
        sha = m.group(1)
        subject = m.group(2).strip()
        body = m.group(3).strip()

        # Extract trailers
        trailers = extract_trailers(body)

        bead_ids = trailers.get("bead", [])

        # Also check subject line for parenthetical bead IDs like "(edb-d6kq.3)"
        subject_bead_match = re.findall(r"\(([a-z]+-[a-z0-9]+(?:\.\d+)?)\)", subject)
        for bid in subject_bead_match:
            if bid not in bead_ids:
                bead_ids.append(bid)

        for bead_id in bead_ids:
            beads[bead_id].append({
                "sha": sha,
                "subject": subject,
                "trailers": trailers,
            })

    return beads


def extract_trailers(body):
    """
    Extract git trailers from a commit body.
    Trailers appear after the last blank line in the body, so we skip
    the message text (including conventional commit subjects like
    "fix: something" that would false-match the Key: Value pattern).
    Returns {trailer_key_lower: [values]}.
    """
    trailers = defaultdict(list)
    lines = body.splitlines()

    # Find the last blank line — trailers come after it
    last_blank = -1
    for i, line in enumerate(lines):
        if line.strip() == "":
            last_blank = i

    # Only parse lines after the last blank line (the trailer block)
    trailer_lines = lines[last_blank + 1:] if last_blank >= 0 else lines
    for line in trailer_lines:
        line = line.strip()
        # Trailer format: Key: Value (or Key-Name: Value)
        m = re.match(r"^([A-Za-z][\w-]*)\s*:\s*(.+)$", line)
        if m:
            key = m.group(1).lower().replace("-", "_")
            val = m.group(2).strip()
            trailers[key].append(val)
    return dict(trailers)


# --- Bead helpers ---

def bd_list(cwd=None):
    """Get all beads via bd list --format json."""
    try:
        result = subprocess.run(
            ["bd", "list", "--format", "json"],
            capture_output=True, text=True, cwd=cwd,
        )
        if result.returncode != 0:
            return []
        return json.loads(result.stdout)
    except (json.JSONDecodeError, FileNotFoundError):
        return []


def bd_show(bead_id, cwd=None):
    """Get a single bead's info."""
    try:
        result = subprocess.run(
            ["bd", "show", bead_id, "--format", "json"],
            capture_output=True, text=True, cwd=cwd,
        )
        if result.returncode != 0:
            return None
        return json.loads(result.stdout)
    except (json.JSONDecodeError, FileNotFoundError):
        return None


# --- Plan doc helpers ---

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

SIGNOFF_HEADER_RE = re.compile(
    r"^##\s+Completion\s+Signoff\s*$",
    re.IGNORECASE,
)

STATUS_RE = re.compile(
    r"^\s*[-*]\s*(?:\*\*)?Status(?:\*\*)?:\s*(.+)$",
    re.IGNORECASE,
)


def find_plan_doc(bead_id, plan_refs, plans_dir):
    """
    Find the plan doc for a bead.

    Strategy:
    1. Check Plan-Ref trailers from commits
    2. Search docs/plans/ for files referencing the bead ID
    3. Check the bead's parent epic for plan references

    Returns the plan doc path (relative to repo root) or None.
    """
    # 1. Direct Plan-Ref from commits
    for ref in plan_refs:
        path = Path(ref)
        if path.exists():
            return str(path)

    # 2. Search plan docs for bead ID references
    if plans_dir.is_dir():
        for md_file in sorted(plans_dir.glob("*.md")):
            # Skip review files and index
            if "-review-" in md_file.name:
                continue
            try:
                text = md_file.read_text(encoding="utf-8")
                if bead_id in text:
                    return str(md_file)
            except Exception:
                continue

    return None


def count_plan_review_rounds(plan_path):
    """Count the number of review disposition rounds in a plan doc."""
    try:
        text = Path(plan_path).read_text(encoding="utf-8")
    except Exception:
        return 0

    rounds = set()
    for line in text.splitlines():
        m = DISPOSITION_HEADER_RE.match(line.strip())
        if m:
            round_num = None
            if m.group(1):
                round_num = int(m.group(1))
            elif m.group(2):
                round_num = int(m.group(2))
            else:
                round_num = 1  # Unlabeled = round 1
            rounds.add(round_num)

    return len(rounds)


def check_plan_signoff(plan_path):
    """
    Check if a plan doc has a completion signoff section.
    Returns (has_signoff, status_string).
    """
    try:
        text = Path(plan_path).read_text(encoding="utf-8")
    except Exception:
        return False, None

    lines = text.splitlines()
    in_signoff = False
    for line in lines:
        if SIGNOFF_HEADER_RE.match(line.strip()):
            in_signoff = True
            continue
        if in_signoff:
            if line.strip().startswith("## "):
                break
            m = STATUS_RE.match(line.strip())
            if m:
                return True, m.group(1).strip()

    return in_signoff, None


def find_seam_reviews(plan_path, plans_dir):
    """Check if any seam review docs reference this plan."""
    if not plans_dir.is_dir():
        return []

    plan_name = Path(plan_path).stem
    seam_docs = []
    for md_file in sorted(plans_dir.glob("*seam*review*.md")):
        try:
            text = md_file.read_text(encoding="utf-8")
            if plan_name in text:
                seam_docs.append(str(md_file))
        except Exception:
            continue
    return seam_docs


# --- Code review helpers ---

def find_code_reviews(bead_id, reviews_dir):
    """
    Find code review docs for a bead in docs/reviews/.
    Returns list of (path, round_number) tuples.
    """
    if not reviews_dir.is_dir():
        return []

    reviews = []
    # Match patterns like: edb-d6kq.3-r1-review-reviewer-1.md
    pattern = f"{bead_id}-r*"
    for md_file in sorted(reviews_dir.glob(f"{pattern}*.md")):
        # Extract round number
        m = re.search(r"-r(\d+)", md_file.name)
        round_num = int(m.group(1)) if m else 0
        reviews.append((str(md_file), round_num))

    return reviews


def find_review_incorporation_commits(bead_id, bead_commits):
    """
    Find commits that incorporate review feedback for this bead.
    Returns list of commit info dicts.
    """
    incorporation = []
    for commit in bead_commits:
        trailers = commit["trailers"]
        if "review_ref" in trailers:
            for ref in trailers["review_ref"]:
                if bead_id in ref:
                    incorporation.append(commit)
                    break
    return incorporation


# --- Audit logic ---

def audit_bead(bead_id, bead_commits, plans_dir, reviews_dir, cwd=None):
    """
    Audit a single bead's pipeline.

    Returns a dict with all pipeline step statuses.
    """
    result = {
        "bead_id": bead_id,
        "steps": {},
        "required_pass": True,
        "summary": "",
    }

    # Collect Plan-Ref trailers from all commits for this bead
    plan_refs = []
    for commit in bead_commits:
        plan_refs.extend(commit["trailers"].get("plan_ref", []))

    # --- Step 1: Plan exists ---
    plan_path = find_plan_doc(bead_id, plan_refs, plans_dir)
    result["steps"]["plan_exists"] = {
        "required": True,
        "status": "pass" if plan_path else "fail",
        "detail": plan_path or "No plan doc found",
    }

    # --- Step 2: Plan review rounds ---
    review_rounds = 0
    if plan_path:
        review_rounds = count_plan_review_rounds(plan_path)
    result["steps"]["plan_review"] = {
        "required": True,
        "status": "pass" if review_rounds >= 1 else "fail",
        "detail": f"{review_rounds} round(s)" if review_rounds > 0 else "No review disposition tables found",
        "count": review_rounds,
    }

    # --- Step 3: Seam review ---
    seam_reviews = []
    if plan_path:
        seam_reviews = find_seam_reviews(plan_path, plans_dir)
    result["steps"]["seam_review"] = {
        "required": False,
        "status": "pass" if seam_reviews else "skip",
        "detail": f"{len(seam_reviews)} seam review(s)" if seam_reviews else "None found",
        "docs": seam_reviews,
    }

    # --- Step 4: Plan complete ---
    # A plan is "complete" if it has disposition tables and the signoff exists
    plan_complete = review_rounds >= 1  # At minimum, review was done
    result["steps"]["plan_complete"] = {
        "required": True,
        "status": "pass" if plan_complete else "fail",
        "detail": "Reviews incorporated" if plan_complete else "No review rounds completed",
    }

    # --- Step 5: Implementation commits ---
    impl_commits = [c for c in bead_commits if "review_ref" not in c["trailers"]]
    result["steps"]["implementation"] = {
        "required": False,
        "status": "pass" if impl_commits else "skip",
        "detail": f"{len(impl_commits)} commit(s)",
        "count": len(impl_commits),
        "commits": [c["sha"][:8] for c in impl_commits],
    }

    # --- Step 6: Code review ---
    code_reviews = find_code_reviews(bead_id, reviews_dir)
    result["steps"]["code_review"] = {
        "required": True,
        "status": "pass" if len(code_reviews) >= 1 else "fail",
        "detail": f"{len(code_reviews)} review doc(s)" if code_reviews else "No code review docs found",
        "count": len(code_reviews),
        "docs": [r[0] for r in code_reviews],
    }

    # --- Step 7: Code review rounds ---
    max_round = max((r[1] for r in code_reviews), default=0)
    result["steps"]["code_review_rounds"] = {
        "required": False,
        "status": "info",
        "detail": f"{max_round} round(s)" if max_round > 0 else "N/A",
        "count": max_round,
    }

    # --- Step 8: Review incorporation commits ---
    incorp_commits = find_review_incorporation_commits(bead_id, bead_commits)
    result["steps"]["review_incorporation"] = {
        "required": False,
        "status": "pass" if incorp_commits else "skip",
        "detail": f"{len(incorp_commits)} incorporation commit(s)",
        "count": len(incorp_commits),
        "commits": [c["sha"][:8] for c in incorp_commits],
    }

    # --- Step 9: Plan signoff review ---
    # Check if there are signoff-related review docs
    signoff_review_found = False
    if plan_path:
        plan_stem = Path(plan_path).stem
        for md_file in plans_dir.glob(f"{plan_stem}*signoff*review*.md"):
            signoff_review_found = True
            break
    result["steps"]["signoff_review"] = {
        "required": False,
        "status": "pass" if signoff_review_found else "skip",
        "detail": "Found" if signoff_review_found else "None found",
    }

    # --- Step 10: Plan completion signoff ---
    has_signoff, signoff_status = False, None
    if plan_path:
        has_signoff, signoff_status = check_plan_signoff(plan_path)
    signoff_complete = has_signoff and signoff_status and "complete" in signoff_status.lower()
    result["steps"]["plan_signoff"] = {
        "required": True,
        "status": "pass" if signoff_complete else "fail",
        "detail": signoff_status or ("Signoff section exists but no status" if has_signoff else "No signoff section"),
    }

    # --- Overall required pass/fail ---
    required_steps = [s for s in result["steps"].values() if s["required"]]
    failed_required = [s for s in required_steps if s["status"] == "fail"]
    result["required_pass"] = len(failed_required) == 0
    result["required_total"] = len(required_steps)
    result["required_passed"] = len(required_steps) - len(failed_required)

    # Summary line
    total_steps = len(result["steps"])
    passed = sum(1 for s in result["steps"].values() if s["status"] == "pass")
    result["summary"] = (
        f"{result['required_passed']}/{result['required_total']} required, "
        f"{passed}/{total_steps} total"
    )

    return result


# --- Output formatting ---

STEP_LABELS = {
    "plan_exists": "Plan exists",
    "plan_review": "Plan review",
    "seam_review": "Seam review",
    "plan_complete": "Plan complete",
    "implementation": "Implementation commits",
    "code_review": "Code review",
    "code_review_rounds": "Code review rounds",
    "review_incorporation": "Review incorporation",
    "signoff_review": "Signoff review",
    "plan_signoff": "Plan signoff",
}

STATUS_ICONS = {
    "pass": "\u2713",
    "fail": "\u2717",
    "skip": "\u2014",
    "info": "\u2139",
}


def format_text_single(audit):
    """Format a single bead audit as text."""
    lines = []
    icon = "\u2713" if audit["required_pass"] else "\u2717"
    lines.append(f"{icon} {audit['bead_id']}  ({audit['summary']})")

    for step_key, step in audit["steps"].items():
        label = STEP_LABELS.get(step_key, step_key)
        icon = STATUS_ICONS.get(step["status"], "?")
        req = "*" if step["required"] else " "
        lines.append(f"  {req} [{icon}] {label}: {step['detail']}")

    return "\n".join(lines)


def format_text(audits, required_only=False):
    """Format all audits as text."""
    lines = []
    lines.append("Pipeline Audit Report")
    lines.append("=" * 50)
    lines.append("")

    # Summary
    total = len(audits)
    passed = sum(1 for a in audits if a["required_pass"])
    failed = total - passed
    lines.append(f"Beads audited: {total}")
    lines.append(f"All required steps pass: {passed}")
    if failed > 0:
        lines.append(f"Missing required steps:  {failed}")
    lines.append("")

    # Failed beads first
    failed_audits = [a for a in audits if not a["required_pass"]]
    passed_audits = [a for a in audits if a["required_pass"]]

    if failed_audits:
        lines.append("--- INCOMPLETE ---")
        lines.append("")
        for audit in failed_audits:
            if required_only:
                icon = "\u2717"
                lines.append(f"{icon} {audit['bead_id']}  ({audit['summary']})")
                for step_key, step in audit["steps"].items():
                    if step["required"] and step["status"] == "fail":
                        label = STEP_LABELS.get(step_key, step_key)
                        lines.append(f"    \u2717 {label}: {step['detail']}")
            else:
                lines.append(format_text_single(audit))
            lines.append("")

    if passed_audits:
        lines.append("--- COMPLETE ---")
        lines.append("")
        for audit in passed_audits:
            if required_only:
                lines.append(f"\u2713 {audit['bead_id']}  ({audit['summary']})")
            else:
                lines.append(format_text_single(audit))
            lines.append("")

    # Legend
    lines.append("Legend: * = required step, \u2713 = pass, \u2717 = fail, \u2014 = skipped, \u2139 = info")

    return "\n".join(lines)


def format_markdown(audits, required_only=False):
    """Format all audits as markdown."""
    lines = []
    lines.append("# Pipeline Audit Report")
    lines.append("")

    total = len(audits)
    passed = sum(1 for a in audits if a["required_pass"])
    lines.append(f"**Beads audited:** {total} | **Pass:** {passed} | **Fail:** {total - passed}")
    lines.append("")

    # Summary table
    lines.append("| Bead | Status | Required | Total | Plan | Plan Review | Code Review | Signoff |")
    lines.append("|------|--------|----------|-------|------|-------------|-------------|---------|")

    for audit in audits:
        icon = "\u2713" if audit["required_pass"] else "\u2717"
        steps = audit["steps"]

        def step_icon(key):
            s = steps.get(key, {})
            return STATUS_ICONS.get(s.get("status", "skip"), "?")

        lines.append(
            f"| `{audit['bead_id']}` | {icon} | "
            f"{audit['required_passed']}/{audit['required_total']} | "
            f"{audit['summary'].split(',')[1].strip()} | "
            f"{step_icon('plan_exists')} | "
            f"{step_icon('plan_review')} ({steps.get('plan_review', {}).get('count', 0)}r) | "
            f"{step_icon('code_review')} ({steps.get('code_review', {}).get('count', 0)}r) | "
            f"{step_icon('plan_signoff')} |"
        )

    lines.append("")

    # Detailed section for failures
    failed_audits = [a for a in audits if not a["required_pass"]]
    if failed_audits:
        lines.append("## Incomplete Beads")
        lines.append("")
        for audit in failed_audits:
            lines.append(f"### `{audit['bead_id']}`")
            lines.append("")
            for step_key, step in audit["steps"].items():
                if required_only and not step["required"]:
                    continue
                label = STEP_LABELS.get(step_key, step_key)
                icon = STATUS_ICONS.get(step["status"], "?")
                req = " **(required)**" if step["required"] else ""
                lines.append(f"- [{icon}] {label}: {step['detail']}{req}")
            lines.append("")

    return "\n".join(lines)


# --- Main ---

def main():
    parser = argparse.ArgumentParser(
        description="Audit the development pipeline for beads.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )

    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--bead", help="Audit a single bead by ID")
    group.add_argument("--history", type=int, metavar="N",
                       help="Scan the last N commits for beads to audit")
    group.add_argument("--all", action="store_true",
                       help="Audit all beads found in bd list and git history")

    parser.add_argument("--format", choices=["text", "markdown", "json"],
                        default="text", help="Output format (default: text)")
    parser.add_argument("--required-only", action="store_true",
                        help="Only show required steps in output")
    parser.add_argument("--plans-dir", default="docs/plans",
                        help="Path to plans directory (default: docs/plans)")
    parser.add_argument("--reviews-dir", default="docs/reviews",
                        help="Path to reviews directory (default: docs/reviews)")

    args = parser.parse_args()

    plans_dir = Path(args.plans_dir)
    reviews_dir = Path(args.reviews_dir)

    # Collect bead IDs to audit
    bead_ids = set()

    if args.bead:
        bead_ids.add(args.bead)

    # Scan git history for beads
    history_count = args.history if args.history else (None if args.all else None)
    git_beads = git_log_beads(history_count=history_count)

    if args.all:
        # Also get beads from bd list
        bd_beads = bd_list()
        for bead in bd_beads:
            bead_id = bead.get("id") or bead.get("bead_id")
            if bead_id:
                bead_ids.add(bead_id)

    # Add all beads found in git
    if not args.bead:
        bead_ids.update(git_beads.keys())
    elif args.bead and args.bead not in git_beads:
        # Single bead mode but no commits found — still audit it
        git_beads[args.bead] = []

    if not bead_ids:
        print("No beads found to audit.", file=sys.stderr)
        sys.exit(1)

    # Audit each bead
    audits = []
    for bead_id in sorted(bead_ids):
        commits = git_beads.get(bead_id, [])
        audit = audit_bead(bead_id, commits, plans_dir, reviews_dir)
        audits.append(audit)

    # Output
    if args.format == "json":
        # Clean up for JSON serialization
        print(json.dumps(audits, indent=2, ensure_ascii=False))
    elif args.format == "markdown":
        print(format_markdown(audits, required_only=args.required_only))
    else:
        print(format_text(audits, required_only=args.required_only))

    # Exit code: 0 if all required steps pass, 1 if any fail
    all_pass = all(a["required_pass"] for a in audits)
    sys.exit(0 if all_pass else 1)


if __name__ == "__main__":
    main()
