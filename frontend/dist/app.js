/* DsxID desktop UI — plain JS, no framework. */

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));

const splash = $("#splash");
const fatal = $("#fatal");
const toastRoot = $("#toasts");
const splashStarted = performance.now();
const splashMinMs = window.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches ? 250 : 950;
let splashHidden = false;

let api = null;
let state = null;
let currentScreen = null;
let currentPage = "overview";
let activeApproval = null;
let approvalBusy = false;
let approvalTimer = null;
const seenMessages = new Set();

// Set this to the public download/update page when it is available.
// While empty, the Update link opens a local information dialog.
const UPDATE_URL = "";

const dateFmt = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short", year: "numeric" });
const timeFmt = new Intl.DateTimeFormat("en-GB", { hour: "2-digit", minute: "2-digit" });

const PAGE_META = {
  overview: {
    title: "Overview",
    subtitle: "Your identity and sign-in status.",
  },
  profile: {
    title: "Profile",
    subtitle: "Contact and delivery details for e-shops.",
  },
  accounts: {
    title: "Approved accounts",
    subtitle: "Websites that received a signature from this identity.",
  },
  settings: {
    title: "Settings",
    subtitle: "Security, backups, and application data.",
  },
};

boot();

async function boot() {
  bindStaticHandlers();
  try {
    api = await getApi();
    if (!api) {
      hideSplash();
      fatal.hidden = false;
      return;
    }
    const initial = await api.GetState();
    applyState(initial);
    window.runtime?.EventsOn?.("dsxid:state", refreshState);
    startApprovalPolling();
    const preview = new URLSearchParams(location.search).get("preview");
    if (initial.unlocked && ["profile", "settings", "accounts"].includes(preview)) {
      showPage(preview);
      if (preview === "profile") loadProfile();
    }
  } catch (err) {
    console.error(err);
    hideSplash();
    fatal.hidden = false;
    $("#fatal-detail").textContent = errorText(err);
  }
}

async function getApi() {
  if (new URLSearchParams(location.search).has("preview")) return createMockApi();
  for (let i = 0; i < 100; i++) {
    const wailsApp = window.go?.main?.App || window.go?.app?.App;
    if (wailsApp) return wailsApp;
    await sleep(40);
  }
  return null;
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function bindStaticHandlers() {
  $("#create-form").addEventListener("submit", onCreate);
  $("#unlock-form").addEventListener("submit", onUnlock);
  $("#restore-btn").addEventListener("click", (e) => restoreBackup(e.currentTarget));
  $("#lock-btn").addEventListener("click", onLock);
  $("#backup-btn").addEventListener("click", (e) => backup(e.currentTarget));
  $("#settings-backup-btn").addEventListener("click", (e) => backup(e.currentTarget));
  $("#copy-key-btn").addEventListener("click", (e) => copyPublicKey(e.currentTarget));
  $("#copy-id-btn").addEventListener("click", (e) => copyFingerprint(e.currentTarget));
  $("#profile-form").addEventListener("submit", onSaveProfile);
  $("#settings-change-form").addEventListener("submit", onChangePassphrase);
  $("#settings-delete-form").addEventListener("submit", onDeleteIdentity);
  $("#approval-approve").addEventListener("click", () => resolveApproval(true));
  $("#approval-deny").addEventListener("click", () => resolveApproval(false));
  $$("[data-update]").forEach((btn) => btn.addEventListener("click", openUpdate));
  $("#update-close").addEventListener("click", closeUpdate);
  $("#update-root").addEventListener("click", (event) => {
    if (event.target.classList.contains("approval-backdrop")) closeUpdate();
  });

  $$(".nav-item").forEach((btn) => {
    btn.addEventListener("click", () => {
      showPage(btn.dataset.page);
      if (btn.dataset.page === "profile") loadProfile();
    });
  });

  $$("[data-reveal]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const input = document.getElementById(btn.dataset.reveal);
      if (!input) return;
      const show = input.type === "password";
      input.type = show ? "text" : "password";
      btn.classList.toggle("is-revealed", show);
      btn.setAttribute("aria-label", show ? "Hide passphrase" : "Show passphrase");
    });
  });

  $("#create-pass").addEventListener("input", updateStrength);
  for (const input of $$("#create-form input, #unlock-form input")) {
    input.addEventListener("input", () => {
      input.removeAttribute("aria-invalid");
      const error = input.closest("form")?.querySelector(".form-error");
      if (error) error.textContent = "";
    });
  }
  for (const input of $$("#settings-change-form input")) {
    input.addEventListener("input", () => {
      $("#settings-change-error").textContent = "";
      $("#settings-change-success").textContent = "";
    });
  }
  for (const input of $$("#settings-delete-form input")) {
    input.addEventListener("input", () => {
      $("#settings-delete-error").textContent = "";
    });
  }
  for (const input of $$("#profile-form input")) {
    input.addEventListener("input", () => {
      $("#profile-error").textContent = "";
      $("#profile-success").textContent = "";
    });
  }

  const refreshOnShow = () => {
    if (!document.hidden) refreshState();
  };
  document.addEventListener("visibilitychange", refreshOnShow);
  window.addEventListener("focus", refreshOnShow);

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape") return;
    if (!$("#update-root").hidden) {
      closeUpdate();
      return;
    }
    if (activeApproval && !approvalBusy) resolveApproval(false);
  });
  window.addEventListener("error", (event) => console.error(event.error || event.message));
}

