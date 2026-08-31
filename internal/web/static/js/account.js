(function () {
  "use strict";
  document.querySelectorAll("[data-unstuck]").forEach(function (form) {
    form.addEventListener("submit", function (e) {
      if (!window.confirm("Send this character to their hearth (inn bind)? They must be logged out of the game.")) {
        e.preventDefault();
      }
    });
  });
  document.querySelectorAll("[data-wg-revoke]").forEach(function (form) {
    form.addEventListener("submit", function (e) {
      if (!window.confirm("Revoke this VPN config? The old file and QR will stop working.")) {
        e.preventDefault();
      }
    });
  });
  document.querySelectorAll("[data-reveal-pass]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var id = btn.getAttribute("data-reveal-pass");
      var input = id ? document.getElementById(id) : null;
      if (!input) {
        return;
      }
      if (input.type === "password") {
        input.type = "text";
        btn.textContent = "Hide";
      } else {
        input.type = "password";
        btn.textContent = "Show";
      }
    });
  });
})();
