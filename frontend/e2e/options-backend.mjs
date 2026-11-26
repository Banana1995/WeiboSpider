import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawn, execFileSync } from "node:child_process";
import { createServer } from "node:http";

const directory = mkdtempSync(
  join(process.env.OPTIONS_E2E_ROOT || tmpdir(), "options-e2e-"),
);
const binary = join(directory, "platform");
execFileSync("go", ["build", "-o", binary, "./cmd/server"], {
  cwd: resolve("../backend"),
  stdio: "inherit",
});
const env = {
  ...process.env,
  BACKEND_ADDR: "127.0.0.1:17051",
  BACKEND_DATA_DIR: directory,
  BACKEND_API_TOKEN: "",
  LIQUOR_AUTO_SYNC: "false",
  LEDGER_ENABLED: "false",
  LEDGER_MCP_ENABLED: "false",
  LEDGER_WEEKLY_ENABLED: "false",
  OPTIONS_ENABLED: "true",
  OPTIONS_QUOTES_URL: "http://127.0.0.1:17053",
};
let stopping = false;
const start = () => spawn(binary, [], { env, stdio: "inherit" });
let child = start();
// Test-only process controller. Production binary has no reset/restart endpoint.
const control = createServer((req, res) => {
  if (req.url === "/setup" && req.method === "POST") {
    // Loopback-only test controller, operating exclusively on its mkdtemp data dir.
    const link = execFileSync(binary, ["recover", "gaomanyi"], {
      env,
      encoding: "utf8",
    }).trim();
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ link }));
    return;
  }
  if (req.url !== "/restart" || req.method !== "POST") {
    res.writeHead(404);
    res.end();
    return;
  }
  const previous = child;
  previous.once("exit", () => {
    if (!stopping) child = start();
    res.writeHead(200);
    res.end("restarted");
  });
  previous.kill("SIGTERM");
}).listen(17052, "127.0.0.1");
function stop() {
  if (stopping) return;
  stopping = true;
  control.close();
  child.once("exit", () => {
    rmSync(directory, { recursive: true, force: true });
    process.exit(0);
  });
  child.kill("SIGTERM");
}
process.on("SIGTERM", stop);
process.on("SIGINT", stop);
