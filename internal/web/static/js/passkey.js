(function () {
  "use strict";

  function $(sel, root) {
    return (root || document).querySelector(sel);
  }

  function csrf() {
    var el = $("[data-csrf]");
    return el ? el.getAttribute("data-csrf") || "" : "";
  }

  function nextPath() {
    var el = $("[data-passkey-next]");
    if (el && el.getAttribute("data-passkey-next")) {
      return el.getAttribute("data-passkey-next");
    }
    try {
      return new URL(window.location.href).searchParams.get("next") || "";
    } catch (err) {
      return "";
    }
  }

  function statusEl() {
    return $("[data-passkey-status]");
  }

  function setStatus(text, isError) {
    var el = statusEl();
    if (!el) return;
    el.hidden = !text;
    el.textContent = text || "";
    el.classList.toggle("flash", !!text && isError);
    el.classList.toggle("flash-error", !!text && isError);
  }

  function b64urlToBuf(s) {
    if (!s) return new ArrayBuffer(0);
    s = String(s).replace(/-/g, "+").replace(/_/g, "/");
    while (s.length % 4) s += "=";
    var bin = atob(s);
    var out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out.buffer;
  }

  function bufToB64url(buf) {
    var u = new Uint8Array(buf);
    var s = "";
    for (var i = 0; i < u.length; i++) s += String.fromCharCode(u[i]);
    return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
  }

  function restoreIds(list) {
    if (!list) return list;
    for (var i = 0; i < list.length; i++) {
      if (list[i].id) list[i].id = b64urlToBuf(list[i].id);
    }
    return list;
  }

  function publicKeyFrom(payload) {
    var pk = payload && payload.publicKey ? payload.publicKey : payload;
    pk.challenge = b64urlToBuf(pk.challenge);
    if (pk.user && pk.user.id) pk.user.id = b64urlToBuf(pk.user.id);
    restoreIds(pk.excludeCredentials);
    restoreIds(pk.allowCredentials);
    return pk;
  }

  function credToJSON(cred) {
    if (cred && typeof cred.toJSON === "function") return cred.toJSON();
    var o = {
      id: cred.id,
      rawId: bufToB64url(cred.rawId),
      type: cred.type,
      response: {},
      clientExtensionResults: cred.getClientExtensionResults ? cred.getClientExtensionResults() : {}
    };
    var r = cred.response;
    if (r.clientDataJSON) o.response.clientDataJSON = bufToB64url(r.clientDataJSON);
    if (r.attestationObject) o.response.attestationObject = bufToB64url(r.attestationObject);
    if (r.authenticatorData) o.response.authenticatorData = bufToB64url(r.authenticatorData);
    if (r.signature) o.response.signature = bufToB64url(r.signature);
    if (r.userHandle) o.response.userHandle = bufToB64url(r.userHandle);
    if (r.getTransports) o.response.transports = r.getTransports();
    if (cred.authenticatorAttachment) o.authenticatorAttachment = cred.authenticatorAttachment;
    return o;
  }

  function postJSON(path, body) {
    return fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
        "X-CSRF-Token": csrf()
      },
      body: JSON.stringify(body || {})
    }).then(function (res) {
      return res.json().then(function (data) {
        data._status = res.status;
        return data;
      }).catch(function () {
        return { ok: false, error: "Unexpected response", _status: res.status };
      });
    });
  }

  function cancelled(err) {
    return err && (err.name === "NotAllowedError" || err.name === "AbortError");
  }

  function unsupported() {
    return !window.PublicKeyCredential || !navigator.credentials;
  }

  function register() {
    if (unsupported()) {
      setStatus("This browser does not support passkeys.", true);
      return;
    }
    var nameEl = $("[data-passkey-name]");
    var btn = $("[data-passkey-register]");
    if (btn) btn.disabled = true;
    setStatus("Waiting for your authenticator…", false);
    postJSON("/account/passkey/register/begin", { name: nameEl ? nameEl.value : "" })
      .then(function (opts) {
        if (opts.error) throw new Error(opts.error);
        return navigator.credentials.create({ publicKey: publicKeyFrom(opts) });
      })
      .then(function (cred) {
        if (!cred) throw new Error("No passkey was created.");
        return postJSON("/account/passkey/register/finish", credToJSON(cred));
      })
      .then(function (data) {
        if (!data.ok) throw new Error(data.error || "Could not save the passkey.");
        window.location.reload();
      })
      .catch(function (err) {
        if (cancelled(err)) setStatus("Passkey setup was cancelled.", false);
        else setStatus(err.message || "Could not add the passkey.", true);
      })
      .then(function () {
        if (btn) btn.disabled = false;
      });
  }

  function login() {
    if (unsupported()) {
      setStatus("This browser does not support passkeys.", true);
      return;
    }
    var btn = $("[data-passkey-login]");
    if (btn) btn.disabled = true;
    setStatus("Waiting for your authenticator…", false);
    postJSON("/account/passkey/login/begin", { next: nextPath() })
      .then(function (opts) {
        if (opts.error) throw new Error(opts.error);
        return navigator.credentials.get({ publicKey: publicKeyFrom(opts) });
      })
      .then(function (cred) {
        if (!cred) throw new Error("No passkey was provided.");
        return postJSON("/account/passkey/login/finish", credToJSON(cred));
      })
      .then(function (data) {
        if (!data.ok) throw new Error(data.error || "Passkey was not recognized.");
        window.location = data.next || "/account";
      })
      .catch(function (err) {
        if (cancelled(err)) setStatus("Passkey sign-in was cancelled.", false);
        else setStatus(err.message || "Passkey was not recognized.", true);
      })
      .then(function () {
        if (btn) btn.disabled = false;
      });
  }

  document.addEventListener("click", function (e) {
    var t = e.target;
    if (!t || !t.closest) return;
    if (t.closest("[data-passkey-register]")) {
      e.preventDefault();
      register();
    } else if (t.closest("[data-passkey-login]")) {
      e.preventDefault();
      login();
    }
  });

  if (unsupported()) {
    document.querySelectorAll("[data-passkey-register], [data-passkey-login]").forEach(function (el) {
      el.hidden = true;
    });
  }
})();
