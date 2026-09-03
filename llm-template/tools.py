"""Tools for the `lathe` llm template.

The `/lathe` skill assumes a coding agent: a shell, web search, web fetch and a
filesystem. `llm -t lathe` has none of those, so these three tools stand in for
exactly the commands the skill names -- no more. They are deliberately coarse
because `llm`'s default chain limit allows only three tool-calling turns.

Registered by `llm` via the template's `functions:` field; because the template
is loaded from disk, the code is trusted and needs no extra flag.
"""

import json
import os
import pathlib
import re
import shutil
import ssl
import subprocess
import sys
import urllib.parse

import httpx

SEARXNG_URL = os.environ.get("LATHE_SEARXNG_URL", "https://sear.xng")
SEARXNG_CA = os.environ.get(
    "LATHE_SEARXNG_CA", "/Users/art/.local/share/homelab/home-network-ca.pem"
)
FALLBACK_ENGINES = "bing,google,mojeek,qwant,wikipedia,duckduckgo,brave,startpage"
# Characters of extracted text kept per page. 20000 x 8 pages of mostly
# navigation boilerplate (the Zig release notes are one huge table of contents)
# pushed a run past the gateway's 30s time-to-first-token and it returned
# 504 DIRECT_RESPONSE_START_TIMEOUT. 12000 still carries the prose that matters.
FETCH_LIMIT = 12000
TIMEOUT = 30

# Probed with `<cmd> <flag>` when the topic mentions the key, plus anything the
# model names in tool_hints. Mirrors the skill's "probe whatever's relevant".
VERSION_PROBES = {
    "zig": ["zig", "version"],
    "go": ["go", "version"],
    "rust": ["rustc", "--version"],
    "cargo": ["cargo", "--version"],
    "node": ["node", "--version"],
    "deno": ["deno", "--version"],
    "bun": ["bun", "--version"],
    "python": ["python3", "--version"],
    "ruby": ["ruby", "--version"],
    "java": ["java", "-version"],
    "clang": ["clang", "--version"],
    "gcc": ["gcc", "--version"],
    "llvm": ["llvm-config", "--version"],
    "cmake": ["cmake", "--version"],
    "swift": ["swift", "--version"],
    "elixir": ["elixir", "--version"],
    "erlang": ["erl", "-eval", "erlang:display(erlang:system_info(otp_release)), halt().", "-noshell"],
    "clojure": ["clojure", "--version"],
    "babashka": ["bb", "--version"],
    "postgres": ["psql", "--version"],
    "sqlite": ["sqlite3", "--version"],
    "redis": ["redis-server", "--version"],
    "docker": ["docker", "--version"],
    "ffmpeg": ["ffmpeg", "-version"],
    "git": ["git", "--version"],
}


def _output(argv, cwd=None):
    """Run argv, returning its trimmed combined output, or None if it cannot run."""
    if not shutil.which(argv[0]):
        return None
    try:
        p = subprocess.run(argv, cwd=cwd, capture_output=True, text=True, timeout=25)
    except (subprocess.SubprocessError, OSError):
        return None
    return ((p.stdout or "").strip() or (p.stderr or "").strip()) or None


def _run(argv, cwd=None):
    """_output, reduced to its first line -- for one-line probes like `zig version`."""
    out = _output(argv, cwd=cwd)
    return out.splitlines()[0].strip() if out else None


def _ssl_context():
    """Public CAs *plus* the home CA, so one client can reach both the local
    SearXNG instance and ziglang.org. Adding a CA to the default bundle is not
    the same as disabling verification -- certificate checking stays on."""
    ctx = ssl.create_default_context()
    if pathlib.Path(SEARXNG_CA).exists():
        try:
            ctx.load_verify_locations(cafile=SEARXNG_CA)
        except (ssl.SSLError, OSError):
            pass
    return ctx


def _client():
    return httpx.Client(
        follow_redirects=True,
        timeout=TIMEOUT,
        verify=_ssl_context(),
        headers={"User-Agent": "Mozilla/5.0 (compatible; lathe-llm-template)"},
    )


