import type { IncomingMessage, ServerResponse } from "node:http";
import type { Plugin, ProxyOptions } from "vite";

const prefix = "/api/platform/ledger";
const localHost = /^(localhost|127\.0\.0\.1|\[::1\])(?::\d+)?$/;
const localPeers = new Set(["127.0.0.1", "::1", "::ffff:127.0.0.1"]);

export function ledgerBoundary(enabled: boolean) {
  return (req: IncomingMessage, res: ServerResponse, next: () => void) => {
    const path = req.url?.split("?")[0] ?? "";
    if (path !== prefix && !path.startsWith(`${prefix}/`)) return next();
    let code = "";
    let status = 403;
    if (!enabled) {
      code = "local_proxy_disabled";
      status = 404;
    } else if (
      !localPeers.has(req.socket.remoteAddress ?? "") ||
      !localHost.test(req.headers.host ?? "")
    ) {
      code = "local_only";
    } else if (
      (req.headers.origin &&
        req.headers.origin !== `http://${req.headers.host}`) ||
      (req.headers["sec-fetch-site"] &&
        !["same-origin", "none"].includes(
          String(req.headers["sec-fetch-site"]),
        ))
    ) {
      code = "cross_origin";
    }
    if (!code) return next();
    res.writeHead(status, {
      "Content-Type": "application/json",
      "Cache-Control": "no-store",
      "X-Content-Type-Options": "nosniff",
    });
    res.end(
      JSON.stringify({ code, message: "ledger development proxy denied" }),
    );
  };
}

// Keep Vite development local; production public access is provided by Nginx.
export function ledgerDevelopment(env: NodeJS.ProcessEnv): {
  plugin: Plugin;
  proxy: Record<string, ProxyOptions>;
} {
  const enabled = env.LEDGER_DEV_PROXY === "true";
  const target = env.BACKEND_PROXY_URL || "http://127.0.0.1:5051";
  if (enabled) {
    const url = new URL(target);
    if (
      url.protocol !== "http:" ||
      !localHost.test(url.host) ||
      url.username ||
      url.password ||
      url.pathname !== "/" ||
      url.search ||
      url.hash
    )
      throw new Error(
        "LEDGER_DEV_PROXY requires a loopback HTTP backend origin",
      );
  }
  return {
    plugin: {
      name: "local-ledger-boundary",
      apply: "serve",
      configureServer(server) {
        server.middlewares.use(ledgerBoundary(enabled));
      },
      configurePreviewServer(server) {
        // Vite preview inherits server.proxy; never inherit ledger access.
        server.middlewares.use(ledgerBoundary(false));
      },
    },
    proxy: enabled
      ? {
          "^/api/platform/ledger(?:/|\\?|$)": {
            target,
            changeOrigin: false,
            headers: { Authorization: "" },
          },
        }
      : {},
  };
}
