"use strict";

/*
 * Minimal DsxID integration example.
 * No npm dependencies: run with `node server.js`.
 */

const http = require("http");
const crypto = require("crypto");
const fs = require("fs");
const path = require("path");
const { verifyAssertion } = require("../dsxid-verify");

const PORT = Number(process.env.PORT || 3000);
const HOST = "127.0.0.1";
const SDK_URL = process.env.DSXID_SDK_URL || "/dsxid.js";
const sessions = new Map();

const exampleCSS = fs.readFileSync(path.join(__dirname, "style.css"), "utf8");
const sdkJS = fs.readFileSync(path.join(__dirname, "..", "dsxid.js"), "utf8");
const sdkCSS = fs.readFileSync(path.join(__dirname, "..", "dsxid.css"), "utf8");

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host || "localhost"}`);

  if (req.method === "GET" && url.pathname === "/style.css") {
    return text(res, 200, exampleCSS, "text/css; charset=utf-8");
  }
  if (req.method === "GET" && url.pathname === "/dsxid.js") {
    return text(res, 200, sdkJS, "text/javascript; charset=utf-8");
  }
  if (req.method === "GET" && url.pathname === "/dsxid.css") {
    return text(res, 200, sdkCSS, "text/css; charset=utf-8");
  }
  if (req.method === "GET" && url.pathname === "/") {
    const session = ensureSession(req, res);
    return text(res, 200, session.user ? dashboardPage(session.user) : loginPage());
  }
  if (req.method === "POST" && url.pathname === "/api/dsxid/start") {
    const session = ensureSession(req, res);
    let body;
    try {
      body = await readJSON(req);
    } catch (err) {
      return json(res, 400, { error: "Invalid JSON request." });
    }
    const origin = normalizeOrigin(req.headers.origin || `http://${req.headers.host || `${HOST}:${PORT}`}`);
    if (!origin) {
      return json(res, 400, { error: "A valid website origin is required." });
    }
    const challenge = crypto.randomBytes(32).toString("base64url");
    session.pending = {
      challenge,
      origin,
      expiresAt: Date.now() + 90_000,
      returnUrl: typeof body.returnUrl === "string" ? body.returnUrl : "/",
    };
    return json(res, 200, { challenge });
  }
  if (req.method === "POST" && url.pathname === "/api/dsxid/verify") {
    const session = ensureSession(req, res);
    let body;
    try {
      body = await readJSON(req);
    } catch (err) {
      return json(res, 400, { error: "Invalid JSON request." });
    }
    const pending = session.pending;
    session.pending = null;
    if (!pending || Date.now() > pending.expiresAt) {
      return json(res, 400, { error: "The sign-in challenge has expired. Try again." });
    }
    try {
      const result = verifyAssertion({
        assertion: body.assertion,
        publicKeyPem: body.publicKeyPem,
        expectedOrigin: pending.origin,
        expectedChallenge: pending.challenge,
      });
      session.user = {
        keyId: result.keyId,
        signedInAt: new Date().toISOString(),
        profile: result.profile,
      };
      return json(res, 200, { ok: true, keyId: result.keyId, profile: result.profile });
    } catch (err) {
      console.error("DsxID verification failed:", err.message);
      return json(res, 401, { error: err.message });
    }
  }
  if (req.method === "POST" && url.pathname === "/logout") {
    const session = ensureSession(req, res);
    session.user = null;
    session.pending = null;
    res.writeHead(303, { Location: "/", "Cache-Control": "no-store" });
    return res.end();
  }
  return json(res, 404, { error: "Not found." });
});

server.listen(PORT, HOST, () => {
  console.log(`Example site: http://127.0.0.1:${PORT}`);
  console.log("Start DsxID, unlock your identity, then click Sign in with DsxID.");
});

function ensureSession(req, res) {
  const cookies = parseCookies(req.headers.cookie || "");
  const existingID = cookies.example_sid;
  if (existingID && sessions.has(existingID)) {
    return sessions.get(existingID);
  }
  const sessionID = crypto.randomBytes(24).toString("hex");
  const session = { createdAt: Date.now(), pending: null, user: null };
  sessions.set(sessionID, session);
  const secure = Boolean(req.socket.encrypted) || String(req.headers["x-forwarded-proto"] || "").toLowerCase() === "https";
  addSetCookie(res, `example_sid=${sessionID}; HttpOnly; SameSite=Lax; Path=/; Max-Age=3600${secure ? "; Secure" : ""}`);
  return session;
}

function parseCookies(header) {
  const out = {};
  for (const part of header.split(";")) {
    const index = part.indexOf("=");
    if (index < 0) continue;
    out[part.slice(0, index).trim()] = decodeURIComponent(part.slice(index + 1).trim());
  }
  return out;
}

function addSetCookie(res, cookie) {
  const existing = res.getHeader("Set-Cookie");
  const values = existing ? (Array.isArray(existing) ? existing : [existing]) : [];
  values.push(cookie);
  res.setHeader("Set-Cookie", values);
}

