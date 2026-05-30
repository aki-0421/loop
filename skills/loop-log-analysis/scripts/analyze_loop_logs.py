#!/usr/bin/env python3
"""Summarize loop run logs and durable audit artifacts."""

from __future__ import annotations

import argparse
import json
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any, Optional, Tuple


TOKEN_FIELDS = (
    "input_tokens",
    "output_tokens",
    "cache_read_tokens",
    "cache_creation_tokens",
    "reasoning_output_tokens",
    "total_tokens",
)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("path", nargs="?", default=".", help="repo root, .loop dir, run dir, or iteration dir")
    parser.add_argument("--run", help="run id to inspect when PATH is a repo root or .loop dir")
    parser.add_argument("--iteration", default="latest", help="iteration id, latest, or all")
    parser.add_argument("--json", action="store_true", help="print machine-readable JSON")
    parser.add_argument("--max-errors", type=int, default=8, help="maximum error lines to include")
    args = parser.parse_args()

    target = Path(args.path).resolve()
    run_dir, fixed_iteration = resolve_run_dir(target, args.run)
    if run_dir is None:
        raise SystemExit(f"could not locate loop run from {target}")

    iteration = fixed_iteration or args.iteration
    iteration_dirs = resolve_iteration_dirs(run_dir, iteration)
    if not iteration_dirs:
        raise SystemExit(f"could not locate iteration {iteration!r} in {run_dir}")

    report = {
        "run_dir": str(run_dir),
        "run_id": run_dir.name,
        "run_state": read_json(run_dir / "run-state.json"),
        "run_errors": read_error_lines(run_dir / "errors.log", args.max_errors),
        "iterations": [analyze_iteration(path, args.max_errors) for path in iteration_dirs],
    }

    if args.json:
        print(json.dumps(report, indent=2, sort_keys=True))
    else:
        print_text_report(report)
    return 0


def resolve_run_dir(path: Path, run_id: Optional[str]) -> Tuple[Optional[Path], Optional[str]]:
    if path.name.isdigit() and (path / "agent-events.jsonl").exists():
        run_dir = path.parent.parent
        return run_dir if (run_dir / "run-state.json").exists() else None, path.name

    if (path / "run-state.json").exists() and (path / "iterations").is_dir():
        return path, None

    loop_dir = path
    if (path / ".loop").is_dir():
        loop_dir = path / ".loop"
    if path.name == ".loop" or (loop_dir / "runs").is_dir():
        runs_dir = loop_dir / "runs"
        if run_id:
            candidate = runs_dir / run_id
            return (candidate if candidate.exists() else None), None
        return latest_child_dir(runs_dir), None

    if path.name == "runs" and path.is_dir():
        if run_id:
            candidate = path / run_id
            return (candidate if candidate.exists() else None), None
        return latest_child_dir(path), None

    return None, None


def resolve_iteration_dirs(run_dir: Path, iteration: str) -> list[Path]:
    iterations_dir = run_dir / "iterations"
    if iteration == "all":
        return sorted([p for p in iterations_dir.iterdir() if p.is_dir()], key=lambda p: p.name)
    if iteration == "latest":
        latest = latest_child_dir(iterations_dir)
        return [latest] if latest else []
    candidate = iterations_dir / iteration
    return [candidate] if candidate.is_dir() else []


def latest_child_dir(path: Path) -> Optional[Path]:
    if not path.is_dir():
        return None
    children = [p for p in path.iterdir() if p.is_dir()]
    if not children:
        return None
    return sorted(children, key=lambda p: p.name)[-1]


def analyze_iteration(iter_dir: Path, max_errors: int) -> dict[str, Any]:
    stream_paths = [iter_dir / "agent-events.jsonl"]
    task_dirs = sorted([p for p in (iter_dir / "tasks").glob("*") if p.is_dir()]) if (iter_dir / "tasks").is_dir() else []
    stream_paths.extend(task / "agent-events.jsonl" for task in task_dirs)

    streams = [read_event_stream(path, iter_dir) for path in stream_paths if path.exists()]
    events = [event for stream in streams for event in stream["events"]]
    events_sorted = sorted(enumerate(events), key=lambda item: (str(item[1].get("ts", "")), item[0]))
    ordered_events = [event for _, event in events_sorted]

    validations = [
        event for event in ordered_events
        if event.get("type") == "validation.command.completed"
    ]
    failed_validations = [
        event for event in validations
        if int_or_none(event.get("exit_code")) not in (None, 0)
    ]
    exits = [event for event in ordered_events if event.get("type") == "agent.exited"]
    nonzero_exits = [
        event for event in exits
        if int_or_none(event.get("exit_code")) not in (None, 0)
    ]
    discarded_attempts = [
        event for event in ordered_events
        if event.get("type") == "task.attempt.discarded"
    ]

    task_summaries = [summarize_task(task) for task in task_dirs]
    artifacts = summarize_artifacts(iter_dir)

    return {
        "iteration_id": iter_dir.name,
        "iteration_dir": str(iter_dir),
        "event_count": len(ordered_events),
        "event_types": dict(Counter(str(event.get("type", "unknown")) for event in ordered_events)),
        "first_event_ts": str(ordered_events[0].get("ts", "")) if ordered_events else "",
        "last_event_ts": str(ordered_events[-1].get("ts", "")) if ordered_events else "",
        "usage": summarize_usage(streams),
        "commands": summarize_commands(ordered_events),
        "files_read": summarize_file_reads(ordered_events),
        "failed_validations": failed_validations,
        "nonzero_exits": nonzero_exits,
        "discarded_attempts": discarded_attempts,
        "errors": read_error_lines(iter_dir / "errors.log", max_errors),
        "tasks": task_summaries,
        "artifacts": artifacts,
    }


