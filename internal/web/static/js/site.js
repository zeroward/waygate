(function () {
  "use strict";

  document.documentElement.classList.add("js");
  const toggle = document.querySelector(".nav-toggle");
  const navigation = document.getElementById("site-navigation");
  const menus = Array.from(document.querySelectorAll("[data-menu]"));

  function closeNavigation() {
    if (!toggle || !navigation) return;
    toggle.setAttribute("aria-expanded", "false");
    navigation.classList.remove("is-open");
  }

  if (toggle && navigation) {
    toggle.hidden = false;
    toggle.addEventListener("click", function () {
      const open = toggle.getAttribute("aria-expanded") !== "true";
      toggle.setAttribute("aria-expanded", String(open));
      navigation.classList.toggle("is-open", open);
    });
    window.matchMedia("(min-width: 761px)").addEventListener("change", closeNavigation);
  }

  menus.forEach(function (menu) {
    menu.addEventListener("toggle", function () {
      if (menu.open) menus.forEach(function (other) { if (other !== menu) other.open = false; });
    });
  });
  document.addEventListener("click", function (event) {
    menus.forEach(function (menu) { if (!menu.contains(event.target)) menu.open = false; });
    if (navigation && !navigation.contains(event.target) && !toggle.contains(event.target)) closeNavigation();
  });
  document.addEventListener("keydown", function (event) {
    if (event.key !== "Escape") return;
    const open = menus.find(function (menu) { return menu.open; });
    if (open) {
      open.open = false;
      open.querySelector("summary").focus();
    } else if (toggle && toggle.getAttribute("aria-expanded") === "true") {
      closeNavigation();
      toggle.focus();
    }
  });

  // Ordinary fragment links remain usable without JavaScript. Enhancement hides
  // unrelated sections while preserving deep links and browser history.
  document.querySelectorAll("[data-sections]").forEach(function (root) {
    const sections = Array.from(root.querySelectorAll("[data-section]"));
    const links = Array.from(root.querySelectorAll("[data-section-link]"));

    function reveal(focus) {
      let id;
      try { id = decodeURIComponent(window.location.hash.slice(1)); } catch (_) { id = ""; }
      const target = id ? document.getElementById(id) : null;
      const section = sections.find(function (item) { return item === target || item.contains(target); }) ||
        sections.find(function (item) { return item.id === root.dataset.defaultSection; }) || sections[0];
      sections.forEach(function (item) { item.hidden = item !== section; });
      links.forEach(function (link) {
        if (link.hash === "#" + section.id) link.setAttribute("aria-current", "location");
        else link.removeAttribute("aria-current");
      });
      if (target && section.contains(target)) {
        let ancestor = target;
        while (ancestor && ancestor !== section) {
          if (ancestor.tagName === "DETAILS") ancestor.open = true;
          ancestor = ancestor.parentElement;
        }
        if (focus) {
          const destination = target.matches("[data-section]") ? target.querySelector("h2, h3") || target : target;
          destination.setAttribute("tabindex", "-1");
          destination.focus({ preventScroll: true });
        }
        const flash = !focus && document.querySelector("main > .flash");
        (flash || target).scrollIntoView({ block: "start" });
        if (flash) requestAnimationFrame(function () { flash.scrollIntoView({ block: "start" }); });
      }
    }

    window.addEventListener("hashchange", function () { reveal(true); });
    // Invalid controls may live inside a closed disclosure. Open before the
    // browser attempts to focus them, without suppressing native validation.
    root.addEventListener("invalid", function (event) {
      let parent = event.target.parentElement;
      while (parent && parent !== root) {
        if (parent.tagName === "DETAILS") parent.open = true;
        parent = parent.parentElement;
      }
    }, true);
    reveal(false);
  });
})();
