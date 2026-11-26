#!/usr/bin/env python3
"""Exercise site routing with synthetic authenticated/anonymous upstreams."""
import http.server
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request


def main():
    root = Path(__file__).resolve().parents[1]
    nginx = os.getenv("NGINX_BIN") or shutil.which("nginx")
    if not nginx:
        raise SystemExit("nginx is required for this local route test")
    token = secrets.token_urlsafe(32)
    seen = []

    class Upstream(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            seen.append((self.path, self.headers.get("Authorization") == "Bearer " + token))
            self.send_response(200 if self.headers.get("Cookie") == "fixture=signed" else 401)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"ok":true}')

        def log_message(self, *args):
            pass

    upstream = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
    threading.Thread(target=upstream.serve_forever, daemon=True).start()
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    with tempfile.TemporaryDirectory(prefix="options-entry-", dir=os.getenv("OPTIONS_E2E_ROOT")) as work:
        work = Path(work)
        (work / "index.html").write_text("<!doctype html><title>options fixture</title>")
        (work / "assets").mkdir()
        (work / "assets" / "probe.js").write_text("// public asset\n")
        text = (root / "frontend" / "nginx.conf.template").read_text()
        text = text.replace("listen 8080;", f"listen 127.0.0.1:{port};")
        text = text.replace("/usr/share/nginx/html", str(work))
        text = text.replace("http://platform:5051", f"http://127.0.0.1:{upstream.server_port}")
        text = text.replace("http://weibospider:5050", f"http://127.0.0.1:{upstream.server_port}")
        text = text.replace("${BACKEND_API_TOKEN}", token)
        config = work / "nginx.conf"
        config.write_text(f"pid {work}/nginx.pid;\nerror_log {work}/error.log;\nevents {{}}\nhttp {{ access_log off;\n" + text + "\n}\n")
        subprocess.run([nginx, "-p", str(work) + "/", "-c", str(config), "-t"], check=True, capture_output=True)
        process = subprocess.Popen([nginx, "-p", str(work) + "/", "-c", str(config), "-g", "daemon off;"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, req, fp, code, msg, headers, newurl):
                return None

        opener = urllib.request.build_opener(NoRedirect)

        def get(url, auth=False):
            headers = {}
            if auth:
                headers["Cookie"] = "fixture=signed"
                # A stale browser Basic header must not become upstream auth.
                headers["Authorization"] = "Basic dGVzdDp0ZXN0"
            req = urllib.request.Request(url, headers=headers)
            try:
                with opener.open(req, timeout=2) as response:
                    assert not response.headers.get("WWW-Authenticate"), "unexpected browser authentication"
                    return response.status, response.read()
            except urllib.error.HTTPError as error:
                return error.code, error.read()

        try:
            public = f"http://127.0.0.1:{port}"
            for attempt in range(40):
                try:
                    if get(public + "/healthz")[0] == 200:
                        break
                except urllib.error.URLError:
                    if process.poll() is not None:
                        raise RuntimeError("test nginx exited")
                    time.sleep(.05)
            checks = [
                (public + "/options", False, 200),
                (public + "/options/", False, 200),
                (public + "/assets/probe.js", False, 200),
                (public + "/api/platform/options/status", False, 401),
                (public + "/options", True, 200),
                (public + "/api/platform/options/status", True, 200),
                (public + "/api/platform/unknown", False, 404),
                (public + "/api/platform/options", False, 301),
                (public + "/api/platform/options-other/status", False, 404),
                (public + "/ledger", False, 200),
                (public + "/tax", False, 200),
                (public + "/liquor", False, 200),
                (public + "/weibo/", False, 302),
                (public + "/api/stats", False, 401),
            ]
            for url, auth, expected in checks:
                actual, body = get(url, auth)
                assert actual == expected, (url, expected, actual)
                assert token.encode() not in body, "internal token leaked in response"
            assert seen == [("/api/platform/options/status", True)] * 2 + [("/api/platform/auth/owner", True)] * 2, seen
            print(f"PASS: {len(checks)} site route checks; protected APIs and owner subrequests verified")
        finally:
            process.terminate()
            process.wait(timeout=10)
            upstream.shutdown()


if __name__ == "__main__":
    main()
