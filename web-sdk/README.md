# DsxID for the web

Add **Sign in with DsxID** to a website. The browser never sees the private key.
A tiny local bridge asks the unlocked DsxID desktop app to sign a one-time
challenge, and your website backend verifies the signature before creating a
session.

The production browser SDK is hosted at:

```
https://dsxid.org/sdk/dsxid.js
https://dsxid.org/sdk/dsxid.css
```

## Files

- `dsxid.js` - the obfuscated browser SDK used on websites,
- `src/dsxid.js` - the readable browser SDK source,
- `build.ps1` - rebuilds `dsxid.js` from the source,
- `dsxid.css` - optional default button style,
- `dsxid-verify.js` - server-side Ed25519 verification helper for Node,
- `example/server.js` - a complete minimal website,
- `example/style.css` - example page styles.

## Browser SDK build

Browsers must execute JavaScript, so browser code cannot be truly encrypted:
the key would have to be shipped with the code. Instead, `dsxid.js` is an
obfuscated production build of `src/dsxid.js`. This makes the code harder to
read and copy, but it is not a security boundary. Keep verification and all
secrets on the server.

Rebuild the browser SDK after changing the source:

```powershell
powershell -ExecutionPolicy Bypass -File web-sdk/build.ps1
```

The script uses `javascript-obfuscator` 4.1.1 through `npx` and writes the
obfuscated result back to `dsxid.js`. The server-side `dsxid-verify.js` remains
readable on purpose; it runs on your server and should stay auditable.

## How it works

```
browser                         website backend              DsxID desktop app
───────                         ───────────────              ─────────────────
1. click Sign in with DsxID  →  POST /api/dsxid/start
                                  create one-time nonce
2.                          ←    { challenge }
3. fetch 127.0.0.1:41991    ─────────────────────────────→   show approval dialog
   /v1/sign { challenge }                                    user clicks Approve
4.                          ←─────────────────────────────    signed assertion
5. POST /api/dsxid/verify    →   verify signature
                                  check origin + challenge
                                  create session cookie
6.                          ←    { ok: true }
```

The website chooses the challenge; DsxID signs the browser's actual `Origin` and
that challenge. The website's backend verifies both. The assertion is short-lived
and the challenge must be single-use.

## Quick start

The desktop app must be running and your identity unlocked. Its website bridge
listens on `127.0.0.1:41991`.

```powershell
node web-sdk/example/server.js
```

To load the browser SDK from the hosted copy instead of the local example
server, set `DSXID_SDK_URL`:

```powershell
$env:DSXID_SDK_URL = "https://dsxid.org/sdk/dsxid.js"
node web-sdk/example/server.js
```

Open **http://127.0.0.1:3000**, click **Sign in with DsxID**, then approve the
request in the DsxID window. The example website will show your key fingerprint,
and the origin will appear under **Approved accounts** in DsxID.

The example has no dependencies; it uses only Node's built-in `http` module.

## Client integration

Load the hosted SDK (or copy `dsxid.js` and `dsxid.css` from
`https://dsxid.org/sdk/` to your own host), add a button, and mount it:

```html
<link rel="stylesheet" href="https://dsxid.org/sdk/dsxid.css">
<button id="dsxid-login" class="dsxid-button" type="button">
  Sign in with DsxID
</button>

<script src="https://dsxid.org/sdk/dsxid.js"></script>
<script>
  DsxID.mount("#dsxid-login", {
    startUrl: "/api/dsxid/start",
    verifyUrl: "/api/dsxid/verify",
    onSuccess: function () { location.reload(); },
    onError: function (err) {
      console.error(err.message);
    }
  });
</script>
```

Self-hosting works the same way; replace the two `https://dsxid.org/sdk/...`
URLs with paths on your domain.

You can also use data attributes:

```html
<button data-dsxid-login
        data-start-url="/api/dsxid/start"
        data-verify-url="/api/dsxid/verify"
        class="dsxid-button">Sign in with DsxID</button>
<script src="https://dsxid.org/sdk/dsxid.js"></script>
<script>DsxID.mountAll({ onSuccess: () => location.reload() });</script>
```

