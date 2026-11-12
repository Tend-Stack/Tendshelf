// Passkeys (decision 0041). The shell's [data-ts-module] loader imports this file on the sign-in page
// and on the Passkeys page only. The server sends every word (data-ts-msg-* on the root) and the
// CSRF token (data-ts-csrf); this file talks to the browser's WebAuthn API and to the four JSON
// routes, and shows the button only where passkeys can work. Nothing here is stored or logged.

// base64url without padding is how the server writes and reads every binary value.
function fromBase64Url(text) {
  const padded = text.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(text.length / 4) * 4, "=");
  const raw = atob(padded);
  const bytes = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) {
    bytes[i] = raw.charCodeAt(i);
  }
  return bytes.buffer;
}

function toBase64Url(buffer) {
  let raw = "";
  for (const byte of new Uint8Array(buffer)) {
    raw += String.fromCharCode(byte);
  }
  return btoa(raw).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// A server error carries the plain words to show; anything else is the generic failure.
class ServerError extends Error {}

async function postJSON(url, body, csrf) {
  const response = await fetch(url, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
    body: JSON.stringify(body),
  });
  let data = {};
  try {
    data = await response.json();
  } catch {
    // An answer that is not JSON is treated as a failure below.
  }
  if (!response.ok) {
    throw new ServerError(data.error || "");
  }
  return data;
}

// The options come from the server as text; the browser wants buffers.
function publicKeyFrom(options) {
  const key = options.publicKey;
  key.challenge = fromBase64Url(key.challenge);
  if (key.user) {
    key.user.id = fromBase64Url(key.user.id);
  }
  for (const list of [key.allowCredentials, key.excludeCredentials]) {
    for (const entry of list || []) {
      entry.id = fromBase64Url(entry.id);
    }
  }
  return key;
}

// Show a message in the status line, or in the error line where the page has one.
function say(root, text, asError) {
  const status = root.querySelector("[data-ts-passkey-status]");
  const error = root.querySelector("[data-ts-passkey-error]");
  if (error) {
    error.textContent = asError ? text : "";
    error.hidden = !asError || text === "";
  }
  if (status) {
    status.textContent = asError && error ? "" : text;
  }
}

function setBusy(button, busy) {
  button.disabled = busy;
  button.setAttribute("aria-busy", busy ? "true" : "false");
}

// The words for a failed ceremony: the server's own, the cancelled line when the person dismissed
// the browser's dialog, else the generic one.
function failureText(error, words) {
  if (error instanceof ServerError && error.message) {
    return error.message;
  }
  if (error && error.name === "NotAllowedError") {
    return words.cancelled;
  }
  return words.failed;
}

function wordsOf(root) {
  return {
    waiting: root.dataset.tsMsgWaiting || "",
    cancelled: root.dataset.tsMsgCancelled || "",
    failed: root.dataset.tsMsgFailed || "",
    unsupported: root.dataset.tsMsgUnsupported || "",
  };
}

function mountSignIn(root, words) {
  const button = root.querySelector("[data-ts-passkey-button]");
  const csrf = root.dataset.tsCsrf || "";
  root.hidden = false;
  button.addEventListener("click", async () => {
    setBusy(button, true);
    say(root, words.waiting, false);
    try {
      const options = await postJSON("/login/passkey/options", {}, csrf);
      const credential = await navigator.credentials.get({ publicKey: publicKeyFrom(options) });
      const response = credential.response;
      const answer = await postJSON(
        "/login/passkey",
        {
          id: toBase64Url(credential.rawId),
          type: credential.type,
          clientDataJSON: toBase64Url(response.clientDataJSON),
          authenticatorData: toBase64Url(response.authenticatorData),
          signature: toBase64Url(response.signature),
          userHandle: response.userHandle ? toBase64Url(response.userHandle) : "",
          next: root.dataset.tsNext || "",
        },
        csrf,
      );
      window.location.assign(answer.redirect || "/");
    } catch (error) {
      say(root, failureText(error, words), true);
      setBusy(button, false);
    }
  });
}

function mountAdd(root, words) {
  const form = root.querySelector("[data-ts-passkey-form]");
  const button = root.querySelector("[data-ts-passkey-button]");
  const csrf = root.dataset.tsCsrf || "";
  form.hidden = false;
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    setBusy(button, true);
    say(root, words.waiting, false);
    try {
      const password = form.elements.password ? form.elements.password.value : "";
      const options = await postJSON("/me/passkeys/options", { password }, csrf);
      const credential = await navigator.credentials.create({ publicKey: publicKeyFrom(options) });
      const response = credential.response;
      const answer = await postJSON(
        "/me/passkeys",
        {
          name: form.elements.name.value,
          type: credential.type,
          clientDataJSON: toBase64Url(response.clientDataJSON),
          attestationObject: toBase64Url(response.attestationObject),
          transports: response.getTransports ? response.getTransports() : [],
        },
        csrf,
      );
      window.location.assign(answer.redirect || "/me/passkeys");
    } catch (error) {
      say(root, failureText(error, words), true);
      if (form.elements.password) {
        form.elements.password.value = "";
      }
      setBusy(button, false);
    }
  });
}

export function mount(root) {
  const words = wordsOf(root);
  const supported = window.isSecureContext && window.PublicKeyCredential;
  if (!supported) {
    // The sign-in button stays hidden; the Passkeys page says why.
    const note = root.querySelector("[data-ts-passkey-unsupported]");
    if (note) {
      note.textContent = words.unsupported;
      note.hidden = false;
    }
    return;
  }
  if (root.dataset.tsPasskey === "signin") {
    mountSignIn(root, words);
  } else {
    mountAdd(root, words);
  }
}