/* ------------------------------------------------------------------ */
/* State and screens                                                   */
/* ------------------------------------------------------------------ */

function applyState(next) {
  if (!next) return;
  const wasMain = currentScreen === "main";
  state = next;
  if (!state.unlocked) clearProfileForm();

  const version = state.version || "0.1.0";
  for (const id of ["setup-version", "unlock-version", "main-version"]) {
    const el = document.getElementById(id);
    if (el) el.textContent = version;
  }


  if (!state.hasIdentity) {
    populateSetup();
    showScreen("setup");
    currentPage = "overview";
  } else if (!state.unlocked) {
    populateUnlock();
    showScreen("unlock");
  } else {
    populateMain();
    showScreen("main");
    if (!wasMain) {
      currentPage = "overview";
      resetSettingsForms();
    }
    showPage(currentPage);
  }

  if (state.notice && !seenMessages.has(state.notice)) {
    seenMessages.add(state.notice);
    setTimeout(() => toast(state.notice, "error"), 600);
  }
  hideSplash();
}

function showScreen(name) {
  const screens = {
    setup: $("#screen-setup"),
    unlock: $("#screen-unlock"),
    main: $("#screen-main"),
  };
  const target = screens[name];
  if (!target) return;
  if (currentScreen !== name) {
    for (const screen of Object.values(screens)) screen.hidden = screen !== target;
    target.hidden = false;
    currentScreen = name;
    if (name === "unlock") setTimeout(() => $("#unlock-pass")?.focus(), 100);
    if (name === "setup") setTimeout(() => $("#create-name")?.focus(), 100);
  }
}

function showPage(name) {
  if (!PAGE_META[name]) name = "overview";
  currentPage = name;
  for (const page of $$(".page")) page.hidden = page.id !== `page-${name}`;
  for (const item of $$(".nav-item")) {
    const active = item.dataset.page === name;
    item.classList.toggle("is-active", active);
    item.setAttribute("aria-current", active ? "page" : "false");
  }
  $("#page-title").textContent = PAGE_META[name].title;
  $("#page-subtitle").textContent = PAGE_META[name].subtitle;
  $("#content-scroll").scrollTop = 0;
}

function populateSetup() {
  $("#create-name").value = "";
  $("#create-pass").value = "";
  $("#create-pass2").value = "";
  $("#create-terms").checked = false;
  $("#create-error").textContent = "";
  $("#create-submit").disabled = false;
  $("#restore-btn").disabled = false;
  updateStrength();
}

function populateUnlock() {
  const identity = state.identity;
  $("#locked-name").textContent = identity?.label || "Unreadable identity";
  $("#locked-id").textContent = identity?.id || "DSX-••••-••••-••••-••••";
  $("#unlock-pass").value = "";
  $("#unlock-error").textContent = "";
  const notice = state.notice || (!identity ? "The identity file cannot be read. Try restoring a backup or check the data folder." : "");
  $("#unlock-notice").hidden = !notice;
  $("#unlock-notice-text").textContent = notice || "";
}

