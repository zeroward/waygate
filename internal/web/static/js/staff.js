(function () {
  const root = document.querySelector("[data-staff]");
  if (!root) return;

  const actorGM = Number(root.getAttribute("data-actor-gm") || "0");
  const actorUser = (root.getAttribute("data-actor-user") || "").toUpperCase();
  const table = root.querySelector("[data-staff-table]");
  const tbody = table ? table.querySelector("tbody") : null;
  const drawer = root.querySelector("[data-staff-drawer]");
  const emptyEl = root.querySelector("[data-staff-empty]");
  const actionsEl = root.querySelector("[data-staff-actions]");
  const work = root.querySelector(".staff-work");
  const backdrop = root.querySelector("[data-staff-backdrop]");
  const searchSelect = root.querySelector("[data-staff-search-select]");
  const nameEl = root.querySelector("[data-staff-name]");
  const gmBadge = root.querySelector("[data-staff-gm-badge]");
  const statusEl = root.querySelector("[data-staff-status]");
  const flashEl = root.querySelector("[data-staff-flash]");
  const blockedEl = root.querySelector("[data-staff-blocked]");
  const resetForm = root.querySelector("[data-staff-reset]");
  const userField = root.querySelector("[data-staff-user-field]");
  const confirmBox = root.querySelector("[data-staff-confirm]");
  const confirmText = root.querySelector("[data-staff-confirm-text]");
  const rankForm = root.querySelector("[data-staff-rank]");
  const rankSelect = root.querySelector("[data-staff-rank-select]");
  const rankConfirm = root.querySelector("[data-staff-rank-confirm]");
  const rankConfirmText = root.querySelector("[data-staff-rank-confirm-text]");
  const banForm = root.querySelector("[data-staff-ban]");
  const unbanForm = root.querySelector("[data-staff-unban]");
  const banConfirm = root.querySelector("[data-staff-ban-confirm]");
  const banConfirmText = root.querySelector("[data-staff-ban-confirm-text]");
  const unbanConfirm = root.querySelector("[data-staff-unban-confirm]");
  const banMeta = root.querySelector("[data-staff-ban-meta]");
  const copyBtn = root.querySelector("[data-staff-copy]");
  const clearBtn = root.querySelector("[data-staff-clear]");

  function rows() {
    return tbody ? Array.from(tbody.querySelectorAll("tr.staff-row")) : [];
  }

  function isEditable(el) {
    if (!el || !el.closest) return false;
    const tag = el.tagName;
    return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" || tag === "BUTTON" || tag === "A" || el.isContentEditable;
  }

  function parseRow(tr) {
    return {
      username: tr.getAttribute("data-user") || "",
      email: tr.getAttribute("data-email") || "",
      gm: Number(tr.getAttribute("data-gm") || "0"),
      online: tr.getAttribute("data-online") === "1",
      banned: tr.getAttribute("data-banned") === "1",
      banreason: tr.getAttribute("data-ban-reason") || "",
      banuntil: tr.getAttribute("data-ban-until") || "",
    };
  }

  function replaceSelect(user) {
    const url = new URL(window.location.href);
    if (user) url.searchParams.set("select", user);
    else url.searchParams.delete("select");
    const qs = url.searchParams.toString();
    history.replaceState(null, "", url.pathname + (qs ? "?" + qs : ""));
  }

  function copyFallback(text) {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.left = "-9999px";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); } catch (err) {}
    document.body.removeChild(ta);
  }

  function hideConfirm() {
    if (!confirmBox || !resetForm) return;
    confirmBox.hidden = true;
    delete resetForm.dataset.confirmed;
  }

  function hideRankConfirm() {
    if (!rankConfirm || !rankForm) return;
    rankConfirm.hidden = true;
    delete rankForm.dataset.confirmed;
  }

  function hideBanConfirm() {
    if (banConfirm && banForm) {
      banConfirm.hidden = true;
      delete banForm.dataset.confirmed;
    }
    if (unbanConfirm && unbanForm) {
      unbanConfirm.hidden = true;
      delete unbanForm.dataset.confirmed;
    }
  }

  function rankName(gm) {
    switch (Number(gm)) {
      case 1: return "Moderator";
      case 2: return "GM";
      case 3: return "Admin";
      case 4: return "Super GM";
      default: return "Player";
    }
  }

  function setDisabled(on) {
    if (!resetForm) return;
    resetForm.setAttribute("aria-disabled", on ? "true" : "false");
    resetForm.querySelectorAll("input:not([type=hidden]), button[data-staff-reset-go]").forEach(function (el) {
      el.disabled = on;
    });
  }

  function setRankDisabled(on) {
    if (!rankForm) return;
    rankForm.setAttribute("aria-disabled", on ? "true" : "false");
    if (rankSelect) rankSelect.disabled = on;
    const go = rankForm.querySelector("[data-staff-rank-go]");
    if (go) go.disabled = on;
  }

  function setBanDisabled(on) {
    [banForm, unbanForm].forEach(function (form) {
      if (!form) return;
      form.setAttribute("aria-disabled", on ? "true" : "false");
      form.querySelectorAll("input:not([type=hidden]), select, button[data-staff-ban-go], button[data-staff-unban-go]").forEach(function (el) {
        el.disabled = on;
      });
    });
  }

  function fillPanel(acc) {
    if (!acc) return;
    nameEl.textContent = acc.username;
    gmBadge.textContent = rankName(acc.gm);
    gmBadge.className = acc.gm > 0 ? "badge badge-gold" : "badge";
    if (acc.banned) {
      statusEl.textContent = "suspended";
      statusEl.className = "staff-status is-banned";
    } else {
      statusEl.textContent = acc.online ? "online" : "offline";
      statusEl.className = acc.online ? "staff-status is-online" : "staff-status is-offline";
    }
    root.querySelectorAll("[data-staff-user-field]").forEach(function (el) {
      el.value = acc.username;
    });
    if (searchSelect) searchSelect.value = acc.username;
    const blocked = acc.gm > actorGM;
    const self = acc.username.toUpperCase() === actorUser;
    if (blocked) {
      blockedEl.hidden = false;
      blockedEl.textContent = "Cannot modify " + rankName(acc.gm);
    } else {
      blockedEl.hidden = true;
      blockedEl.textContent = "";
    }
    setDisabled(blocked);
    setRankDisabled(blocked || self);
    setBanDisabled(blocked || self);
    if (banForm) banForm.hidden = !!acc.banned;
    if (unbanForm) unbanForm.hidden = !acc.banned;
    if (banMeta) {
      const bits = [];
      if (acc.banuntil) bits.push("Until " + acc.banuntil + ".");
      if (acc.banreason) bits.push(acc.banreason);
      banMeta.textContent = bits.join(" ");
    }
    if (rankSelect) {
      Array.prototype.forEach.call(rankSelect.options, function (opt) {
        const v = Number(opt.value);
        opt.disabled = v >= actorGM;
      });
      const want = String(acc.gm === 1 || acc.gm === 4 ? 0 : acc.gm);
      rankSelect.value = want;
      if (rankSelect.value !== want) rankSelect.value = "0";
    }
    if (confirmText) confirmText.textContent = "Set a new password for " + acc.username + "?";
    hideConfirm();
    hideRankConfirm();
    hideBanConfirm();
  }

  function showFlash(kind, text) {
    if (!flashEl) return;
    if (!text) {
      flashEl.hidden = true;
      flashEl.textContent = "";
      flashEl.className = "staff-panel-flash";
      return;
    }
    flashEl.hidden = false;
    flashEl.textContent = text;
    flashEl.className = "staff-panel-flash flash-" + kind + " is-flash";
  }

  function selectRow(tr, opts) {
    const updateUrl = !opts || opts.updateUrl !== false;
    rows().forEach(function (r) {
      r.classList.remove("is-selected");
      r.setAttribute("aria-selected", "false");
    });
    if (!tr) {
      if (emptyEl) emptyEl.hidden = false;
      if (actionsEl) actionsEl.hidden = true;
      if (drawer) drawer.classList.remove("is-open");
      if (work) work.classList.remove("has-selection");
      if (backdrop) backdrop.hidden = true;
      if (searchSelect) searchSelect.value = "";
      if (userField) userField.value = "";
      hideConfirm();
      hideRankConfirm();
      showFlash("", "");
      if (updateUrl) replaceSelect("");
      return;
    }
    tr.classList.add("is-selected");
    tr.setAttribute("aria-selected", "true");
    fillPanel(parseRow(tr));
    if (emptyEl) emptyEl.hidden = true;
    if (actionsEl) actionsEl.hidden = false;
    if (drawer) drawer.classList.add("is-open");
    if (work) work.classList.add("has-selection");
    if (backdrop) backdrop.hidden = false;
    if (!opts || opts.clearFlash !== false) showFlash("", "");
    if (updateUrl) replaceSelect(tr.getAttribute("data-user") || "");
  }

  if (tbody) {
    tbody.addEventListener("click", function (e) {
      const tr = e.target.closest("tr.staff-row");
      if (!tr) return;
      e.preventDefault();
      selectRow(tr, { clearFlash: true });
      tr.focus({ preventScroll: true });
    });
  }

  root.addEventListener("keydown", function (e) {
    if (e.key === "Escape") {
      if (confirmBox && !confirmBox.hidden) {
        hideConfirm();
        e.preventDefault();
        return;
      }
      if (drawer && drawer.classList.contains("is-open") && !isEditable(e.target)) {
        selectRow(null);
        e.preventDefault();
      }
      return;
    }
    if (isEditable(e.target)) return;
    const list = rows();
    if (!list.length) return;
    let i = list.indexOf(e.target.closest ? e.target.closest("tr.staff-row") : null);
    if (e.key === "ArrowDown") {
      e.preventDefault();
      const next = i < 0 ? list[0] : list[Math.min(i + 1, list.length - 1)];
      next.focus();
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      const prev = i < 0 ? list[0] : list[Math.max(i - 1, 0)];
      prev.focus();
    } else if (e.key === "Enter") {
      if (i < 0) return;
      e.preventDefault();
      selectRow(list[i], { clearFlash: true });
    }
  });

  if (clearBtn) {
    clearBtn.addEventListener("click", function () {
      selectRow(null);
    });
  }

  if (backdrop) {
    backdrop.addEventListener("click", function () {
      selectRow(null);
    });
  }

  if (copyBtn) {
    copyBtn.addEventListener("click", function () {
      const name = userField ? userField.value : "";
      if (!name) return;
      const done = function () {
        const prev = copyBtn.textContent;
        copyBtn.textContent = "Copied";
        setTimeout(function () { copyBtn.textContent = prev; }, 1200);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(name).then(done).catch(function () { copyFallback(name); done(); });
      } else {
        copyFallback(name);
        done();
      }
    });
  }

  if (resetForm) {
    resetForm.addEventListener("submit", function (e) {
      if (resetForm.dataset.confirmed === "1") return;
      e.preventDefault();
      if (userField && !userField.value) return;
      if (confirmBox) confirmBox.hidden = false;
    });
  }

  const yes = root.querySelector("[data-staff-confirm-yes]");
  const no = root.querySelector("[data-staff-confirm-no]");
  if (yes) {
    yes.addEventListener("click", function () {
      if (!resetForm) return;
      resetForm.dataset.confirmed = "1";
      if (resetForm.requestSubmit) resetForm.requestSubmit();
      else resetForm.submit();
    });
  }
  if (no) {
    no.addEventListener("click", function () {
      hideConfirm();
    });
  }

  if (rankForm) {
    rankForm.addEventListener("submit", function (e) {
      if (rankForm.dataset.confirmed === "1") return;
      e.preventDefault();
      if (userField && !userField.value) return;
      const label = rankSelect && rankSelect.options[rankSelect.selectedIndex]
        ? rankSelect.options[rankSelect.selectedIndex].text
        : "this rank";
      const who = (rankForm.querySelector("[data-staff-user-field]") || userField);
      const name = who ? who.value : "this account";
      if (rankConfirmText) rankConfirmText.textContent = "Set " + name + " to " + label + "?";
      if (rankConfirm) rankConfirm.hidden = false;
    });
  }
  const rankYes = root.querySelector("[data-staff-rank-yes]");
  const rankNo = root.querySelector("[data-staff-rank-no]");
  if (rankYes) {
    rankYes.addEventListener("click", function () {
      if (!rankForm) return;
      rankForm.dataset.confirmed = "1";
      if (rankForm.requestSubmit) rankForm.requestSubmit();
      else rankForm.submit();
    });
  }
  if (rankNo) {
    rankNo.addEventListener("click", function () {
      hideRankConfirm();
    });
  }

  if (banForm) {
    banForm.addEventListener("submit", function (e) {
      if (banForm.dataset.confirmed === "1") return;
      e.preventDefault();
      const who = banForm.querySelector("[data-staff-user-field]") || userField;
      const name = who ? who.value : "this account";
      if (banConfirmText) banConfirmText.textContent = "Suspend " + name + "? They will not be able to log in.";
      if (banConfirm) banConfirm.hidden = false;
    });
  }
  const banYes = root.querySelector("[data-staff-ban-yes]");
  const banNo = root.querySelector("[data-staff-ban-no]");
  if (banYes) {
    banYes.addEventListener("click", function () {
      if (!banForm) return;
      banForm.dataset.confirmed = "1";
      if (banForm.requestSubmit) banForm.requestSubmit();
      else banForm.submit();
    });
  }
  if (banNo) {
    banNo.addEventListener("click", function () { hideBanConfirm(); });
  }

  if (unbanForm) {
    unbanForm.addEventListener("submit", function (e) {
      if (unbanForm.dataset.confirmed === "1") return;
      e.preventDefault();
      if (unbanConfirm) unbanConfirm.hidden = false;
    });
  }
  const unbanYes = root.querySelector("[data-staff-unban-yes]");
  const unbanNo = root.querySelector("[data-staff-unban-no]");
  if (unbanYes) {
    unbanYes.addEventListener("click", function () {
      if (!unbanForm) return;
      unbanForm.dataset.confirmed = "1";
      if (unbanForm.requestSubmit) unbanForm.requestSubmit();
      else unbanForm.submit();
    });
  }
  if (unbanNo) {
    unbanNo.addEventListener("click", function () { hideBanConfirm(); });
  }

  const selected = tbody && tbody.querySelector("tr.is-selected");
  if (selected) {
    selected.focus({ preventScroll: true });
    selected.scrollIntoView({ block: "nearest" });
    if (drawer) drawer.classList.add("is-open");
    if (work) work.classList.add("has-selection");
    if (backdrop) backdrop.hidden = false;
    if (flashEl && !flashEl.hidden) flashEl.classList.add("is-flash");
  } else if (userField && userField.value) {
    if (emptyEl) emptyEl.hidden = true;
    if (actionsEl) actionsEl.hidden = false;
    if (drawer) drawer.classList.add("is-open");
    if (work) work.classList.add("has-selection");
    if (backdrop) backdrop.hidden = false;
  }

  const dlForm = root.querySelector("[data-dl-form]");
  if (dlForm) {
    const catSel = dlForm.querySelector("[data-dl-category]");
    function syncDlFields() {
      const formLocked = dlForm.getAttribute("aria-disabled") === "true";
      const cat = catSel ? catSel.value : "";
      dlForm.querySelectorAll("[data-dl-for]").forEach(function (box) {
        const on = (box.getAttribute("data-dl-for") || "").split(/\s+/).indexOf(cat) >= 0;
        box.hidden = !on;
        box.querySelectorAll("input, textarea").forEach(function (inp) {
          inp.disabled = !on || formLocked;
          inp.required = on && !inp.disabled && inp.getAttribute("data-req") === "1";
        });
      });
    }
    if (catSel) catSel.addEventListener("change", syncDlFields);
    syncDlFields();

    const prog = dlForm.querySelector("[data-dl-progress]");
    const progLabel = dlForm.querySelector("[data-dl-progress-label]");
    const progBar = dlForm.querySelector("[data-dl-progress-bar]");
    const progFill = dlForm.querySelector("[data-dl-progress-fill]");
    const goBtn = dlForm.querySelector(".staff-dl-go .btn");
    function setBarPct(pct) {
      if (progBar) {
        progBar.classList.remove("is-indeterminate");
        progBar.setAttribute("aria-valuenow", String(pct));
      }
      if (progFill) progFill.style.width = pct + "%";
    }
    function setBarIndeterminate() {
      if (progBar) {
        progBar.classList.add("is-indeterminate");
        progBar.removeAttribute("aria-valuenow");
      }
      if (progFill) progFill.style.width = "35%";
    }
    dlForm.addEventListener("submit", function (e) {
      if (!window.XMLHttpRequest || !window.FormData) return;
      e.preventDefault();
      const fileInput = dlForm.querySelector('input[type="file"]');
      const file = fileInput && fileInput.files && fileInput.files[0];
      if (!file) return;
      const scanMax = Number(dlForm.getAttribute("data-dl-scan-max") || "0");
      const willScan = scanMax > 0 && file.size <= scanMax;
      if (goBtn) goBtn.disabled = true;
      if (prog) prog.hidden = false;
      if (progLabel) progLabel.textContent = "Uploading… 0%";
      setBarPct(0);
      const xhr = new XMLHttpRequest();
      xhr.open("POST", dlForm.getAttribute("action") || "/staff/downloads");
      xhr.setRequestHeader("Accept", "application/json");
      xhr.setRequestHeader("X-Requested-With", "XMLHttpRequest");
      xhr.upload.onprogress = function (ev) {
        if (!ev.lengthComputable) return;
        const pct = Math.max(0, Math.min(100, Math.round((ev.loaded / ev.total) * 100)));
        setBarPct(pct);
        if (progLabel) progLabel.textContent = "Uploading… " + pct + "%";
      };
      xhr.upload.onload = function () {
        setBarPct(100);
        if (willScan) {
          if (progLabel) progLabel.textContent = "Scanning with ClamAV…";
          setBarIndeterminate();
        } else if (progLabel) {
          progLabel.textContent = "Saving…";
        }
      };
      xhr.onerror = function () {
        if (progLabel) progLabel.textContent = "Upload failed.";
        if (goBtn) goBtn.disabled = false;
      };
      xhr.onload = function () {
        var data = {};
        try { data = JSON.parse(xhr.responseText); } catch (err) { data = {}; }
        var ok = data.ok === true || (xhr.status >= 200 && xhr.status < 300 && data.ok !== false && !data.message);
        if (ok) {
          if (progLabel) progLabel.textContent = "Done.";
          setBarPct(100);
          window.location.replace("/staff?ok=" + Date.now() + "#downloads");
          return;
        }
        if (progLabel) progLabel.textContent = data.message || "Upload failed.";
        if (goBtn) goBtn.disabled = false;
        setBarPct(0);
      };
      xhr.send(new FormData(dlForm));
    });
  }

  root.querySelectorAll("form[data-confirm-msg]").forEach(function (form) {
    const box = form.querySelector("[data-inline-confirm]");
    const yesBtn = form.querySelector("[data-inline-yes]");
    const noBtn = form.querySelector("[data-inline-no]");
    form.addEventListener("submit", function (e) {
      if (form.dataset.confirmed === "1") return;
      e.preventDefault();
      if (box) box.hidden = false;
    });
    if (yesBtn) {
      yesBtn.addEventListener("click", function () {
        form.dataset.confirmed = "1";
        if (form.requestSubmit) form.requestSubmit();
        else form.submit();
      });
    }
    if (noBtn) {
      noBtn.addEventListener("click", function () {
        if (box) box.hidden = true;
        delete form.dataset.confirmed;
      });
    }
  });
})();
