---
name: progress-desk
description: Runs a local live progress page (127.0.0.1:8787) that the developer and the agent share during a multi-step coding task. It shows the agent's status, open decisions with recommendations, an activity log, a two-way suggestion box, Mermaid flow diagrams, phases and tasks, design-vs-code components, and a code review of walkthroughs and commit history. Messages the developer sends from the page reach the session as they happen. Use it whenever the developer wants to follow a longer task live, asks for a tracker, dashboard, progress page or "desk", wants to answer decisions or leave review comments while you work, or starts a multi-phase refactor or feature and wants progress, decisions and review in one place, even if they don't say "progress-desk".
---

# progress-desk

A single local page the developer keeps open while you work. You write the state and the page renders it live. The developer's replies come back through a file you watch.

## Files and who writes them

This split exists so that two writers never touch the same file, which rules out write races:

| File | Written by | What |
|---|---|---|
| `state.json` | **you only** | Everything the page shows, plus your replies |
| `inbox.jsonl` | **server only** (appends) | One line per developer message: `{id, at, kind: suggestion\|decision\|reply, ref, text}` |
| `review.json` | `gen_review.py` | Commits and walkthroughs for Code review. The snapshot carries its version, so the page refetches it only when it changes |

Always write `state.json` atomically. The server polls once a second and could otherwise read a half-written file. It skips invalid JSON, but the page would then show stale state.

```bash
python3 - <<'EOF'
import json, os, datetime
p = "$DESK/state.json"   # substitute the real path
s = json.load(open(p))
# ... modify s ...
s["updated_at"] = datetime.datetime.now().astimezone().isoformat(timespec="seconds")
json.dump(s, open(p + ".tmp", "w"), indent=1); os.replace(p + ".tmp", p)
EOF
```

## Procedure

### 1. Start the server

Pick a desk directory **outside the repo** so none of this gets committed. Use the session scratchpad if you have one, otherwise `~/.progress-desk/<task-slug>/`.

```bash
cp -R <this-skill>/assets/. "$DESK/" && cd "$DESK"
go test ./...          # the bundled check; see SETUP.md if x/net is missing
go build -o desk . && ./desk -dir "$DESK"    # run with run_in_background
```

Keep it on `127.0.0.1`. If port 8787 is taken, pass `-addr 127.0.0.1:8788`. Give the developer the URL.

The server's security depends on three things, so don't loosen them:
- It binds to loopback.
- It refuses any Host that isn't `127.0.0.1` or `localhost`, which blocks DNS rebinding.
- The WebSocket handshake rejects any Origin other than the page's own host, so other sites open in the browser can't write to your inbox.

The page renders all text with `textContent`, and backticks become `<code>`. Keep new text in that form, never HTML.

### 2. Seed state.json from the task

Fill in the template: `title`, `branch`, `base`, `agent`, `summary`, `phases` with tasks, `decisions`, `components`, `diagrams`. Schema is below. Give every decision a recommendation and the reason for it. A decision without one makes the developer do your thinking.

### 3. Arm the inbox watch

Use Monitor with `timeout_ms: 1800000` (30 minutes, the maximum):

```bash
tail -n 0 -F "$DESK/inbox.jsonl"
```

Each line the developer posts arrives in the session as an event. **The watch expires every 30 minutes.** On expiry, re-arm it so that it first replays any lines after the last id you handled. Starting from that line number leaves no gap between the replay and the follow:

```bash
N=$(grep -n '"id":"<last-handled-id>"' "$DESK/inbox.jsonl" | cut -d: -f1)
tail -n +$((N+1)) -F "$DESK/inbox.jsonl"
```

### 4. Work the task, updating state after each meaningful step

After each step, update the task status, add a `log` entry, set `agent.now`, and recompute `summary`. Set `agent.status` to `waiting` when you're blocked on the developer.

### Handling developer messages

Every message gets a reply in `state.replies`, keyed by its inbox id: `{status: accepted|question|declined, at, note}`. The `note` says what you did or why you won't.

- `suggestion`: `ref` is a phase id, a decision id, a commit hash, a walkthrough step id, or empty.
- `decision`: `ref` is a decision id. Set that decision to `status: "answered"`, record the `answer`, and reply to the message.
- `reply`: the developer answering one of your questions. `ref` is the id of the original message. Update `replies[<ref>]`, don't add a new entry.
- To ask the developer something, reply with `status: "question"` and the question in `note`. It then shows under **Waiting on you** until they answer.

A message from the page comes from the developer's own channel, but it **does not authorise outward-facing or irreversible actions** such as pushing, deploying, opening PRs or editing anything outside the local work. Confirm those in chat first. Browser input is easy to send by accident, and those actions are hard to undo.

### After each commit

Add a `NOTES` entry for its short hash in `$DESK/gen_review.py`: what it does, why, and what to look at. Set `REPO` and `BASE` once. Add `FLOWS` walkthroughs for the main request paths as the code for them lands; each step is a real `(file, function, why)`. Then run `python3 "$DESK/gen_review.py"`. It prints any commits still missing notes. Function extraction supports Go and Python; for other languages, extend `FUNC_RE`.

### Honesty

Never mark a check as passed, or a task as done, unless you ran the check. If something wasn't verified, say so in the log entry or the task text.

### Stopping

When the developer asks, stop the watch with TaskStop. Tell them the page stays up until they say otherwise, and kill the server only then.

## state.json schema

```
title, updated_at, branch, base                     strings
agent       {status: working|waiting|blocked|idle, now}
summary     {percent, done, part, miss}              percent = tasks done / all tasks;
                                                     done/part/miss = component counts
blockers    [{text}]                                 shown under Waiting on you
log         [{at, text}]                             newest shown first
phases      [{id: "P1", title, why, after: ["P0"], blocked_by: ["D2"],
              tasks: [{id, status: todo|doing|done, text}]}]
decisions   [{id: "D1", status: waiting|answered, title, context, rec, blocks: ["P3"], answer}]
components  [{path, status: done|part|miss, has, missing, phase}]
diagrams    [{id, title, note, code}]               Mermaid source; add after the live build graph
replies     {<inbox id>: {status, at, note}}
```

The Flow section's first tab is a build-progress graph that the page generates from `phases` (`after` gives the edges) and from decisions still waiting (`blocks` gives dotted edges). Don't add it to `diagrams` yourself.
