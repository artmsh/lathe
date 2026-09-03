#!/usr/bin/env python3
"""Score a stored Lathe tutorial against the /lathe contract.

The point of the `lathe` llm template is that `llm -t lathe "<topic>"` produces
what `/lathe <topic>` produces in a coding agent. "Produces the same prose" is
not checkable; "satisfies the same contract" is -- so this checks the parts of
SKILL.md that are mechanically verifiable, and is run against both a template
tutorial and /lathe-generated ones to compare.

    ./conformance.py <slug> [<slug> ...]      # from ~/.lathe/tutorials
    ./conformance.py --dir /path/to/tutorial
"""

import argparse
import json
import pathlib
import re
import sys

BANNED_OPENINGS = (
    "in this tutorial, we will",
    "this post explains",
    "have you ever wondered",
    "welcome to",
    "let's dive in",
)


def _checks(d: pathlib.Path):
    """Yield (name, ok, detail) for one stored tutorial directory."""
    meta_path = d / "metadata.json"
    meta = json.loads(meta_path.read_text()) if meta_path.exists() else {}
    parts = sorted(p.name for p in d.glob("part-*.md"))
    yield "metadata.json exists", meta_path.exists(), str(meta_path)
    yield "has part-01.md", "part-01.md" in parts, ", ".join(parts) or "none"
    yield "no index.md", not (d / "index.md").exists(), ""

    body = (d / "part-01.md").read_text() if (d / "part-01.md").exists() else ""
    heads = re.findall(r"^##+ +(.*)$", body, re.M)
    joined = " | ".join(heads)

    def has(pattern, flags=re.M):
        return bool(re.search(pattern, body, flags))

    yield "H1 title", has(r"^# \S"), (body.splitlines() or [""])[0][:70]

    first = ""
    for line in body.splitlines():
        s = line.strip()
        if s and not s.startswith("#"):
            first = s
            break
    yield (
        "opening not banned",
        not any(first.lower().startswith(b) for b in BANNED_OPENINGS),
        first[:70],
    )

    yield "## What you'll build", has(r"^##+ +What you'll build"), ""
    yield "## Prerequisites", has(r"^##+ +Prerequisites"), ""
    yield (
        "domain-specific section titles",
        not any(re.match(r"(?i)step \d", h) for h in heads),
        joined[:90],
    )
    yield "'By the end of this part'", "By the end of this part" in body, ""
    yield "## Checkpoint", has(r"^##+ +Checkpoint"), ""
    yield "[!PREDICT] beat", "[!PREDICT]" in body, ""
    yield "## What's next", has(r"^##+ +What's next"), ""

    boxes = re.findall(r"^- \[ \] ", body, re.M)
    yield "## Exercises, 3-5 items", has(r"^##+ +Exercises") and 3 <= len(boxes) <= 5, f"{len(boxes)} boxes"

    src_block = body.split("## Sources", 1)[1] if "## Sources" in body else ""
    src_entries = re.findall(r"^\d+\. .*\(https?://", src_block, re.M)
    unverified = body.count("[!UNVERIFIED]")
    yield (
        "## Sources numbered w/ URLs",
        bool(src_entries) or unverified > 0,
        f"{len(src_entries)} entries, {unverified} [!UNVERIFIED]",
    )
    yield "no mermaid fences", "```mermaid" not in body, ""
    yield "code fences present", body.count("```") >= 4, f"{body.count('```') // 2} blocks"

    tags = meta.get("tags") or []
    yield "tags 2-5 lowercase", 2 <= len(tags) <= 5 and all(t == t.lower() for t in tags), ",".join(tags)
    yield "voice recorded", bool(meta.get("voice")), str(meta.get("voice"))
    yield "model recorded", bool(meta.get("model")), str(meta.get("model"))
    yield (
        "tools pinned",
        bool(meta.get("tools")),
        ",".join(f"{t['name']}:{t['version']}" for t in meta.get("tools") or []),
    )
    yield "sources recorded", bool(meta.get("sources")), f"{len(meta.get('sources') or [])}"
    yield "repo pinned", bool(meta.get("repo")), str(meta.get("repo") or "(standalone)")


def report(d: pathlib.Path, hard: set) -> tuple[int, int, int]:
    """Print one tutorial's scorecard. Returns (passed, total, hard failures).

    Only hard failures move the exit status: a soft check that fails is a
    legitimate opt-out (a standalone tutorial, a run with no web access), and
    the /lathe baselines themselves score 21/22 that way.
    """
    print(f"\n\033[1m{d.name}\033[0m  ({d})")
    passed = total = failed_hard = 0
    for name, ok, detail in _checks(d):
        total += 1
        passed += bool(ok)
        failed_hard += (not ok) and name in hard
        mark = "\033[32mPASS\033[0m" if ok else ("\033[31mFAIL\033[0m" if name in hard else "\033[33mwarn\033[0m")
        print(f"  {mark}  {name}" + (f"  \033[2m{detail}\033[0m" if detail else ""))
    print(f"  -> {passed}/{total}")
    return passed, total, failed_hard


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("slugs", nargs="*")
    ap.add_argument("--dir", action="append", default=[])
    args = ap.parse_args()

    # Soft checks: legitimately optional per SKILL.md (repo may be a stated
    # opt-out; sources may be absent with no web access).
    soft = {"repo pinned", "sources recorded"}
    dirs = [pathlib.Path.home() / ".lathe/tutorials" / s for s in args.slugs]
    dirs += [pathlib.Path(p) for p in args.dir]
    if not dirs:
        ap.error("give at least one slug or --dir")

    worst = 0
    for d in dirs:
        if not d.exists():
            print(f"missing: {d}", file=sys.stderr)
            worst = 2
            continue
        hard = {n for n, _, _ in _checks(d)} - soft
        _, _, failed_hard = report(d, hard)
        if failed_hard:
            worst = max(worst, 1)
    return worst


if __name__ == "__main__":
    sys.exit(main())
