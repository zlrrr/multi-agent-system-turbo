#!/usr/bin/env python3
"""Assert one topology's demo report is a real diagnosis.

CI runs the topology comparison the user manual tells operators to run. Checking
only that the command exited zero would pass on an empty report, so this checks
what the conformance contract requires of every topology: it names itself, it
reached a hypothesis and a summary, and it actually called a model.
"""
import json
import sys

path, want_topology = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as fh:
    report = json.load(fh)

problems = []
if report.get("topology") != want_topology:
    problems.append(f"topology is {report.get('topology')!r}, want {want_topology!r}")
if not report.get("hypotheses"):
    problems.append("no hypothesis")
if not (report.get("summary") or "").strip():
    problems.append("no summary")
if report.get("usage", {}).get("llm_calls", 0) <= 0:
    problems.append("no model call was made")

if problems:
    print(f"{want_topology}: " + "; ".join(problems), file=sys.stderr)
    sys.exit(1)