function populateMain() {
  const identity = state.identity;
  if (!identity) return;
  const label = identity.label || "My identity";

  $("#sidebar-label").textContent = label;
  $("#identity-label").textContent = label;
  $("#identity-label").title = label;
  $("#identity-id").textContent = identity.id;
  $("#identity-created").textContent = formatDate(identity.createdAt);
  $("#identity-pubkey").textContent = shortenKey(identity.publicKeyFull || "");
  $("#identity-pubkey").title = identity.publicKeyFull || "";

  $("#settings-data-path").textContent = state.dataDir || "-";

  renderAccounts();
}

async function loadProfile() {
  if (!api?.GetProfile) return;
  const errBox = $("#profile-error");
  const okBox = $("#profile-success");
  errBox.textContent = "";
  okBox.textContent = "";
  try {
    const values = await api.GetProfile();
    for (const input of $$("[data-profile-field]")) {
      input.value = values?.[input.dataset.profileField] || "";
    }
  } catch (err) {
    errBox.textContent = errorText(err);
  }
}

function clearProfileForm() {
  for (const input of $$("[data-profile-field]")) input.value = "";
  $("#profile-error").textContent = "";
  $("#profile-success").textContent = "";
}

async function onSaveProfile(event) {
  event.preventDefault();
  const values = {};
  for (const input of $$("[data-profile-field]")) {
    const value = input.value.trim();
    if (value) values[input.dataset.profileField] = value;
  }
  const btn = $("#profile-save");
  const errBox = $("#profile-error");
  const okBox = $("#profile-success");
  errBox.textContent = "";
  okBox.textContent = "";
  setBusy(btn, true);
  try {
    await api.SaveProfile(values);
    okBox.textContent = "Profile saved locally.";
    toast("Profile saved locally.", "success");
  } catch (err) {
    errBox.textContent = errorText(err);
  } finally {
    setBusy(btn, false);
  }
}

function renderAccounts() {
  const accounts = Array.isArray(state?.approvedAccounts) ? state.approvedAccounts : [];
  const list = $("#accounts-list");
  const count = $("#accounts-count");
  count.textContent = `${accounts.length} approved`;

  if (!accounts.length) {
    list.innerHTML = `<div class="accounts-empty">
      <span class="empty-icon"><svg><use href="#i-list"/></svg></span>
      <h3>No approved accounts yet</h3>
      <p>When you sign in to a website with DsxID and approve the request, that website will appear here.</p>
    </div>`;
    return;
  }

  list.innerHTML = accounts.map((account) => {
    const origin = escapeHtml(account.origin || "");
    const last = escapeHtml(formatDateTime(account.lastApprovedAt));
    const signIns = Number(account.signIns) || 1;
    return `<div class="account-row">
      <span class="icon-tile"><svg><use href="#i-globe"/></svg></span>
      <div class="account-body">
        <span class="account-origin" title="${origin}">${origin}</span>
        <span class="account-meta">Last approved ${last} · ${signIns} sign-in${signIns === 1 ? "" : "s"}</span>
      </div>
      <span class="badge badge-success"><svg><use href="#i-check"/></svg>Approved</span>
    </div>`;
  }).join("");
}

async function refreshState() {
  if (!api?.GetState) return;
  try {
    applyState(await api.GetState());
  } catch {
    // The action already succeeded; a refresh failure is not fatal.
  }
}

/* ------------------------------------------------------------------ */
/* Setup and unlock                                                   */
/* ------------------------------------------------------------------ */