def read_event_stream(path: Path, iter_dir: Path) -> dict[str, Any]:
    events = []
    for line_number, line in enumerate(read_text(path).splitlines(), start=1):
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError as exc:
            event = {"type": "parse_error", "error": str(exc), "line": line_number}
        event["_source"] = relative_to(path, iter_dir)
        event["_line"] = line_number
        events.append(event)
    return {"path": str(path), "events": events}


def summarize_usage(streams: list[dict[str, Any]]) -> dict[str, Any]:
    totals = defaultdict(int)
    scopes = []
    estimated = False

    for stream in streams:
        current = new_usage_scope(stream["path"])
        for event in stream["events"]:
            if event.get("type") == "agent.started":
                if current["seen"]:
                    scopes.append(finalize_scope(current))
                current = new_usage_scope(stream["path"], event)
            if event.get("type") != "agent.usage":
                continue
            current["seen"] = True
            if event.get("estimated") is True:
                estimated = True
                current["estimated"] = True
            if event.get("delta") is True:
                for field in TOKEN_FIELDS:
                    current["delta"][field] += max(0, int_or_zero(event.get(field)))
            else:
                for field in TOKEN_FIELDS:
                    if field in event:
                        current["snapshot"][field] = max(current["snapshot"][field], max(0, int_or_zero(event.get(field))))
        if current["seen"]:
            scopes.append(finalize_scope(current))

    for scope in scopes:
        for field in TOKEN_FIELDS:
            totals[field] += scope["tokens"].get(field, 0)

    return {
        "estimated": estimated,
        "totals": dict(totals),
        "scopes": scopes,
    }


def new_usage_scope(path: str, start_event: Optional[dict[str, Any]] = None) -> dict[str, Any]:
    start_event = start_event or {}
    return {
        "path": path,
        "agent_type": start_event.get("agent_type", ""),
        "task_id": start_event.get("task_id", ""),
        "started_at": start_event.get("ts", ""),
        "seen": False,
        "estimated": False,
        "delta": defaultdict(int),
        "snapshot": defaultdict(int),
    }


def finalize_scope(scope: dict[str, Any]) -> dict[str, Any]:
    tokens = {}
    for field in TOKEN_FIELDS:
        tokens[field] = max(scope["delta"].get(field, 0), scope["snapshot"].get(field, 0))
    return {
        "path": scope["path"],
        "agent_type": scope["agent_type"],
        "task_id": scope["task_id"],
        "started_at": scope["started_at"],
        "estimated": scope["estimated"],
        "tokens": tokens,
    }


