(function () {
  "use strict";
  var link = document.getElementById("sso-continue");
  if (link) {
    window.location.replace(link.href);
  }
})();
