/*
 * Server-side Ed25519 verification for DsxID assertions.
 *
 * Never trust a browser that says the sign-in succeeded. Verify the assertion
 * on your backend, bind it to the challenge your backend issued, and only then
 * create a session cookie.
 *
 * Copyright (c) 2026 Damián Sýkora. MIT License.
 */
"use strict";

const crypto = require("crypto");

const DOMAIN = "dsxid-auth-v1";
const MAX_ASSERTION_TTL_SECONDS = 120;
const MAX_CLOCK_SKEW_MS = 30_000;

function isLoopbackHost(host) {
  return host === "localhost" || host === "127.0.0.1" || host === "::1" || host === "[::1]";
}

function requireSecureOrigin(origin) {
  let parsed;
  try {
    parsed = new URL(origin);
  } catch {
    throw new Error("The expected origin is not a valid URL.");
  }
  if (parsed.protocol === "https:") return;
  if (parsed.protocol === "http:" && isLoopbackHost(parsed.hostname)) return;
  throw new Error("DsxID requires an HTTPS origin; HTTP is allowed only for localhost.");
}

function deriveKeyID(publicKeyPem) {
  const key = crypto.createPublicKey(publicKeyPem);
  if (key.asymmetricKeyType !== "ed25519") {
    throw new Error("The public key is not an Ed25519 key.");
  }
  const der = key.export({ format: "der", type: "spki" });
  const raw = der.subarray(der.length - 32);
  if (raw.length !== 32) throw new Error("Invalid Ed25519 public key.");
  const digest = crypto.createHash("sha256").update(raw).digest().subarray(0, 10);
  const base32 = base32Encode(digest);
  return `DSX-${base32.slice(0, 4)}-${base32.slice(4, 8)}-${base32.slice(8, 12)}-${base32.slice(12, 16)}`;
}

function verifyAssertion({ assertion, publicKeyPem, expectedOrigin, expectedChallenge, now = Date.now() }) {
  if (typeof assertion !== "string" || typeof publicKeyPem !== "string") {
    throw new Error("Missing assertion or public key.");
  }
  if (!expectedOrigin || !expectedChallenge) {
    throw new Error("Missing expected origin or challenge.");
  }
  const origin = expectedOrigin.replace(/\/+$/, "");
  requireSecureOrigin(origin);
  const parts = assertion.split(".");
  if (parts.length !== 3 || parts[0] !== "dsx1") {
    throw new Error("Invalid assertion format.");
  }
  const payload = Buffer.from(parts[1], "base64url");
  const signature = Buffer.from(parts[2], "base64url");
  if (payload.length === 0 || payload.length > 4096) throw new Error("Invalid assertion payload.");
  if (signature.length !== 64) throw new Error("Invalid assertion signature.");

  let statement;
  try {
    statement = JSON.parse(payload.toString("utf8"));
  } catch {
    throw new Error("Invalid assertion JSON.");
  }
  if (statement.v !== 1 || statement.typ !== "auth") {
    throw new Error("Unsupported assertion version or type.");
  }
  if (statement.profile !== undefined) {
    if (!statement.profile || typeof statement.profile !== "object" || Array.isArray(statement.profile)) {
      throw new Error("Invalid profile data.");
    }
    const entries = Object.entries(statement.profile);
    if (entries.length > 10) throw new Error("Too many profile fields.");
    for (const [key, value] of entries) {
      if (!/^[A-Za-z0-9_]{1,32}$/.test(key)) throw new Error("Invalid profile field name.");
      if (typeof value !== "string" || value.length > 512 || /[\u0000\r\n]/.test(value)) {
        throw new Error("Invalid profile value.");
      }
    }
  }
  if (statement.aud !== origin) {
    throw new Error("The assertion is for a different website.");
  }
  if (statement.nonce !== expectedChallenge) {
    throw new Error("The challenge does not match.");
  }
  const issuedAt = Number(statement.iat) * 1000;
  const expiresAt = Number(statement.exp) * 1000;
  if (!Number.isFinite(issuedAt) || !Number.isFinite(expiresAt) || expiresAt <= issuedAt) {
    throw new Error("Invalid assertion time window.");
  }
  if (expiresAt - issuedAt > MAX_ASSERTION_TTL_SECONDS * 1000) {
    throw new Error("Assertion lifetime is too long.");
  }
  if (now > expiresAt) throw new Error("The assertion has expired.");
  if (issuedAt > now + MAX_CLOCK_SKEW_MS) throw new Error("The assertion is not valid yet.");

  const key = crypto.createPublicKey(publicKeyPem);
  if (deriveKeyID(publicKeyPem) !== statement.kid) {
    throw new Error("The assertion fingerprint does not match the public key.");
  }
  const signedData = Buffer.concat([Buffer.from(DOMAIN + "."), payload]);
  if (!crypto.verify(null, signedData, key, signature)) {
    throw new Error("The signature is invalid.");
  }
  return { statement, keyId: statement.kid, profile: statement.profile || {} };
}

function base32Encode(buffer) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = 0;
  let value = 0;
  let output = "";
  for (const byte of buffer) {
    value = (value << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      output += alphabet[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  if (bits > 0) output += alphabet[(value << (5 - bits)) & 31];
  return output;
}

module.exports = { deriveKeyID, verifyAssertion };
