#!/usr/bin/env python3
"""Exercise the no-login HTTPS template with disposable local TLS fixtures."""
import http.server
import os
from pathlib import Path
import secrets
import shutil
import socket
import ssl
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
        raise SystemExit("nginx is required for this local HTTPS route test")
    token = secrets.token_urlsafe(32)
    seen = []

    class Upstream(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            seen.append((self.path, self.headers.get("Authorization") == "Bearer " + token))
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"ok":true}')

        def log_message(self, *args):
            pass

    upstream = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
    threading.Thread(target=upstream.serve_forever, daemon=True).start()
    ports = []
    for _ in range(2):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            ports.append(sock.getsockname()[1])
    with tempfile.TemporaryDirectory(prefix="options-entry-", dir=os.getenv("OPTIONS_E2E_ROOT")) as work:
        work = Path(work)
        cert, key = work / "fullchain.pem", work / "privkey.pem"
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                        "-subj", "/CN=localhost", "-addext", "subjectAltName=IP:127.0.0.1,DNS:localhost",
                        "-keyout", str(key), "-out", str(cert)], check=True, capture_output=True)
        os.chmod(key, 0o600)
        (work / "index.html").write_text("<!doctype html><title>private options fixture</title>")
        (work / "assets").mkdir()
        (work / "assets" / "probe.js").write_text("// protected asset\n")
        configurations = []
        for filename, port, old in (("nginx.conf.template", ports[0], "listen 8080;"),
                                     ("nginx.options.conf.template", ports[1], "listen 8443 ssl;")):
            text = (root / "frontend" / filename).read_text()
            text = text.replace(old, f"listen 127.0.0.1:{port}" + (" ssl;" if "ssl" in old else ";"))
            text = text.replace("/usr/share/nginx/html", str(work)).replace("/run/options", str(work))
            text = text.replace("http://platform:5051", f"http://127.0.0.1:{upstream.server_port}")
            text = text.replace("${BACKEND_API_TOKEN}", token)
            configurations.append(text)
        config = work / "nginx.conf"
        config.write_text(f"pid {work}/nginx.pid;\nerror_log {work}/error.log;\nevents {{}}\nhttp {{ access_log off;\n" + "\n".join(configurations) + "\n}\n")
        subprocess.run([nginx, "-p", str(work) + "/", "-c", str(config), "-t"], check=True, capture_output=True)
        process = subprocess.Popen([nginx, "-p", str(work) + "/", "-c", str(config), "-g", "daemon off;"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        context = ssl.create_default_context(cafile=str(cert))

        def get(url, auth=False):
            headers = {}
            if auth:
                # A stale browser Basic header must not become upstream auth.
                headers["Authorization"] = "Basic dGVzdDp0ZXN0"
            req = urllib.request.Request(url, headers=headers)
            try:
                with urllib.request.urlopen(req, context=context, timeout=2) as response:
                    return response.status, response.read()
            except urllib.error.HTTPError as error:
                return error.code, error.read()

        try:
            public = f"http://127.0.0.1:{ports[0]}"
            private = f"https://127.0.0.1:{ports[1]}"
            for attempt in range(40):
                try:
                    if get(public + "/healthz")[0] == 200:
                        break
                except urllib.error.URLError:
                    if process.poll() is not None:
                        raise RuntimeError("test nginx exited")
                    time.sleep(.05)
            checks = [
                (public + "/options", False, 404),
                (public + "/options/", False, 404),
                (public + "/api/platform/options/status", False, 404),
                (private + "/options", False, 200),
                (private + "/assets/probe.js", False, 200),
                (private + "/api/platform/options/status", False, 200),
                (private + "/options", True, 200),
                (private + "/options/", True, 200),
                (private + "/assets/probe.js", True, 200),
                (private + "/api/platform/options/status", True, 200),
                (private + "/api/platform/ledger/accounts", True, 404),
                (private + "/index.html", True, 404),
            ]
            for url, auth, expected in checks:
                actual, _ = get(url, auth)
                assert actual == expected, (url, expected, actual)
            assert seen == [("/api/platform/options/status", True)] * 2, seen
            print(f"PASS: {len(checks)} no-login HTTPS/public-route checks; internal bearer injection verified")
        finally:
            process.terminate()
            process.wait(timeout=10)
            upstream.shutdown()


if __name__ == "__main__":
    main()