async function onCreate(event) {
  event.preventDefault();
  const btn = $("#create-submit");
  const name = $("#create-name").value;
  const pass = $("#create-pass").value;
  const pass2 = $("#create-pass2").value;
  const terms = $("#create-terms").checked;
  clearCreateError();

  if (runes(pass).length < 8) {
    return createError("Passphrase must be at least 8 characters.", $("#create-pass"));
  }
  if (pass !== pass2) {
    return createError("Passphrases do not match. Try again.", $("#create-pass2"));
  }
  if (!terms) {
    return createError("Confirm that you understand the identity cannot be recovered without the passphrase.");
  }

  setBusy(btn, true);
  try {
    const next = await api.CreateIdentity(name, pass);
    $("#create-pass").value = "";
    $("#create-pass2").value = "";
    applyState(next);
    toast("Identity created.", "success");
  } catch (err) {
    createError(errorText(err));
  } finally {
    setBusy(btn, false);
  }
}

function createError(message, input) {
  $("#create-error").textContent = message;
  if (input) {
    input.setAttribute("aria-invalid", "true");
    input.focus();
  }
  return false;
}

function clearCreateError() {
  $("#create-error").textContent = "";
  for (const input of $$("#create-form input")) input.removeAttribute("aria-invalid");
}

async function onUnlock(event) {
  event.preventDefault();
  const btn = $("#unlock-submit");
  const input = $("#unlock-pass");
  const pass = input.value;
  $("#unlock-error").textContent = "";
  input.removeAttribute("aria-invalid");

  if (!pass) {
    $("#unlock-error").textContent = "Enter your passphrase.";
    input.setAttribute("aria-invalid", "true");
    input.focus();
    return;
  }

  setBusy(btn, true);
  try {
    const next = await api.Unlock(pass);
    input.value = "";
    applyState(next);
    toast("Identity unlocked.", "success");
  } catch (err) {
    input.value = "";
    input.setAttribute("aria-invalid", "true");
    $("#unlock-error").textContent = errorText(err);
    input.focus();
  } finally {
    setBusy(btn, false);
  }
}

async function restoreBackup(btn) {
  setBusy(btn, true);
  try {
    const next = await api.RestoreBackup();
    if (next && next.hasIdentity) {
      applyState(next);
      toast("Backup restored. Unlock it with your passphrase.", "success");
    }
  } catch (err) {
    toast(errorText(err), "error");
  } finally {
    setBusy(btn, false);
  }
}

function updateStrength() {
  const value = $("#create-pass").value;
  const n = runes(value).length;
  let level = "none";
  let label = "at least 8 characters";
  if (n > 0 && n < 8) {
    level = "weak";
    label = `${n} / 8 characters`;
  } else if (n >= 8) {
    let score = 1;
    if (n >= 12) score++;
    if (/[^\x00-\x7F]/.test(value) || /[A-Z]/.test(value) || /\d/.test(value) || /[^A-Za-z0-9]/.test(value)) score++;
    if (score >= 3) {
      level = "strong";
      label = "strong";
    } else {
      level = "ok";
      label = n >= 12 ? "good" : "add more characters";
    }
  }
  $("#strength-bars").dataset.level = level;
  const strengthLabel = $("#strength-label");
  strengthLabel.dataset.level = level;
  strengthLabel.textContent = label;
}

/* ------------------------------------------------------------------ */
/* Main actions                                                        */
/* ------------------------------------------------------------------ */

async function onLock() {
  try {
    const next = await api.Lock();
    applyState(next);
  } catch (err) {
    toast(errorText(err), "error");
  }
}

async function copyPublicKey(btn) {
  const identity = state?.identity;
  if (!identity) return;
  await copyText(identity.publicKeyPem || identity.publicKeyFull || "", btn, "Public key copied.");
}

async function copyFingerprint(btn) {
  const identity = state?.identity;
  if (!identity) return;
  await copyText(identity.id, btn, "Identity fingerprint copied.");
}

async function copyText(text, btn, message) {
  if (!text) return;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
    } else {
      await api.CopyToClipboard(text);
    }
  } catch {
    try {
      await api.CopyToClipboard(text);
    } catch (err) {
      toast(errorText(err), "error");
      return;
    }
  }
  toast(message, "success");
  if (btn) {
    btn.classList.add("copied");
    setTimeout(() => btn.classList.remove("copied"), 1200);
  }
}

async function backup(btn) {
  setBusy(btn, true);
  try {
    const path = await api.ExportBackup();
    if (path) toast("Backup saved.", "success");
  } catch (err) {
    toast(errorText(err), "error");
  } finally {
    setBusy(btn, false);
  }
}

