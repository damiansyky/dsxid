/*
 * DsxID browser SDK
 * -----------------
 * Adds "Sign in with DsxID" to any website. The website backend owns the
 * challenge and verifies the assertion; this script only orchestrates the
 * browser flow and talks to the local DsxID bridge after user approval.
 *
 * Copyright (c) 2026 Damián Sýkora. MIT License.
 */
(function (global) {
  "use strict";

  var DEFAULT_BRIDGE = "http://127.0.0.1:41991";

  function isLocalHost(host) {
    return host === "localhost" || host === "127.0.0.1" || host === "::1" || host === "[::1]";
  }

  function requireSecureContext() {
    if (global.location && global.location.protocol === "https:") return;
    if (global.location && isLocalHost(global.location.hostname)) return;
    throw new Error("DsxID requires HTTPS. Serve this website over HTTPS; HTTP is allowed only for localhost development.");
  }

  function readJSON(response) {
    return response.text().then(function (text) {
      var data = {};
      if (text) {
        try {
          data = JSON.parse(text);
        } catch (err) {
          throw new Error("The server returned invalid JSON.");
        }
      }
      if (!response.ok) {
        var error = new Error(data.error || "Request failed (" + response.status + ").");
        error.status = response.status;
        throw error;
      }
      return data;
    });
  }

  function setBusy(button, busy) {
    if (!button) return;
    button.disabled = busy;
    button.setAttribute("aria-busy", busy ? "true" : "false");
    button.classList.toggle("dsxid-busy", busy);
  }

  function getButton(target) {
    if (!target) return null;
    return typeof target === "string" ? document.querySelector(target) : target;
  }

  /**
   * Run the full sign-in flow.
   *
   * Options:
   *   startUrl   same-origin endpoint that returns { challenge }
   *   verifyUrl  same-origin endpoint that verifies the assertion
   *   bridgeUrl  optional, defaults to http://127.0.0.1:41991
   *   button     optional button element or selector
   *   onSuccess  optional callback with the server response
   */
  function signIn(options) {
    try {
      requireSecureContext();
    } catch (err) {
      return Promise.reject(err);
    }
    var opts = options || {};
    if (!opts.startUrl || !opts.verifyUrl) {
      return Promise.reject(new Error("DsxID: startUrl and verifyUrl are required."));
    }
    var profile = opts.profile;
    if (profile !== undefined) {
      if (!Array.isArray(profile) || profile.length > 10) {
        return Promise.reject(new Error("DsxID: profile must be an array of at most 10 field names."));
      }
      for (var i = 0; i < profile.length; i++) {
        if (typeof profile[i] !== "string" || !/^[A-Za-z0-9_]{1,32}$/.test(profile[i])) {
          return Promise.reject(new Error("DsxID: invalid profile field name."));
        }
      }
    }
    var bridge = (opts.bridgeUrl || DEFAULT_BRIDGE).replace(/\/+$/, "");
    var button = getButton(opts.button);
    setBusy(button, true);

    return fetch(opts.startUrl, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ returnUrl: global.location.href })
    })
      .then(readJSON)
      .then(function (start) {
        if (!start || typeof start.challenge !== "string" || start.challenge.length < 16) {
          throw new Error("The website did not return a valid challenge.");
        }
        return fetch(bridge + "/v1/sign", {
          method: "POST",
          mode: "cors",
          credentials: "omit",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ challenge: start.challenge, profile: profile })
        })
          .catch(function () {
            throw new Error("DsxID is not running or the local bridge is blocked. Start DsxID, unlock your identity, and try again.");
          })
          .then(readJSON)
          .catch(function (err) {
            if (err && err.status === 423) {
              throw new Error("DsxID is locked. Open DsxID, unlock your identity, and try again.");
            }
            throw err;
          })
          .then(function (signed) {
            if (!signed || !signed.assertion || !signed.publicKeyPem) {
              throw new Error("DsxID returned an invalid signature.");
            }
            return fetch(opts.verifyUrl, {
              method: "POST",
              credentials: "same-origin",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({
                challenge: start.challenge,
                assertion: signed.assertion,
                publicKeyPem: signed.publicKeyPem
              })
            });
          })
          .then(readJSON)
          .then(function (verified) {
            if (!verified || verified.ok !== true) {
              throw new Error("The website rejected the signature.");
            }
            if (opts.onSuccess) return opts.onSuccess(verified);
            return verified;
          });
      })
      .finally(function () {
        setBusy(button, false);
      });
  }

  /**
   * Attach the SDK to a button.
   * Returns the button so the caller can keep a reference.
   */
  function mount(target, options) {
    var button = getButton(target);
    if (!button) throw new Error("DsxID: button not found.");
    if (button.getAttribute("data-dsxid-mounted") === "1") return button;
    button.setAttribute("data-dsxid-mounted", "1");
    button.addEventListener("click", function (event) {
      event.preventDefault();
      if (button.disabled) return;
      signIn(Object.assign({}, options || {}, { button: button })).catch(function (err) {
        if (options && options.onError) options.onError(err);
        else console.error("[DsxID]", err);
      });
    });
    return button;
  }

  /**
   * Mount every element with data-dsxid-login. Endpoint attributes override
   * the defaults passed here.
   */
  function mountAll(options) {
    var defaults = options || {};
    Array.prototype.forEach.call(
      document.querySelectorAll("[data-dsxid-login]"),
      function (button) {
        mount(button, {
          startUrl: button.getAttribute("data-start-url") || defaults.startUrl,
          verifyUrl: button.getAttribute("data-verify-url") || defaults.verifyUrl,
          bridgeUrl: defaults.bridgeUrl,
          profile: defaults.profile,
          onSuccess: defaults.onSuccess,
          onError: defaults.onError
        });
      }
    );
  }

  global.DsxID = {
    signIn: signIn,
    mount: mount,
    mountAll: mountAll,
    bridgeUrl: DEFAULT_BRIDGE
  };
})(window);
