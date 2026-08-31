(function () {
  const root = document.getElementById("armory-model");
  if (!root || typeof window.jQuery !== "function") return;

  const raw = root.getAttribute("data-model") || "";
  let character;
  try {
    character = JSON.parse(raw);
  } catch (err) {
    return;
  }
  if (!character || !character.race) return;

  const CONTENT = "/armory/mv/";
  window.CONTENT_PATH = CONTENT;
  window.WOTLK_TO_RETAIL_DISPLAY_ID_API = "/armory/display";

  if (!window.WH) window.WH = {};
  window.WH.debug = function () {};
  window.WH.defaultAnimation = "Stand";
  window.WH.WebP = { getImageExtension: function () { return ".webp"; } };
  window.WH.Wow = window.WH.Wow || {};
  window.WH.Wow.Item = window.WH.Wow.Item || {
    INVENTORY_TYPE_HEAD: 1,
    INVENTORY_TYPE_SHOULDERS: 3,
    INVENTORY_TYPE_SHIRT: 4,
    INVENTORY_TYPE_CHEST: 5,
    INVENTORY_TYPE_WAIST: 6,
    INVENTORY_TYPE_LEGS: 7,
    INVENTORY_TYPE_FEET: 8,
    INVENTORY_TYPE_WRISTS: 9,
    INVENTORY_TYPE_HANDS: 10,
    INVENTORY_TYPE_BACK: 16,
    INVENTORY_TYPE_TABARD: 19,
    INVENTORY_TYPE_ROBE: 20,
    INVENTORY_TYPE_MAIN_HAND: 21,
    INVENTORY_TYPE_OFF_HAND: 22
  };

  const PARTS = {
    "Face": "face",
    "Skin Color": "skin",
    "Hair Style": "hairStyle",
    "Hair Color": "hairColor",
    "Facial Hair": "facialStyle",
    "Mustache": "facialStyle",
    "Beard": "facialStyle",
    "Sideburns": "facialStyle",
    "Piercings": "facialStyle"
  };

  function loadScript(src) {
    return new Promise(function (resolve, reject) {
      const s = document.createElement("script");
      s.src = src;
      s.onload = resolve;
      s.onerror = reject;
      document.head.appendChild(s);
    });
  }

  function fetchJSON(path) {
    return fetch(CONTENT + path, { credentials: "same-origin" }).then(function (res) {
      if (!res.ok) throw new Error(path);
      return res.json();
    });
  }

  function choiceId(part, idx) {
    if (!part || !part.Choices) return undefined;
    const c = part.Choices[idx];
    return c ? c.Id : undefined;
  }

  function characterOptions(ch, full) {
    const options = full.Options || [];
    const out = [];
    options.forEach(function (part) {
      const key = PARTS[part.Name];
      let id;
      if (key) id = choiceId(part, ch[key]);
      if (id === undefined && part.Choices && part.Choices.length) {
        id = part.Choices[0].Id;
      }
      if (id === undefined) return;
      out.push({ optionId: part.Id, choiceId: id });
    });
    return out;
  }

  function mapDisplay(entry, displayId) {
    const a = entry || displayId;
    if (!a || !displayId) return Promise.resolve(displayId);
    return fetch("/armory/display/" + a + "/" + displayId, { credentials: "same-origin" }).then(function (res) {
      if (!res.ok) return displayId;
      return res.json().then(function (data) {
        const n = data && (data.newDisplayId || (data.data && data.data.newDisplayId));
        return n || displayId;
      });
    }).catch(function () {
      return displayId;
    });
  }

  function resolveItem(slot, displayId, invType) {
    const remap = { 5: 20, 16: 21, 18: 22 };
    const candidates = [slot];
    if (invType && invType !== slot) candidates.push(invType);
    if (remap[slot] && candidates.indexOf(remap[slot]) < 0) candidates.push(remap[slot]);

    function tryArmor(i) {
      if (i >= candidates.length) {
        return fetchJSON("meta/item/" + displayId + ".json").then(function () {
          return [remap[slot] || slot, displayId];
        }).catch(function () {
          return [slot, displayId];
        });
      }
      const s = candidates[i];
      return fetchJSON("meta/armor/" + s + "/" + displayId + ".json").then(function () {
        // Cloak meta is stored as InventoryType 16; keep paperdoll slot 15.
        if (slot === 15) return [15, displayId];
        return [s, displayId];
      }).catch(function () {
        return tryArmor(i + 1);
      });
    }
    return tryArmor(0);
  }

  function hideFallback() {
    const fb = root.querySelector(".armory-model-fallback");
    if (fb) fb.hidden = true;
  }

  function fail() {
    const fb = root.querySelector(".armory-model-fallback");
    if (fb) {
      fb.hidden = false;
      fb.textContent = "3D model unavailable";
    }
  }

  loadScript(CONTENT + "viewer/viewer.min.js").then(function () {
    if (typeof ZamModelViewer !== "function") throw new Error("viewer");
    const race = character.race;
    const gender = character.gender;
    const id = race * 2 - 1 + gender;
    return fetchJSON("meta/charactercustomization/" + id + ".json").then(function (full) {
      const data = full.data || full;
      const items = character.items || [];
      return Promise.all(items.map(function (pair) {
        const slot = pair[0];
        const displayId = pair[1];
        const entry = pair[2] || displayId;
        const invType = pair[3] || 0;
        return mapDisplay(entry, displayId).then(function (mapped) {
          return resolveItem(slot, mapped, invType);
        });
      })).then(function (resolved) {
        const opts = {
          type: 2,
          contentPath: CONTENT,
          container: window.jQuery(root),
          aspect: 0.75,
          hd: true,
          items: resolved,
          models: { id: id, type: 16 },
          charCustomization: { options: characterOptions(character, data) }
        };
        hideFallback();
        // eslint-disable-next-line no-undef
        new ZamModelViewer(opts);
      });
    });
  }).catch(function () {
    fail();
  });
})();