function isLoopbackHost(host) {
  return host === "localhost" || host === "127.0.0.1" || host === "[::1]";
}

function normalizeOrigin(value) {
  if (typeof value !== "string") return null;
  const trimmed = value.replace(/\/+$/, "");
  if (!/^https?:\/\/[^/\s]+$/i.test(trimmed)) return null;
  let parsed;
  try {
    parsed = new URL(trimmed);
  } catch {
    return null;
  }
  if (parsed.protocol !== "https:" && !(parsed.protocol === "http:" && isLoopbackHost(parsed.hostname))) {
    return null;
  }
  return trimmed;
}

function readJSON(req) {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks = [];
    req.on("data", (chunk) => {
      size += chunk.length;
      if (size > 64 * 1024) {
        reject(new Error("Body too large"));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on("end", () => {
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}"));
      } catch (err) {
        reject(err);
      }
    });
    req.on("error", reject);
  });
}

function text(res, status, body, type = "text/html; charset=utf-8") {
  res.writeHead(status, {
    "Content-Type": type,
    "Cache-Control": "no-store",
    "X-Content-Type-Options": "nosniff",
    "Referrer-Policy": "no-referrer",
  });
  res.end(body);
}

function json(res, status, value) {
  text(res, status, JSON.stringify(value), "application/json; charset=utf-8");
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  })[char]);
}

function layout(title, content, scripts = "") {
  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${escapeHTML(title)}</title>
  <link rel="stylesheet" href="/dsxid.css">
  <link rel="stylesheet" href="/style.css">
</head>
<body>
  <main class="site-shell">
    ${content}
  </main>
  <footer class="site-footer">Example website · powered by DsxID</footer>
  ${scripts}
</body>
</html>`;
}

function loginPage() {
  return layout(
    "Sign in · Example",
    `<section class="card">
      <div class="site-mark" aria-hidden="true">E</div>
      <p class="eyebrow">Example application</p>
      <h1>Sign in with your key.</h1>
      <p class="lede">This page uses the DsxID JavaScript SDK. No password is sent to this website.</p>
      <div id="login-error" class="error" role="alert" hidden></div>
      <button class="dsxid-button" type="button" id="dsxid-login">Sign in with DsxID</button>
      <p class="hint">DsxID must be open and unlocked on this computer.</p>
      <ol class="steps">
        <li><span>1</span>Your backend creates a one-time challenge.</li>
        <li><span>2</span>DsxID signs it after you approve.</li>
        <li><span>3</span>This backend verifies the signature.</li>
      </ol>
    </section>`,
    `<script src="${escapeHTML(SDK_URL)}"></script>
    <script>
      DsxID.mount("#dsxid-login", {
        startUrl: "/api/dsxid/start",
        verifyUrl: "/api/dsxid/verify",
        profile: ["fullName", "email", "phone", "company", "address1", "address2", "city", "postalCode", "country"],
        onSuccess: function () { location.reload(); },
        onError: function (err) {
          var box = document.getElementById("login-error");
          box.textContent = err.message;
          box.hidden = false;
        }
      });
    </script>`
  );
}

function dashboardPage(user) {
  return layout(
    "Account · Example",
    `<section class="card">
      <div class="welcome">
        <div class="site-mark" aria-hidden="true">E</div>
        <div>
          <p class="eyebrow">Signed in with DsxID</p>
          <h1>Welcome back.</h1>
        </div>
      </div>
      <p class="lede">This website verified your Ed25519 signature. Your private key never left DsxID.</p>
      <dl class="details">
        <div><dt>Identity</dt><dd><code>${escapeHTML(user.keyId)}</code></dd></div>
        <div><dt>Signed in</dt><dd>${escapeHTML(new Date(user.signedInAt).toLocaleString("en-GB"))}</dd></div>
        <div><dt>Method</dt><dd>Ed25519</dd></div>
      </dl>
      <form method="post" action="/logout">
        <button class="secondary" type="submit">Sign out</button>
      </form>
    </section>
    ${checkoutCard(user.profile)}`
  );
}

function checkoutCard(profile) {
  const values = profile || {};
  const input = (label, key, type = "text", wide = false) => `
    <label class="${wide ? "wide" : ""}">
      <span>${escapeHTML(label)}</span>
      <input type="${type}" name="${key}" value="${escapeHTML(values[key] || "")}">
    </label>`;
  return `<section class="card checkout-card">
    <p class="eyebrow">E-shop checkout</p>
    <h2>Delivery details</h2>
    <p class="lede">These fields were prefilled from the DsxID profile you approved. Edit them as needed.</p>
    <div class="checkout-grid">
      ${input("Full name", "fullName")}
      ${input("Email", "email", "email")}
      ${input("Phone", "phone", "tel")}
      ${input("Company", "company")}
      ${input("Address line 1", "address1", "text", true)}
      ${input("Address line 2", "address2", "text", true)}
      ${input("City", "city")}
      ${input("Postal code", "postalCode")}
      ${input("Country", "country")}
    </div>
    <button class="secondary" type="button">Continue (demo)</button>
  </section>`;
}