/* ------------------------------------------------------------------ */
/* Web sign-in approvals                                               */
/* ------------------------------------------------------------------ */

function startApprovalPolling() {
  if (approvalTimer) return;
  approvalTimer = setInterval(pollApproval, 700);
  pollApproval();
}

async function pollApproval() {
  if (!api?.GetPendingApproval || activeApproval || approvalBusy) return;
  try {
    const request = await api.GetPendingApproval();
    if (request && request.id) showApproval(request);
  } catch {
    // Polling errors are not actionable for the user.
  }
}

function showApproval(request) {
  activeApproval = request;
  approvalBusy = false;
  $("#approval-origin").textContent = request.origin || "Unknown website";
  $("#approval-challenge").textContent = shortChallenge(request.challenge);
  const fields = Array.isArray(request.profile) ? request.profile : [];
  $("#approval-profile").hidden = fields.length === 0;
  $("#approval-profile-count").textContent = `${fields.length} field${fields.length === 1 ? "" : "s"}`;
  $("#approval-profile-list").innerHTML = fields.map((field) => {
    const label = escapeHtml(field.label || field.key || "");
    const value = field.available
      ? escapeHtml(field.value || "")
      : "Not saved — not shared";
    return `<div><dt>${label}</dt><dd class="${field.available ? "" : "is-missing"}">${value}</dd></div>`;
  }).join("");
  $("#approval-approve").disabled = false;
  $("#approval-deny").disabled = false;
  setBusy($("#approval-approve"), false);
  setBusy($("#approval-deny"), false);
  $("#approval-root").hidden = false;
  $("#approval-approve").focus();
}

async function resolveApproval(approve) {
  if (!activeApproval || approvalBusy) return;
  const request = activeApproval;
  approvalBusy = true;
  const approveBtn = $("#approval-approve");
  const denyBtn = $("#approval-deny");
  approveBtn.disabled = true;
  denyBtn.disabled = true;
  setBusy(approve ? approveBtn : denyBtn, true);

  try {
    await api.ResolveApproval(request.id, approve);
    hideApproval();
    toast(approve ? "Sign-in request approved." : "Sign-in request denied.");
    if (approve) setTimeout(refreshState, 400);
  } catch (err) {
    hideApproval();
    toast(errorText(err), "error");
  } finally {
    approvalBusy = false;
    setBusy(approveBtn, false);
    setBusy(denyBtn, false);
    approveBtn.disabled = false;
    denyBtn.disabled = false;
  }
}

function hideApproval() {
  activeApproval = null;
  $("#approval-root").hidden = true;
  $("#approval-origin").textContent = "-";
  $("#approval-challenge").textContent = "-";
  $("#approval-profile").hidden = true;
  $("#approval-profile-list").innerHTML = "";
  $("#approval-profile-count").textContent = "0 fields";
}

function openUpdate() {
  if (UPDATE_URL) {
    if (!/^https:\/\//i.test(UPDATE_URL)) {
      toast("The update URL must use HTTPS.", "error");
      return;
    }
    if (window.runtime?.BrowserOpenURL) {
      window.runtime.BrowserOpenURL(UPDATE_URL);
    } else {
      window.open(UPDATE_URL, "_blank", "noopener");
    }
    return;
  }
  $("#update-current").textContent = state?.version || "0.1.0";
  $("#update-root").hidden = false;
  $("#update-close").focus();
}

function closeUpdate() {
  $("#update-root").hidden = true;
}

function shortChallenge(challenge) {
  const value = String(challenge || "");
  return value.length > 32 ? `${value.slice(0, 28)}…` : value;
}

/* ------------------------------------------------------------------ */
/* Settings page                                                       */
/* ------------------------------------------------------------------ */

function resetSettingsForms() {
  $("#settings-current-pass").value = "";
  $("#settings-new-pass").value = "";
  $("#settings-new-pass2").value = "";
  $("#settings-delete-pass").value = "";
  $("#settings-delete-confirm").checked = false;
  $("#settings-change-error").textContent = "";
  $("#settings-change-success").textContent = "";
  $("#settings-delete-error").textContent = "";
  for (const input of $$("#settings-change-form input, #settings-delete-form input")) {
    input.removeAttribute("aria-invalid");
  }
}