def _html_to_text(html):
    """Crude but dependency-free HTML -> text. Good enough to read docs from."""
    html = re.sub(r"(?is)<(script|style|noscript|svg|nav|footer)\b.*?</\1>", " ", html)
    html = re.sub(r"(?is)<!--.*?-->", " ", html)
    html = re.sub(r"(?i)<(br|/p|/div|/li|/h[1-6]|/tr)\s*/?>", "\n", html)
    text = re.sub(r"(?s)<[^>]+>", " ", html)
    for entity, char in (
        ("&nbsp;", " "), ("&amp;", "&"), ("&lt;", "<"), ("&gt;", ">"),
        ("&quot;", '"'), ("&#39;", "'"), ("&mdash;", "--"),
    ):
        text = text.replace(entity, char)
    text = re.sub(r"[ \t]+", " ", text)
    text = re.sub(r"\n\s*\n\s*\n+", "\n\n", text)
    return text.strip()


def _search_once(client, query, max_results, engines=None):
    params = {"q": query, "format": "json"}
    if engines:
        params["engines"] = engines
    try:
        r = client.get(SEARXNG_URL + "/search?" + urllib.parse.urlencode(params))
        r.raise_for_status()
        data = r.json()
    except Exception as ex:  # noqa: BLE001 -- any failure is reported, never fatal
        return None, f"{type(ex).__name__}: {ex}"
    results = [
        {
            "title": item.get("title", ""),
            "url": item.get("url", ""),
            "snippet": (item.get("content") or "")[:400],
        }
        for item in (data.get("results") or [])[:max_results]
    ]
    return results, json.dumps(data.get("unresponsive_engines") or [])


def _search(client, query, max_results):
    """SearXNG, with a second attempt against a wider engine list.

    The instance's default engines rate-limit and CAPTCHA independently, and
    when they all trip at once the search returns zero results with a 200. That
    silently degrades the whole run to whatever the model recalls -- which is
    how a tutorial pinned to Zig 0.16.0 ended up citing the 0.14.0 docs. Engines
    fail one at a time, so naming a broader set on the retry usually finds one
    that is still answering.
    """
    results, detail = _search_once(client, query, max_results)
    if results:
        return {"query": query, "results": results}

    fallback, fb_detail = _search_once(client, query, max_results, engines=FALLBACK_ENGINES)
    if fallback:
        return {"query": query, "results": fallback, "engines": FALLBACK_ENGINES}

    return {
        "query": query,
        "results": [],
        "error": (
            f"search returned no results (unresponsive: {detail}; "
            f"retry on {FALLBACK_ENGINES}: {fb_detail}); fetch documentation "
            "URLs directly with lathe_research instead, and make sure each URL "
            "names the toolchain version lathe_probe reported"
        ),
    }


def _fetch(client, url):
    try:
        r = client.get(url)
        r.raise_for_status()
    except Exception as ex:  # noqa: BLE001
        return {"url": url, "error": f"{type(ex).__name__}: {ex}"}
    body = r.text
    ctype = r.headers.get("content-type", "")
    text = body if "html" not in ctype.lower() else _html_to_text(body)
    truncated = len(text) > FETCH_LIMIT
    return {
        "url": str(r.url),
        "content_type": ctype,
        "truncated": truncated,
        "text": text[:FETCH_LIMIT],
    }


def _resolved_model() -> str:
    """The model this `llm` process is actually running, for the tutorial's
    `--model` field.

    A model asked to name itself guesses -- the first end-to-end run recorded
    "Gemini 2.5 Pro" for what was in fact sonnet-4.6. So read it off the process
    instead: `-m/--model` on the command line if present (it beats the
    template), else the `model:` the template pins, else a LATHE_MODEL
    override. Empty string if none resolve, which leaves the model's own answer
    as a last resort rather than a hard failure.
    """
    argv = sys.argv
    for i, arg in enumerate(argv):
        if arg in ("-m", "--model") and i + 1 < len(argv):
            return argv[i + 1]
        if arg.startswith("--model="):
            return arg.split("=", 1)[1]
    path = _output(["llm", "templates", "path"])
    if path:
        try:
            for line in (pathlib.Path(path) / "lathe.yaml").read_text().splitlines():
                if line.startswith("model:"):
                    return line.split(":", 1)[1].strip()
        except OSError:
            pass
    return os.environ.get("LATHE_MODEL", "")


