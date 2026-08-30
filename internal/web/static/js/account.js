(function () {
  "use strict";
  document.querySelectorAll("[data-unstuck]").forEach(function (form) {
    form.addEventListener("submit", function (e) {
      if (!window.confirm("Send this character to their hearth (inn bind)? They must be logged out of the game.")) {
        e.preventDefault();
      }
    });
  });
})();