async function onChangePassphrase(event) {
  event.preventDefault();
  const current = $("#settings-current-pass").value;
  const next = $("#settings-new-pass").value;
  const next2 = $("#settings-new-pass2").value;
  const errBox = $("#settings-change-error");
  const okBox = $("#settings-change-success");
  errBox.textContent = "";
  okBox.textContent = "";

  if (!current) return (errBox.textContent = "Enter your current passphrase.");
  if (runes(next).length < 8) return (errBox.textContent = "The new passphrase must be at least 8 characters.");
  if (next !== next2) return (errBox.textContent = "The new passphrases do not match.");
  if (current === next) return (errBox.textContent = "The new passphrase must be different from the current one.");

  const btn = $("#settings-change-form button[type=submit]");
  setBusy(btn, true);
  try {
    await api.ChangePassphrase(current, next);
    resetSettingsForms();
    $("#settings-change-success").textContent = "Passphrase changed. The key is unchanged.";
    toast("Passphrase changed.", "success");
  } catch (err) {
    errBox.textContent = errorText(err);
  } finally {
    setBusy(btn, false);
  }
}

async function onDeleteIdentity(event) {
  event.preventDefault();
  const pass = $("#settings-delete-pass").value;
  const confirmed = $("#settings-delete-confirm").checked;
  const errBox = $("#settings-delete-error");
  errBox.textContent = "";

  if (!pass) return (errBox.textContent = "Enter your passphrase to confirm.");
  if (!confirmed) return (errBox.textContent = "Confirm that you understand the consequences.");

  const btn = $("#settings-delete-btn");
  setBusy(btn, true);
  try {
    const next = await api.DeleteIdentity(pass);
    applyState(next);
    toast("Identity removed from this computer.");
  } catch (err) {
    errBox.textContent = errorText(err);
    setBusy(btn, false);
  }
}

/* ------------------------------------------------------------------ */
/* Helpers                                                             */
/* ------------------------------------------------------------------ */

function setBusy(btn, busy) {
  if (!btn) return;
  btn.classList.toggle("is-loading", busy);
  btn.disabled = busy;
  btn.setAttribute("aria-busy", busy ? "true" : "false");
}

function toast(message, type = "info", title = "") {
  if (!message) return;
  while (toastRoot.children.length >= 3) toastRoot.firstElementChild.remove();
  const node = document.createElement("div");
  node.className = `toast${type === "error" ? " toast-error" : type === "success" ? " toast-success" : ""}`;
  node.innerHTML = icon(type === "error" ? "warning" : type === "success" ? "check" : "info");
  const text = document.createElement("div");
  if (title) {
    const strong = document.createElement("strong");
    strong.textContent = title;
    text.append(strong, document.createElement("br"));
  }
  text.append(document.createTextNode(message));
  node.append(text);
  toastRoot.append(node);
  setTimeout(() => {
    node.classList.add("hide");
    setTimeout(() => node.remove(), 200);
  }, 4200);
}

async function hideSplash() {
  if (splashHidden) return;
  splashHidden = true;
  const remaining = splashMinMs - (performance.now() - splashStarted);
  if (remaining > 0) await sleep(remaining);
  splash.classList.add("is-hidden");
}

function errorText(err) {
  let message = String(err?.message || err || "Unknown error");
  message = message.replace(/^Error:\s*/i, "").trim();
  return message || "Unknown error";
}

function runes(value) {
  return Array.from(value || "");
}

function icon(name) {
  return `<svg aria-hidden="true"><use href="#i-${name}"/></svg>`;
}

function shortenKey(key) {
  if (!key) return "ed25519:…";
  if (key.length <= 48) return key;
  return `${key.slice(0, 34)}…${key.slice(-8)}`;
}

function formatDate(value) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : dateFmt.format(date);
}

function formatDateTime(value) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : `${dateFmt.format(date)}, ${timeFmt.format(date)}`;
}

