(function () {
  "use strict";

  function slugify(s) {
    return String(s || "")
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 80)
      .replace(/-+$/g, "");
  }

  function bindCopy(root) {
    (root || document).querySelectorAll(".kb-copy").forEach(function (btn) {
      if (btn.dataset.bound) return;
      btn.dataset.bound = "1";
      btn.addEventListener("click", function () {
        var block = btn.closest(".kb-code");
        var pre = block && block.querySelector("pre");
        var text = pre ? pre.innerText : "";
        function done() {
          var prev = btn.textContent;
          btn.textContent = "Copied";
          setTimeout(function () {
            btn.textContent = prev || "Copy";
          }, 1400);
        }
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(done).catch(function () {
            fallbackCopy(text, done);
          });
        } else {
          fallbackCopy(text, done);
        }
      });
    });
  }

  function fallbackCopy(text, done) {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.left = "-9999px";
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand("copy");
      done();
    } catch (e) {}
    document.body.removeChild(ta);
  }

  bindCopy(document);

  document.querySelectorAll("[data-kb-delete]").forEach(function (form) {
    form.addEventListener("submit", function (e) {
      if (!window.confirm("Delete this article? This cannot be undone.")) {
        e.preventDefault();
      }
    });
  });

  var editor = document.querySelector("[data-kb-editor]");
  if (!editor) return;

  var title = editor.querySelector("[data-kb-title]");
  var slug = editor.querySelector("[data-kb-slug]");
  var slugPrev = editor.querySelector("[data-kb-slug-preview]");
  var body = editor.querySelector("[data-kb-body]");
  var preview = document.querySelector("[data-kb-preview]");
  var csrf = editor.querySelector('input[name="csrf_token"]');
  var locked = slug && slug.getAttribute("data-kb-slug-locked") === "1";

  function syncSlugPreview() {
    if (slugPrev && slug) slugPrev.textContent = slug.value || "slug";
  }

  if (title && slug && !locked) {
    title.addEventListener("input", function () {
      if (locked) return;
      slug.value = slugify(title.value);
      syncSlugPreview();
    });
  }
  if (slug) {
    slug.addEventListener("input", function () {
      locked = true;
      syncSlugPreview();
    });
  }

  var timer = 0;
  function renderPreview() {
    if (!body || !preview || !csrf) return;
    var params = new URLSearchParams();
    params.set("csrf_token", csrf.value);
    params.set("body_markdown", body.value);
    fetch("/staff/kb/preview", {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      },
      body: params.toString(),
      credentials: "same-origin",
    })
      .then(function (res) {
        if (!res.ok) return null;
        return res.text();
      })
      .then(function (html) {
        if (html === null) return;
        preview.innerHTML = html;
        bindCopy(preview);
      })
      .catch(function () {});
  }

  if (body && preview) {
    body.addEventListener("input", function () {
      clearTimeout(timer);
      timer = setTimeout(renderPreview, 280);
    });
  }
})();