def summarize_commands(events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    out = []
    for event in events:
        if event.get("type") != "agent.command":
            continue
        command = " ".join(str(event.get(key, "")).strip() for key in ("command", "args")).strip()
        out.append({
            "ts": event.get("ts", ""),
            "agent_type": event.get("agent_type", ""),
            "task_id": event.get("task_id", ""),
            "command": command,
            "source": event.get("_source", ""),
        })
    return out


def summarize_file_reads(events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    counts = Counter()
    for event in events:
        if event.get("type") == "agent.file_read" and event.get("path"):
            counts[str(event["path"])] += 1
    return [{"path": path, "count": count} for path, count in counts.most_common(20)]


def summarize_task(task_dir: Path) -> dict[str, Any]:
    task = read_json(task_dir / "task.json")
    result = read_json(task_dir / "task-result.json")
    todo = read_json(task_dir / "task-todo.json")
    merge = read_json(task_dir / "task-merge.json")
    merge_commit = merge.get("merge_commit", {}) if isinstance(merge, dict) else {}
    return {
        "sequence": task_dir.name,
        "id": task.get("id", result.get("task_id", "")) if isinstance(task, dict) else "",
        "title": task.get("title", "") if isinstance(task, dict) else "",
        "status": result.get("status", "") if isinstance(result, dict) else "",
        "summary": result.get("summary", "") if isinstance(result, dict) else "",
        "todo_count": len(todo.get("items", [])) if isinstance(todo, dict) else 0,
        "merge_commit": merge_commit.get("sha", "") if isinstance(merge_commit, dict) else "",
    }


def summarize_artifacts(iter_dir: Path) -> dict[str, Any]:
    names = [
        "task-tree.json",
        "review-result.json",
        "validation.md",
        "errors.log",
        "pr-state.json",
        "pr-checks.json",
        "pr-check-log.txt",
        "github-updates.md",
    ]
    artifacts = {}
    for name in names:
        path = iter_dir / name
        artifacts[name] = {"exists": path.exists(), "bytes": path.stat().st_size if path.exists() else 0}
    artifacts["validation_outputs"] = [
        {"name": path.name, "bytes": path.stat().st_size}
        for path in sorted(iter_dir.glob("validation-output-*.log"))
    ]
    return artifacts


def print_text_report(report: dict[str, Any]) -> None:
    state = report.get("run_state") if isinstance(report.get("run_state"), dict) else {}
    print("# Loop Log Analysis")
    print()
    print(f"Run: {report['run_id']}")
    if state:
        print(f"Stage: {state.get('stage', '')}")
        print(f"Base: {state.get('base_branch', '')}")
        print(f"Current iteration: {state.get('current_iteration', '')}")
        if state.get("goal"):
            print(f"Goal: {state.get('goal')}")
    if report.get("run_errors"):
        print()
        print("Run errors:")
        for line in report["run_errors"]:
            print(f"- {line}")

    for iteration in report["iterations"]:
        print()
        print(f"## Iteration {iteration['iteration_id']}")
        print(f"Events: {iteration['event_count']} ({iteration['first_event_ts']} to {iteration['last_event_ts']})")
        top_types = Counter(iteration["event_types"]).most_common(8)
        if top_types:
            print("Top event types: " + ", ".join(f"{name}={count}" for name, count in top_types))

        usage = iteration["usage"]
        tokens = usage["totals"]
        token_note = " estimated" if usage.get("estimated") else ""
        if tokens:
            print(
                "Usage%s: %s input, %s output, %s cache read, %s cache creation"
                % (
                    token_note,
                    tokens.get("input_tokens", 0),
                    tokens.get("output_tokens", 0),
                    tokens.get("cache_read_tokens", 0),
                    tokens.get("cache_creation_tokens", 0),
                )
            )

        signals = []
        if iteration["errors"]:
            signals.append(f"errors.log has {len(iteration['errors'])} captured line(s)")
        if iteration["nonzero_exits"]:
            signals.append(f"{len(iteration['nonzero_exits'])} non-zero agent exit(s)")
        if iteration["failed_validations"]:
            signals.append(f"{len(iteration['failed_validations'])} failed validation command(s)")
        if iteration["discarded_attempts"]:
            signals.append(f"{len(iteration['discarded_attempts'])} discarded task attempt(s)")
        print("Signals: " + (", ".join(signals) if signals else "no obvious failure signals"))

        if iteration["errors"]:
            print("Iteration errors:")
            for line in iteration["errors"]:
                print(f"- {line}")

        if iteration["failed_validations"]:
            print("Failed validation:")
            for event in iteration["failed_validations"]:
                name = event.get("name", event.get("command", "validation"))
                print(f"- {name}: exit {event.get('exit_code')} at {event.get('ts', '')}")

        if iteration["nonzero_exits"]:
            print("Non-zero agent exits:")
            for event in iteration["nonzero_exits"]:
                role = event.get("agent_type", "")
                task = event.get("task_id", "")
                suffix = f" task={task}" if task else ""
                print(f"- {role}{suffix}: exit {event.get('exit_code')} at {event.get('ts', '')}")

        if iteration["tasks"]:
            print("Tasks:")
            for task in iteration["tasks"]:
                label = task["id"] or task["sequence"]
                status = task["status"] or "unknown"
                summary = task["summary"] or task["title"]
                print(f"- {label}: {status}; {summary}")

        if iteration["commands"]:
            print("Commands:")
            for command in iteration["commands"][-8:]:
                label = command["agent_type"] or "agent"
                if command.get("task_id"):
                    label += f"/{command['task_id']}"
                print(f"- {label}: {command['command']}")


def read_json(path: Path) -> Any:
    if not path.exists():
        return {}
    try:
        return json.loads(path.read_text())
    except (OSError, json.JSONDecodeError):
        return {}


def read_error_lines(path: Path, limit: int) -> list[str]:
    if not path.exists():
        return []
    lines = [line.strip() for line in read_text(path).splitlines() if line.strip()]
    return lines[:limit]


def read_text(path: Path) -> str:
    try:
        return path.read_text(errors="replace")
    except OSError:
        return ""


def relative_to(path: Path, root: Path) -> str:
    try:
        return path.relative_to(root).as_posix()
    except ValueError:
        return str(path)


def int_or_zero(value: Any) -> int:
    value = int_or_none(value)
    return value if value is not None else 0


def int_or_none(value: Any) -> Optional[int]:
    if isinstance(value, bool):
        return None
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        return int(value)
    if isinstance(value, str):
        try:
            return int(value)
        except ValueError:
            return None
    return None


if __name__ == "__main__":
    raise SystemExit(main())