## E-shop autofill

Add a `profile` array with the field names your checkout needs. DsxID shows
those fields and their saved values in its approval dialog. After approval the
signed assertion contains only the available fields, and the backend returns
them to the browser:

```js
DsxID.mount("#checkout", {
  startUrl: "/api/dsxid/start",
  verifyUrl: "/api/dsxid/verify",
  profile: ["fullName", "email", "phone", "address1", "city", "postalCode", "country"],
  onSuccess: function (result) {
    for (const [key, value] of Object.entries(result.profile || {})) {
      const input = document.querySelector(`[name="${key}"]`);
      if (input) input.value = value;
    }
  }
});
```

Supported field names: `fullName`, `email`, `phone`, `address1`, `address2`,
`city`, `postalCode`, `country`, `company`, `vatId`. The website never receives
a field the user did not save and approve.

If your site uses a Content Security Policy, allow the local bridge:

```
connect-src 'self' http://127.0.0.1:41991;
```

## Backend contract

Your backend owns the session and the challenge.

### `POST /api/dsxid/start`

Create a random challenge (at least 16 characters, base64url recommended), store
it in the visitor's session with a short expiry (about 90 seconds), and return:

```json
{ "challenge": "dGhpcy1pcy1hLW9uZS10aW1lLWNoYWxsZW5nZQ" }
```

### `POST /api/dsxid/verify`

The SDK sends:

```json
{
  "challenge": "...",
  "assertion": "dsx1...",
  "publicKeyPem": "-----BEGIN PUBLIC KEY-----\n..."
}
```

Verify **on the server**:

- the stored challenge matches, has not expired, and is consumed once,
- the assertion audience equals the website origin,
- the client-supplied public key actually derives the `kid` in the assertion,
- the Ed25519 signature is valid,
- the time window has not expired.

Then create your normal session cookie and return
`{ "ok": true, "keyId": "...", "profile": { ... } }`. The `profile` object is
the verified, signed subset requested by the SDK; return it only after the
signature check passes.

Never trust a client-side "verified" flag. The SDK does not verify signatures.

### Node helper

`dsxid-verify.js` implements the checks above:

```js
const { verifyAssertion } = require("./dsxid-verify");

const result = verifyAssertion({
  assertion: body.assertion,
  publicKeyPem: body.publicKeyPem,
  expectedOrigin: pending.origin,
  expectedChallenge: pending.challenge,
});

// result.keyId is the stable identity fingerprint
```

`expectedOrigin` must be the exact browser origin, for example
`http://127.0.0.1:3000`. Do not trust a client-supplied origin.

## Security notes

- The SDK refuses to start a sign-in on an HTTP page outside localhost, and the
  bridge rejects HTTP origins outside loopback. Production sites must use HTTPS.
- The desktop app shows every request with the **actual website origin** and
  every requested profile field/value. Only approve requests you started.
- Profile values are part of the signed assertion, so a network attacker cannot
  alter them after approval.
- The assertion is bound to that origin and the one-time challenge. The Node
  verifier also rejects an HTTP `expectedOrigin` outside localhost.
- The bridge listens on IPv4 loopback only. It has CORS support, but every
  signature still requires explicit approval in the desktop app.
- The bridge has no authentication beyond the user's approval. Do not run an
  untrusted local app that occupies port `41991`.
- This is a local integration SDK. Browsers may block or prompt for requests
  from an HTTPS page to a local HTTP bridge (mixed content / Private Network
  Access). The example uses `http://127.0.0.1` for development. For production
  websites, a custom app link, a browser extension, or a hosted relay would be a
  better transport; the signature/verification format stays the same.
- Use HTTPS and `Secure`, `HttpOnly`, `SameSite` cookies for real sessions.

## Why the bridge is a separate server

A browser page cannot read the DsxID private key, and it should not. The bridge
is the only signing surface. It returns a signed assertion to the page, and the
page sends that assertion to its own backend. The backend remains the authority:
it checks the origin, challenge, key fingerprint, signature, and time window.
