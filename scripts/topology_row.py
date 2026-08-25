#!/usr/bin/env python3
"""Print one row of the demo's topology comparison.

It exists as a file rather than inline in demo.sh so the shell script stays
readable and the row format has one definition.
"""
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    report = json.load(fh)

usage = report.get("usage", {})
hypotheses = report.get("hypotheses") or [{}]
statement = hypotheses[0].get("statement") or "(none)"

print("%-14s %6d %6d %8d  %s" % (
    report.get("topology", "?"),
    usage.get("llm_calls", 0),
    usage.get("tool_calls", 0),
    usage.get("wall_millis", 0),
    statement[:64],
))
