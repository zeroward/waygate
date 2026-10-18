(function () {
  "use strict";

  var root = document.querySelector("[data-companion]");
  if (!root) {
    return;
  }

  var pick = document.querySelector("[data-companion-pick]");
  if (pick) {
    var sel = pick.querySelector("select");
    if (sel) {
      sel.addEventListener("change", function () {
        pick.submit();
      });
    }
  }

  var guid = root.getAttribute("data-guid");
  var zone = root.getAttribute("data-zone") || "";
  if (!guid) {
    return;
  }

  var timer = 0;
  var inflight = false;

  function statusLabel(status) {
    if (status === "complete") {
      return "Complete";
    }
    if (status === "failed") {
      return "Failed";
    }
    return "In progress";
  }

  function wowhead(id) {
    return "https://www.wowhead.com/wotlk/quest=" + id;
  }

  function emptyItem(text) {
    var li = document.createElement("li");
    li.className = "empty";
    li.textContent = text;
    return li;
  }

  function routeStatusLabel(status) {
    if (status === "active") {
      return "In your log";
    }
    if (status === "locked") {
      return "Locked";
    }
    return "Pick up";
  }

  function questItem(q) {
    var li = document.createElement("li");
    li.className = "quest quest-" + (q.status || "incomplete");
    var head = document.createElement("div");
    head.className = "quest-head";
    var a = document.createElement("a");
    a.href = wowhead(q.id);
    a.rel = "noopener noreferrer";
    a.textContent = q.title || ("Quest " + q.id);
    head.appendChild(a);
    var badge = document.createElement("span");
    badge.className = "quest-status";
    badge.textContent = statusLabel(q.status);
    head.appendChild(badge);
    li.appendChild(head);
    if (q.objectives && q.objectives.length) {
      var ul = document.createElement("ul");
      ul.className = "quest-obj";
      q.objectives.forEach(function (o) {
        var oi = document.createElement("li");
        if (o.done) {
          oi.className = "is-done";
        }
        oi.appendChild(document.createTextNode((o.text || "Objective") + " "));
        var n = document.createElement("span");
        n.textContent = (o.have || 0) + "/" + (o.need || 0);
        oi.appendChild(n);
        ul.appendChild(oi);
      });
      li.appendChild(ul);
    }
    return li;
  }

  function routeItem(q) {
    var li = document.createElement("li");
    li.className = "quest quest-" + (q.status || "ready") + (q.now ? " is-now" : "");
    var step = document.createElement("span");
    step.className = "quest-step";
    step.textContent = String(q.step || "");
    li.appendChild(step);
    var body = document.createElement("div");
    body.className = "quest-body";
    var head = document.createElement("div");
    head.className = "quest-head";
    var a = document.createElement("a");
    a.href = wowhead(q.id);
    a.rel = "noopener noreferrer";
    a.textContent = q.title || ("Quest " + q.id);
    head.appendChild(a);
    var badge = document.createElement("span");
    badge.className = "quest-status";
    badge.textContent = routeStatusLabel(q.status);
    head.appendChild(badge);
    body.appendChild(head);
    if (q.note) {
      var note = document.createElement("p");
      note.className = "quest-note";
      note.textContent = q.note;
      body.appendChild(note);
    }
    li.appendChild(body);
    return li;
  }

  function fillList(el, items, emptyText, factory) {
    if (!el) {
      return;
    }
    el.textContent = "";
    if (!items || !items.length) {
      el.appendChild(emptyItem(emptyText));
      return;
    }
    items.forEach(function (q) {
      el.appendChild(factory(q));
    });
  }

  function apply(data) {
    var loc = root.querySelector("[data-location]");
    if (loc) {
      loc.textContent = data.location || "";
    }
    var coords = root.querySelector("[data-coords]");
    if (coords) {
      var x = typeof data.x === "number" ? data.x : 0;
      var y = typeof data.y === "number" ? data.y : 0;
      coords.textContent = x.toFixed(1) + ", " + y.toFixed(1);
    }
    var on = root.querySelector("[data-online]");
    if (on) {
      on.textContent = data.online ? "Online" : "Offline";
    }
    var count = root.querySelector("[data-quest-count]");
    if (count) {
      count.textContent = data.quests ? String(data.quests.length) : "0";
    }
    fillList(
      root.querySelector("[data-quests]"),
      data.quests,
      "No quests in the log. Last saved by the realm — pick some up in-game.",
      questItem
    );
    var rname = root.querySelector("[data-route-name]");
    if (rname && data.routeName) {
      rname.textContent = data.routeName;
    }
    fillList(
      root.querySelector("[data-route]"),
      data.route,
      "No leftover quests in this region at your level.",
      routeItem
    );
  }

  function poll() {
    if (inflight || document.hidden) {
      return;
    }
    inflight = true;
    var url = "/companion/live?guid=" + encodeURIComponent(guid);
    if (zone) {
      url += "&zone=" + encodeURIComponent(zone);
    }
    fetch(url, {
      credentials: "same-origin",
      headers: { Accept: "application/json" }
    })
      .then(function (res) {
        if (res.status === 401) {
          window.location = "/account?next=" + encodeURIComponent("/companion?guid=" + guid);
          return null;
        }
        if (!res.ok) {
          return null;
        }
        return res.json();
      })
      .then(function (data) {
        if (data && data.guid) {
          apply(data);
        }
      })
      .catch(function () {})
      .then(function () {
        inflight = false;
      });
  }

  function start() {
    if (timer) {
      window.clearInterval(timer);
    }
    timer = window.setInterval(poll, 5000);
  }

  document.addEventListener("visibilitychange", function () {
    if (document.hidden) {
      if (timer) {
        window.clearInterval(timer);
        timer = 0;
      }
      return;
    }
    poll();
    start();
  });

  start();
})();