def lathe_probe(topic: str, tool_hints: str = "", queries: list[str] = None, **_extra) -> dict:
    """Pin the repo, toolchain versions and voice, and run the first web searches.

    This is the `/lathe` skill's "Pin the repo and versions" step plus the first
    half of "Research first", performed for you. Call it once, first.

    Args:
        topic: the tutorial topic, verbatim from the user's prompt.
        tool_hints: comma-separated tool names whose versions matter for this
            topic (e.g. "zig,llvm" or "go,postgres"). Probed on top of any
            names detected in the topic itself.
        queries: 2-4 web search queries aimed at authoritative sources --
            official docs, the spec/RFC, the reference implementation.

    Returns:
        cwd, git repo/branch/dirty, detected tool versions, the active voice
        spec and the list of available voices, and the search hits.
    """
    cwd = os.getcwd()
    git = {
        "origin": _run(["git", "-C", cwd, "remote", "get-url", "origin"]),
        "branch": _run(["git", "-C", cwd, "branch", "--show-current"]),
    }
    if not git["origin"]:
        git["note"] = "not a git repo with an origin -- standalone tutorial, no repo"

    wanted = {k.strip().lower() for k in tool_hints.split(",") if k.strip()}
    lowered = topic.lower()
    wanted |= {key for key in VERSION_PROBES if re.search(rf"\b{re.escape(key)}\b", lowered)}
    versions = {}
    for key in sorted(wanted):
        argv = VERSION_PROBES.get(key)
        if argv is None:
            argv = [key, "--version"]
        got = _run(argv)
        versions[key] = got or "NOT INSTALLED -- pin a version from the docs and say so"

    lathe_bin = shutil.which("lathe")
    voice = {
        "active": _output(["lathe", "voice", "show"]),
        "available": _output(["lathe", "voice", "list"]),
        "lathe_on_path": bool(lathe_bin),
    }

    client = _client()
    with client:
        searches = [_search(client, q, 6) for q in (queries or [])]

    return {
        "cwd": cwd,
        "git": git,
        "tool_versions": versions,
        "voice": voice,
        "searches": searches,
        "next": "call lathe_research(urls=[...]) once with 3-8 URLs, then lathe_publish",
    }


def lathe_research(urls: list[str], queries: list[str] = None, **_extra) -> dict:
    """Fetch the sources you will actually read. Call once, with every URL batched.

    Any URL is allowed -- it does not have to have come from a search. If the
    searches in lathe_probe came back empty, pass the canonical documentation
    URLs you already know here.

    Args:
        urls: 3-8 URLs to fetch and extract readable text from.
        queries: extra search queries, if you still need to find sources.

    Returns:
        For each URL: the resolved URL, its extracted text (truncated), or an
        error. Pages that failed are reported, never silently dropped.
    """
    client = _client()
    with client:
        searches = [_search(client, q, 6) for q in (queries or [])]
        pages = [_fetch(client, u) for u in (urls or [])]
    ok = [p for p in pages if "text" in p and p["text"]]
    return {
        "pages": pages,
        "searches": searches,
        "fetched_ok": len(ok),
        "note": (
            "Cite these inline and list them under ## Sources. Anything you could"
            " not ground here is [!UNVERIFIED], never asserted."
            if ok
            else "Nothing fetched. Follow the skill's 'No web tools in this session?'"
            " branch and pass no_sources_reason to lathe_publish."
        ),
    }


# Structural requirements SKILL.md states outright, checked here because a
# missing one is exactly as mechanical as a missing tag -- and invisible to the
# model that just wrote 400 lines. Opus 5 shipped a tutorial with no "By the end
# of this part" line; the prose read fine, the contract was broken.
REQUIRED_MARKERS = [
    ("By the end of this part", "the opening promise: \"By the end of this part, you'll have <concrete thing>\""),
    ("## What you'll build", "the '## What you'll build' section"),
    ("## Prerequisites", "the '## Prerequisites' section"),
    ("## Checkpoint", "the '## Checkpoint' section"),
    ("[!PREDICT]", "a > [!PREDICT] beat before the Checkpoint"),
    ("## What's next", "the \"## What's next\" section"),
    ("## Exercises", "the '## Exercises' section"),
    ("## Sources", "the '## Sources' section"),
]


def _structure_errors(markdown: str) -> list:
    out = [f"part-01.md is missing {label}" for marker, label in REQUIRED_MARKERS
           if marker not in markdown]
    if "```mermaid" in markdown:
        out.append("part-01.md contains a mermaid fence; the skill forbids them")
    if not re.search(r"^#\s+\S", markdown, re.M):
        out.append("part-01.md has no H1 title line")
    boxes = len(re.findall(r"^- \[ \]", markdown, re.M))
    if boxes and not (3 <= boxes <= 5):
        out.append(f"## Exercises has {boxes} checkboxes; the skill requires 3-5")
    return out


