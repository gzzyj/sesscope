#!/usr/bin/env python3
"""Tiny GET/PUT file server for the Windows E2E harness.

GET /<path>          serve from --root
PUT /upload/<path>   write to <root>/results/<path>

Usage: python3 serve.py --root /tmp/sesscope-e2e --port 18081
"""
import argparse
import http.server
import os
import sys
import urllib.parse


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", default="/tmp/sesscope-e2e")
    parser.add_argument("--port", type=int, default=18081)
    args = parser.parse_args()
    root = os.path.abspath(args.root)
    upload = os.path.join(root, "results")
    os.makedirs(upload, exist_ok=True)

    class Handler(http.server.SimpleHTTPRequestHandler):
        def translate_path(self, path):
            rel = urllib.parse.urlparse(path).path.lstrip("/")
            if rel.startswith("upload/"):
                return os.path.join(upload, rel[len("upload/"):])
            return os.path.join(root, rel)

        def do_PUT(self):
            target = self.translate_path(self.path)
            os.makedirs(os.path.dirname(target), exist_ok=True)
            length = int(self.headers.get("Content-Length", "0"))
            with open(target, "wb") as handle:
                handle.write(self.rfile.read(length))
            self.send_response(201)
            self.end_headers()
            self.wfile.write(b"ok")

        def log_message(self, fmt, *values):
            sys.stderr.write("%s - %s\n" % (self.address_string(), fmt % values))

    http.server.ThreadingHTTPServer(("0.0.0.0", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