function escapeHtml(value) {
  return String(value ?? "").replace(/[&<>"']/g, (character) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[character]);
}

/* ------------------------------------------------------------------ */
/* Preview-only mock backend (never used in the packaged app)          */
/* ------------------------------------------------------------------ */

function createMockApi() {
  const params = new URLSearchParams(location.search);
  const mode = params.get("preview") || "setup";
  const knownModes = ["unlock", "overview", "home", "profile", "accounts", "settings"];
  const identity = {
    label: "Alex",
    id: "DSX-7K3M-2QXA-V1TB-8R0E",
    publicKeyPem: "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAq5zPreviewPublicKeyOnly1234567890abcdef\n-----END PUBLIC KEY-----\n",
    publicKeyFull: "ed25519:MCowBQYDK2VwAyEAq5zPreviewPublicKeyOnly1234567890abcdef",
    createdAt: new Date(Date.now() - 37 * 86400000).toISOString(),
    algorithm: "Ed25519",
    kdf: "Argon2id",
    cipher: "AES-256-GCM",
  };
  let hasIdentity = knownModes.includes(mode);
  let unlocked = ["overview", "home", "profile", "accounts", "settings"].includes(mode);
  let lastPass = "preview-passphrase";
  const approvedAccounts = [
    {
      origin: "http://127.0.0.1:3000",
      status: "approved",
      firstApprovedAt: new Date(Date.now() - 3 * 86400000).toISOString(),
      lastApprovedAt: new Date(Date.now() - 12 * 60000).toISOString(),
      signIns: 4,
    },
    {
      origin: "https://app.example.com",
      status: "approved",
      firstApprovedAt: new Date(Date.now() - 11 * 86400000).toISOString(),
      lastApprovedAt: new Date(Date.now() - 2 * 86400000).toISOString(),
      signIns: 2,
    },
  ];

  const snapshot = () => ({
    hasIdentity,
    unlocked,
    identity: hasIdentity ? { ...identity } : undefined,
    approvedAccounts: unlocked ? approvedAccounts : undefined,
    version: "0.1.0",
    dataDir: "C:\\Users\\Alex\\AppData\\Roaming\\DsxID",
  });
  const after = () => sleep(180).then(snapshot);
  let profile = {
    fullName: "Alex Example",
    email: "alex@example.com",
    phone: "+421 900 000 000",
    address1: "Main street 1",
    city: "Bratislava",
    postalCode: "811 01",
    country: "Slovakia",
  };

  return {
    GetState: async () => snapshot(),
    GetProfile: async () => ({ ...profile }),
    SaveProfile: async (values) => {
      await sleep(160);
      profile = { ...values };
      return undefined;
    },
    CreateIdentity: async (label, pass) => {
      if (hasIdentity) throw new Error("an identity already exists");
      if (runes(pass).length < 8) throw new Error("passphrase must be at least 8 characters");
      hasIdentity = true;
      unlocked = true;
      lastPass = pass;
      identity.label = String(label || "").trim() || "My identity";
      return after();
    },
    Unlock: async (pass) => {
      if (pass !== lastPass) throw new Error("Incorrect passphrase. Try again.");
      unlocked = true;
      return after();
    },
    Lock: async () => {
      unlocked = false;
      return after();
    },
    ChangePassphrase: async (current, next) => {
      if (current !== lastPass) throw new Error("The current passphrase is not correct.");
      lastPass = next;
      return after();
    },
    DeleteIdentity: async (pass) => {
      if (pass !== lastPass) throw new Error("Incorrect passphrase. The identity was not changed.");
      hasIdentity = false;
      unlocked = false;
      return after();
    },
    CopyToClipboard: async (text) => {
      if (navigator.clipboard?.writeText) return navigator.clipboard.writeText(text);
      return undefined;
    },
    ExportBackup: async () => {
      await sleep(220);
      return "C:\\Users\\Alex\\Downloads\\dsxid-backup-preview.json";
    },
    RestoreBackup: async () => {
      throw new Error("Backup restore is only available in the desktop application.");
    },
    GetPendingApproval: async () => null,
    ResolveApproval: async () => undefined,
  };
}
