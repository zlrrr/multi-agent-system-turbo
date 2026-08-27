#!/usr/bin/env python3
"""Stub Prometheus and Loki servers for the MAS-Turbo demo.

They serve a coherent story: a Redis instance at 99% of maxmemory, evicting
keys, with OOM refusals in the log — the scenario the shipped knowledge pack is
written to recognise.
"""
import json
import re
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

SERIES = [
    (r"redis_memory_used_bytes", "1013972992"),
    (r"redis_memory_max_bytes", "1024000000"),
    (r"redis_evicted_keys_total", "18.4"),
    (r"redis_expired_keys_total", "2.1"),
    (r"redis_up", "1"),
    (r"redis_connected_clients", "184"),
    (r"redis_blocked_clients", "0"),
    (r"redis_config_maxclients", "10000"),
    (r"redis_rejected_connections_total", "0"),
    (r"redis_instantaneous_ops_per_sec", "24500"),
    (r"keyspace_hits", "0.62"),
    (r"redis_mem_fragmentation_ratio", "1.18"),
    (r"redis_latest_fork_usec", "48000"),
    (r"redis_rdb_last_bgsave_status", "1"),
    (r"redis_aof_last_write_status", "1"),
    (r"redis_rdb_changes_since_last_save", "9120"),
    (r"redis_master_link_up", "1"),
    (r"redis_connected_slaves", "2"),
    (r"repl_offset", "0"),
    (r"redis_cpu_", "0.41"),
    (r"redis_db_keys", "4180000"),
]

LOG_LINES = [
    "1:M 23 Aug 2026 10:41:02.118 # OOM command not allowed when used memory > 'maxmemory'.",
    "1:M 23 Aug 2026 10:40:55.902 # OOM command not allowed when used memory > 'maxmemory'.",
    "1:M 23 Aug 2026 10:38:11.447 * 10000 changes in 60 seconds. Saving...",
    "1:M 23 Aug 2026 10:38:11.502 * Background saving started by pid 412",
    "1:M 23 Aug 2026 10:38:12.031 * Background saving terminated with success",
    "1:M 23 Aug 2026 10:31:44.219 # Warning: maxmemory reached, evicting keys",
]


def value_for(query: str) -> str:
    for pattern, value in SERIES:
        if re.search(pattern, query):
            return value
    return "0"


class Prom(BaseHTTPRequestHandler):
    def _send(self, payload):
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        form = parse_qs(self.rfile.read(length).decode())
        query = form.get("query", [""])[0]
        value = value_for(query)
        path = urlparse(self.path).path
        if path.endswith("query_range"):
            values = [[1756000000 + i * 60, value] for i in range(12)]
            self._send({"status": "success", "data": {"resultType": "matrix", "result": [
                {"metric": {"instance": "redis-0", "job": "redis"}, "values": values}]}})
        elif path.endswith("series"):
            self._send({"status": "success", "data": [
                {"__name__": "redis_up", "instance": "redis-0", "job": "redis"}]})
        else:
            self._send({"status": "success", "data": {"resultType": "vector", "result": [
                {"metric": {"instance": "redis-0", "job": "redis"}, "value": [1756000000, value]}]}})

    def do_GET(self):
        self._send({"status": "success", "data": []})

    def log_message(self, *args):
        pass


class Loki(BaseHTTPRequestHandler):
    def _send(self, payload):
        body = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path = urlparse(self.path).path
        if "label" in path:
            self._send({"status": "success", "data": ["job", "pod", "namespace"]})
            return
        values = [[str(1756000000000000000 + i * 1000000000), line]
                  for i, line in enumerate(LOG_LINES)]
        self._send({"status": "success", "data": {"resultType": "streams", "result": [
            {"stream": {"job": "redis", "pod": "redis-0"}, "values": values}]}})

    def log_message(self, *args):
        pass


def serve(port, handler):
    HTTPServer(("127.0.0.1", port), handler).serve_forever()


if __name__ == "__main__":
    threading.Thread(target=serve, args=(19090, Prom), daemon=True).start()
    serve(13100, Loki)
