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

# Cost is printed only when it was measured. The demo prices nothing, so this
# reads "unpriced" — which is the honest answer, and the point: a zero here
# would be read as "this run was free".
cost = usage.get("cost") or {}
if cost.get("known"):
    money = "$%.4f" % cost.get("usd", 0.0)
else:
    money = "unpriced"

print("%-14s %6d %6d %8d %10s  %s" % (
    report.get("topology", "?"),
    usage.get("llm_calls", 0),
    usage.get("tool_calls", 0),
    usage.get("wall_millis", 0),
    money,
    statement[:52],
))
