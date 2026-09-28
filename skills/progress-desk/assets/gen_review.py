#!/usr/bin/env python3
"""Build review.json for progress-desk's Code review section from the repo's git
history and current source. Set REPO and BASE, add a NOTES entry per commit,
then rerun; an open page refetches it on its own."""
import json, os, re, subprocess, sys, datetime

REPO = "/path/to/repo"    # the repo being worked on
BASE = "abc1234"          # first commit to show (usually the first commit of this task)
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "review.json")
MAX_DIFF_LINES = 700      # per file; longer diffs are cut with a note
SKIP_DIFF = {"go.sum", "package-lock.json", "poetry.lock", "uv.lock", "yarn.lock"}  # lockfile noise: list it, don't show it
EMPTY_TREE = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"  # diff base for a root commit

def git(*args):
    return subprocess.run(["git", "-C", REPO, *args], check=True, capture_output=True, text=True).stdout

# ---- per-commit notes: what changed, why, what to look at ----------------
# Add one entry after every commit. Backticks render as code on the page.
NOTES = {
 # "abc1234": ("One line: what this commit does.",
 #             ["Why it's built this way.", "Look at: the part a reviewer should check."]),
}

# ---- walkthroughs: request flows through the real functions -------------
# Each step is (file, function name, why it matters), in the order the code runs.
FLOWS = [
 # {"id": "upload", "title": "Upload", "summary": "POST /files. Returns file_id.", "steps": [
 #   ("internal/router/upload.go", "upload", "The handler: validate, stream to storage."),
 # ]},
]

# ponytail: Go (brace-closed) and Python (indent-closed) only; add a pattern per language as needed.
FUNC_RE = r"^(\s*)(func (\([^)]*\) )?|(async )?def ){name}[\[(]"

def extract(path, name):
    lines = open(os.path.join(REPO, path)).read().split("\n")
    rx = re.compile(FUNC_RE.format(name=re.escape(name)))
    for i, l in enumerate(lines):
        m = rx.match(l)
        if m:
            indent, start = m.group(1), i
            while start > 0 and lines[start - 1].strip().startswith(("//", "#", "@")):
                start -= 1
            end = i + 1
            if "def " in m.group(2):   # Python: body ends at the first line indented no deeper than the def
                while end < len(lines) and (not lines[end].strip() or lines[end].startswith(indent + " ") or lines[end].startswith(indent + "\t")):
                    end += 1
                while lines[end - 1].strip() == "":
                    end -= 1
                end -= 1
            else:                      # Go: body ends at the closing brace at the func's indent
                while end < len(lines) and lines[end] != indent + "}":
                    end += 1
            return {"start": start + 1, "end": end + 1, "code": "\n".join(lines[start:end + 1])}
    sys.exit(f"gen_review: {name} not found in {path}")

def commit_files(rev, parent):
    files = []
    status = dict((l.split("\t")[-1], l.split("\t")[0][0]) for l in git("diff", "--no-renames", "--name-status", parent, rev).splitlines() if l)
    for l in git("diff", "--no-renames", "--numstat", parent, rev).splitlines():
        add, rem, path = l.split("\t")
        f = {"path": path, "status": status.get(path, "M"), "add": int(add) if add != "-" else 0, "del": int(rem) if rem != "-" else 0}
        if f["status"] == "D":
            f["note"] = "Deleted"
        elif os.path.basename(path) in SKIP_DIFF:
            f["note"] = "Lockfile, not shown"
        else:
            diff = git("diff", "-U3", parent, rev, "--", path).split("\n")
            body = [d for d in diff if not d.startswith(("diff --git", "index ", "--- ", "+++ ", "new file mode", "deleted file mode", "similarity", "rename "))]
            if len(body) > MAX_DIFF_LINES:
                f["note"] = f"Showing the first {MAX_DIFF_LINES} of {len(body)} diff lines"
                body = body[:MAX_DIFF_LINES]
            f["diff"] = "\n".join(body).rstrip("\n")
        files.append(f)
    return files

def main():
    def has_parent(rev):
        return subprocess.run(["git", "-C", REPO, "rev-parse", "-q", "--verify", f"{rev}^1"], capture_output=True).returncode == 0
    span = f"{BASE}^1..HEAD" if has_parent(BASE) else "HEAD"
    log = git("log", "--reverse", "--format=%h%x1f%s%x1f%b%x1f%aI%x1e", span, "--first-parent").strip("\x1e\n")
    commits = []
    for entry in filter(None, log.split("\x1e")):
        h, subject, body, date = [x.strip() for x in entry.split("\x1f")]
        parent = f"{h}^1" if has_parent(h) else EMPTY_TREE
        what, points = NOTES.get(h, ("", []))
        commits.append({"hash": h, "subject": subject, "body": body, "date": date,
                        "what": what, "points": points, "files": commit_files(h, parent)})
    flows = []
    for fl in FLOWS:
        steps = []
        for n, (path, name, why) in enumerate(fl["steps"], 1):
            steps.append({"id": f"{fl['id']}-{n}", "file": path, "func": name, "why": why, **extract(path, name)})
        flows.append({**{k: fl[k] for k in ("id", "title", "summary")}, "steps": steps})
    head = git("rev-parse", "--short", "HEAD").strip()
    out = {"generated_at": datetime.datetime.now().astimezone().isoformat(timespec="seconds"),
           "head": head, "branch": git("rev-parse", "--abbrev-ref", "HEAD").strip(), "commits": commits, "flows": flows}
    tmp = OUT + ".tmp"
    json.dump(out, open(tmp, "w"))
    os.replace(tmp, OUT)
    missing = [c["hash"] for c in commits if not c["what"]]
    print(f"review.json: {len(commits)} commits, {sum(len(f['steps']) for f in flows)} walkthrough steps, "
          f"{os.path.getsize(OUT)//1024} KiB, head {head}" + (f"; commits without notes: {missing}" if missing else ""))

main()