def lathe_publish(
    slug: str,
    markdown: str,
    tags: list[str],
    voice: str,
    model: str,
    sources: list[str] = None,
    repo: str = None,
    repo_branch: str = None,
    tools: list[str] = None,
    no_repo_reason: str = None,
    no_tools_reason: str = None,
    no_sources_reason: str = None,
    **_extra,
) -> dict:
    """Write /tmp/lathe-<slug>/part-01.md and store it via the lathe CLI.

    This is the skill's "Output files" plus "After writing" steps in one call.
    The pre-store gate is enforced here: each of repo, tools and sources needs
    either a value or an explicit reason, or the store is refused.

    Args:
        slug: the topic in kebab-case, e.g. "digital-synth-zig".
        markdown: the complete text of part-01.md.
        tags: 2-5 lowercase tags (language/runtime, domain, technique).
        voice: the voice name you wrote in, e.g. "plainspoken".
        model: fallback only -- the running model is read off the `llm`
            process (see `_resolved_model`) and overrides whatever is passed
            here, because a model asked to name itself guesses. Display label,
            e.g. "Claude Opus 5".
        sources: every authoritative URL you read.
        repo: the raw git origin URL from lathe_probe, if the tutorial is for it.
        repo_branch: that repo's branch.
        tools: pinned toolchain versions as "name:version", e.g. ["zig:0.13.0"].
        no_repo_reason: state it instead of repo, e.g. "standalone tutorial, no repo".
        no_tools_reason: state it instead of tools.
        no_sources_reason: state it instead of sources, e.g. "search unavailable this session".

    Returns:
        ok plus the store command and its output, or the gate errors to fix.
    """
    # Authoritative, not the model's self-report.
    model = _resolved_model() or model

    errors = []
    if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", slug or ""):
        errors.append("slug must be lowercase kebab-case, e.g. digital-synth-zig")
    if not (markdown or "").strip():
        errors.append("markdown is empty -- pass the full text of part-01.md")
    if not 2 <= len(tags or []) <= 5:
        errors.append("tags must be 2-5 lowercase tags")
    if not (voice or "").strip():
        errors.append("voice is required -- the name from lathe_probe's voice.active")
    if not (model or "").strip():
        errors.append("model is required -- the display label of the model you are")
    if not repo and not no_repo_reason:
        errors.append("pre-store gate: pass repo + repo_branch, or no_repo_reason")
    if not tools and not no_tools_reason:
        errors.append("pre-store gate: pass tools=['name:version', ...], or no_tools_reason")
    if not sources and not no_sources_reason:
        errors.append("pre-store gate: pass sources=[url, ...], or no_sources_reason")
    errors.extend(_structure_errors(markdown or ""))
    for entry in tools or []:
        if ":" not in entry:
            errors.append(f"tool {entry!r} must be name:version")
    if errors:
        return {"ok": False, "stored": False, "errors": errors}

    out_dir = pathlib.Path("/tmp") / f"lathe-{slug}"
    out_dir.mkdir(parents=True, exist_ok=True)
    part = out_dir / "part-01.md"
    part.write_text(markdown, encoding="utf-8")

    if not shutil.which("lathe"):
        return {
            "ok": False,
            "stored": False,
            "wrote": str(part),
            "errors": ["the `lathe` binary is not on PATH -- the file was written but not stored"],
        }

    argv = ["lathe", "store", str(out_dir)]
    for tag in tags:
        argv += ["--tag", tag]
    if repo:
        argv += ["--repo", repo]
        if repo_branch:
            argv += ["--repo-branch", repo_branch]
    for entry in tools or []:
        argv += ["--tool", entry]
    for src in sources or []:
        argv += ["--source", src]
    argv += ["--voice", voice, "--model", model]

    p = subprocess.run(argv, capture_output=True, text=True, timeout=120)
    return {
        "ok": p.returncode == 0,
        "stored": p.returncode == 0,
        "wrote": str(part),
        "command": " ".join(argv),
        "stdout": (p.stdout or "").strip(),
        "stderr": (p.stderr or "").strip(),
        "opt_outs": {
            k: v
            for k, v in (
                ("repo", no_repo_reason),
                ("tools", no_tools_reason),
                ("sources", no_sources_reason),
            )
            if v
        },
    }
