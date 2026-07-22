(function () {
  "use strict";

  var body = document.body;
  var text = {
    on: body.dataset.textOn,
    off: body.dataset.textOff,
    unknown: body.dataset.textUnknown,
    turnOn: body.dataset.textTurnOn,
    turnOff: body.dataset.textTurnOff,
    connected: body.dataset.textConnected,
    disconnected: body.dataset.textDisconnected,
    stale: body.dataset.textStale,
    liveInterrupted: body.dataset.textLiveInterrupted,
    commandSent: body.dataset.textCommandSent,
    commandAccepted: body.dataset.textCommandAccepted,
    commandFailed: body.dataset.textCommandFailed,
    unsupported: body.dataset.textUnsupported,
    deviceMissing: body.dataset.textDeviceMissing,
    stmUnavailable: body.dataset.textStmUnavailable,
    commandTimeout: body.dataset.textCommandTimeout,
    reloading: body.dataset.textReloading,
    reloadFailed: body.dataset.textReloadFailed,
    favourite: body.dataset.textFavourite,
    removeFavourite: body.dataset.textRemoveFavourite
  };
  var lastRevision = 0;
  var favouriteKey = "phc-bridge:favourites:v1";
  var favouriteDrag = null;

  // Live state kept current by SSE, so a client-side page swap can re-apply the
  // authoritative state to freshly server-rendered (possibly prefetched) markup.
  var deviceStates = {};
  var connectionState = { status: "disconnected", stale: true };
  var prefetchCache = {};
  var events = null;

  function deviceRoot(deviceID) {
    var nodes = document.querySelectorAll("[data-device-id]");
    for (var i = 0; i < nodes.length; i += 1) {
      if (nodes[i].dataset.deviceId === deviceID) return nodes[i];
    }
    return null;
  }

  function setPower(deviceID, power) {
    var root = deviceRoot(deviceID);
    if (!root) return;
    var output = root.querySelector('[data-role="power-state"]');
    var button = root.querySelector('[data-role="power-command"]');
    root.dataset.power = power;
    if (output) output.textContent = power === "on" ? text.on : power === "off" ? text.off : text.unknown;
    if (button) {
      button.setAttribute("aria-pressed", power === "on" ? "true" : "false");
      button.textContent = power === "on" ? text.turnOff : text.turnOn;
    }
  }

  function setConnection(status, stale, override) {
    var region = document.querySelector('[data-role="connection-region"]');
    var output = document.querySelector('[data-role="connection-status"]');
    if (!region || !output) return;
    var key = status === "connected" ? (stale ? "stale" : "connected") : "disconnected";
    region.className = "connection-bar is-" + key;
    output.textContent = override || (key === "connected" ? text.connected : key === "stale" ? text.stale : text.disconnected);
  }

  function applySnapshot(snapshot) {
    lastRevision = snapshot.revision || 0;
    connectionState = { status: snapshot.connection, stale: snapshot.stale };
    deviceStates = {};
    setConnection(snapshot.connection, snapshot.stale);
    Object.keys(snapshot.devices || {}).forEach(function (deviceID) {
      deviceStates[deviceID] = snapshot.devices[deviceID].power;
      setPower(deviceID, snapshot.devices[deviceID].power);
    });
  }

  // Re-apply the known live state to the current DOM after a navigation swap.
  function applyLiveState() {
    setConnection(connectionState.status, connectionState.stale);
    Object.keys(deviceStates).forEach(function (deviceID) { setPower(deviceID, deviceStates[deviceID]); });
  }

  function refreshSnapshot() {
    fetch("/api/v1/state", { headers: { "Accept": "application/json" } })
      .then(function (response) { if (!response.ok) throw new Error(); return response.json(); })
      .then(applySnapshot)
      .catch(function () { setConnection("disconnected", true, text.liveInterrupted); });
  }

  function updateRevision(revision) {
    if (lastRevision && revision > lastRevision + 1) {
      refreshSnapshot();
      return false;
    }
    lastRevision = Math.max(lastRevision, revision || 0);
    return true;
  }

  function transientStatus(root, message, isError) {
    var output = root && root.querySelector('[data-role="command-status"]');
    if (!output) return;
    output.textContent = message;
    output.classList.toggle("is-error", Boolean(isError));
    window.setTimeout(function () {
      if (output.textContent === message) output.textContent = "";
    }, 4000);
  }

  function sendCommand(button) {
    var root = button.closest("[data-device-id]");
    if (!root || button.disabled) return;
    if (button.dataset.confirm && !window.confirm(button.dataset.confirm)) return;
    button.disabled = true;
    fetch("/api/v1/devices/" + encodeURIComponent(root.dataset.deviceId) + "/commands", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Accept": "application/json" },
      body: JSON.stringify({ action: button.dataset.action })
    }).then(function (response) {
      if (!response.ok) throw new Error(commandError(response.status));
      return response.json();
    }).then(function () {
      var stateCapable = Boolean(root.querySelector('[data-role="power-state"]'));
      transientStatus(root, stateCapable ? text.commandAccepted : text.commandSent, false);
    }).catch(function (error) {
      transientStatus(root, error.message || text.commandFailed, true);
    }).finally(function () { button.disabled = false; });
  }

  function commandError(status) {
    if (status === 404) return text.deviceMissing;
    if (status === 409) return text.unsupported;
    if (status === 503) return text.stmUnavailable;
    if (status === 504) return text.commandTimeout;
    return text.commandFailed;
  }

  function loadFavourites() {
    try {
      var value = JSON.parse(window.localStorage.getItem(favouriteKey) || "[]");
      return Array.isArray(value) ? value.filter(function (id) { return typeof id === "string"; }) : [];
    } catch (_) { return []; }
  }

  function renderFavourites(ids) {
    var list = document.querySelector('[data-role="favourites-list"]');
    var valid = ids.slice();
    if (list) {
      var items = Array.from(document.querySelectorAll("[data-favorite-item]"));
      valid = [];
      ids.forEach(function (id) {
        var item = items.find(function (candidate) { return candidate.dataset.favoriteItem === id; });
        if (!item) return;
        item.hidden = false;
        list.appendChild(item);
        valid.push(id);
      });
      items.forEach(function (item) {
        if (valid.indexOf(item.dataset.favoriteItem) < 0) item.hidden = true;
      });
    }
    document.querySelectorAll("[data-favorite-toggle]").forEach(function (button) {
      var selected = valid.indexOf(button.dataset.favoriteToggle) >= 0;
      button.setAttribute("aria-pressed", selected ? "true" : "false");
      button.setAttribute("aria-label", selected ? text.removeFavourite : text.favourite);
      button.title = selected ? text.removeFavourite : text.favourite;
      button.querySelector("span").innerHTML = selected ? "&#9733;" : "&#9734;";
    });
    document.querySelectorAll("[data-favorite-move]").forEach(function (button) {
      var item = button.closest("[data-favorite-item]");
      var index = item ? valid.indexOf(item.dataset.favoriteItem) : -1;
      button.disabled = index < 0 || (button.dataset.favoriteMove === "up" ? index === 0 : index === valid.length - 1);
    });
    var empty = document.querySelector('[data-role="favourites-empty"]');
    if (empty) empty.hidden = valid.length > 0;
    if (list && valid.length !== ids.length) window.localStorage.setItem(favouriteKey, JSON.stringify(valid));
    return valid;
  }

  function toggleFavourite(button) {
    var ids = loadFavourites();
    var id = button.dataset.favoriteToggle;
    var index = ids.indexOf(id);
    if (index >= 0) ids.splice(index, 1); else ids.push(id);
    window.localStorage.setItem(favouriteKey, JSON.stringify(ids));
    renderFavourites(ids);
  }

  function moveFavourite(button) {
    var item = button.closest("[data-favorite-item]");
    if (!item) return;
    var ids = loadFavourites();
    var index = ids.indexOf(item.dataset.favoriteItem);
    var target = index + (button.dataset.favoriteMove === "up" ? -1 : 1);
    if (index < 0 || target < 0 || target >= ids.length) return;
    var moved = ids[index];
    ids[index] = ids[target];
    ids[target] = moved;
    window.localStorage.setItem(favouriteKey, JSON.stringify(ids));
    renderFavourites(ids);
  }

  function visibleFavouriteIDs(list) {
    return Array.from(list.querySelectorAll("[data-favorite-item]"))
      .filter(function (item) { return !item.hidden; })
      .map(function (item) { return item.dataset.favoriteItem; });
  }

  function persistFavouriteDOMOrder(list) {
    var ids = visibleFavouriteIDs(list);
    window.localStorage.setItem(favouriteKey, JSON.stringify(ids));
    renderFavourites(ids);
  }

  function startFavouriteDrag(handle, event) {
    if (event.button !== undefined && event.button !== 0) return;
    var item = handle.closest("[data-favorite-item]");
    var list = item && item.closest('[data-role="favourites-list"]');
    if (!item || !list || item.hidden) return;
    favouriteDrag = { item: item, list: list, handle: handle, pointerID: event.pointerId };
    item.classList.add("is-reordering");
    handle.setPointerCapture(event.pointerId);
    event.preventDefault();
  }

  function moveFavouriteDrag(event) {
    if (!favouriteDrag || event.pointerId !== favouriteDrag.pointerID) return;
    var target = document.elementFromPoint(event.clientX, event.clientY);
    target = target && target.closest("[data-favorite-item]");
    if (!target || target === favouriteDrag.item || target.hidden || target.parentElement !== favouriteDrag.list) return;

    var rect = target.getBoundingClientRect();
    var sameRow = event.clientY >= rect.top && event.clientY <= rect.bottom;
    var insertAfter = sameRow ? event.clientX > rect.left + rect.width / 2 : event.clientY > rect.top + rect.height / 2;
    favouriteDrag.list.insertBefore(favouriteDrag.item, insertAfter ? target.nextSibling : target);
    event.preventDefault();
  }

  function finishFavouriteDrag(event) {
    if (!favouriteDrag || event.pointerId !== favouriteDrag.pointerID) return;
    var drag = favouriteDrag;
    favouriteDrag = null;
    drag.item.classList.remove("is-reordering");
    if (drag.handle.hasPointerCapture(event.pointerId)) drag.handle.releasePointerCapture(event.pointerId);
    persistFavouriteDOMOrder(drag.list);
    event.preventDefault();
  }

  function reloadProject(button) {
    var output = document.querySelector('[data-role="reload-status"]');
    button.disabled = true;
    if (output) { output.textContent = text.reloading; output.classList.remove("is-error"); }
    fetch("/api/v1/project/reload", {
      method: "POST", headers: { "Content-Type": "application/json", "Accept": "application/json" }, body: "{}"
    }).then(function (response) {
      if (!response.ok) throw new Error(text.reloadFailed);
      window.location.reload();
    }).catch(function (error) {
      if (output) { output.textContent = error.message; output.classList.add("is-error"); }
      button.disabled = false;
    });
  }

  // --- Client-side navigation (swaps only <main>; the Go template still renders
  //     it, so it remains the sole structural renderer). ---

  function isAppPage(pathname) {
    return pathname === "/" || pathname === "/settings" || pathname === "/acknowledgments" ||
      pathname.indexOf("/floors/") === 0;
  }

  function fetchPage(url) {
    var pending = fetch(url, { headers: { "Accept": "text/html" }, credentials: "same-origin" })
      .then(function (response) { if (!response.ok) throw new Error("navigation " + response.status); return response.text(); })
      .then(function (html) {
        var doc = new DOMParser().parseFromString(html, "text/html");
        var main = doc.querySelector("main");
        if (!main) throw new Error("no main element");
        return { mainHTML: main.innerHTML, title: doc.title, pathname: new URL(url, window.location.origin).pathname };
      });
    prefetchCache[url] = pending;
    return pending;
  }

  function prefetch(url) {
    if (!prefetchCache[url]) fetchPage(url).catch(function () { delete prefetchCache[url]; });
  }

  function updateNav(pathname) {
    document.querySelectorAll(".site-header nav a").forEach(function (link) {
      var linkPath = new URL(link.href, window.location.origin).pathname;
      var current = (linkPath === "/" && (pathname === "/" || pathname.indexOf("/floors/") === 0)) ||
        (linkPath !== "/" && linkPath === pathname);
      if (current) link.setAttribute("aria-current", "page"); else link.removeAttribute("aria-current");
    });
  }

  function navigateTo(url, push) {
    (prefetchCache[url] || fetchPage(url)).then(function (page) {
      var main = document.querySelector("main");
      if (main) main.innerHTML = page.mainHTML;
      if (page.title) document.title = page.title;
      if (push) window.history.pushState({}, "", url);
      updateNav(page.pathname);
      window.scrollTo(0, 0);
      initPage(true);
    }).catch(function () {
      delete prefetchCache[url];
      window.location.href = url; // fall back to a full navigation
    });
  }

  // Per-page (re)initialisation; run at load and after every navigation swap.
  function initPage(applyState) {
    renderFavourites(loadFavourites());
    document.querySelectorAll("details[data-category]").forEach(function (details) {
      var key = "phc-bridge:category:" + window.location.pathname + ":" + details.dataset.category;
      var saved = window.localStorage.getItem(key);
      if (saved !== null) details.open = saved === "open";
      details.addEventListener("toggle", function () {
        window.localStorage.setItem(key, details.open ? "open" : "closed");
      });
    });
    if (applyState) applyLiveState();
  }

  function openEvents() {
    events = new EventSource("/api/v1/events");
    events.addEventListener("snapshot", function (event) { applySnapshot(JSON.parse(event.data)); });
    events.addEventListener("state", function (event) {
      var value = JSON.parse(event.data);
      if (updateRevision(value.revision)) {
        deviceStates[value.deviceID] = value.power;
        setPower(value.deviceID, value.power);
      }
    });
    events.addEventListener("connection", function (event) {
      var value = JSON.parse(event.data);
      if (updateRevision(value.revision)) {
        connectionState = { status: value.status, stale: value.stale };
        setConnection(value.status, value.stale);
      }
    });
    events.addEventListener("project", function () { window.location.reload(); });
    events.onerror = function () { setConnection("disconnected", true, text.liveInterrupted); };
  }

  // --- Wiring ---

  document.addEventListener("click", function (event) {
    var command = event.target.closest("[data-action]");
    if (command) { sendCommand(command); return; }
    var favourite = event.target.closest("[data-favorite-toggle]");
    if (favourite) { toggleFavourite(favourite); return; }
    var move = event.target.closest("[data-favorite-move]");
    if (move) { moveFavourite(move); return; }
    var reload = event.target.closest("[data-project-reload]");
    if (reload) { reloadProject(reload); return; }

    // Internal navigation → swap in place instead of a full reload.
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey ||
      event.shiftKey || event.altKey) return;
    var link = event.target.closest("a[href]");
    if (!link || link.hasAttribute("download") || (link.target && link.target !== "_self")) return;
    var dest = new URL(link.href, window.location.origin);
    if (dest.origin !== window.location.origin || !isAppPage(dest.pathname)) return;
    event.preventDefault();
    navigateTo(dest.href, true);
  });

  document.addEventListener("pointerover", function (event) {
    var link = event.target.closest && event.target.closest("a[href]");
    if (!link) return;
    var dest = new URL(link.href, window.location.origin);
    if (dest.origin === window.location.origin && isAppPage(dest.pathname)) prefetch(dest.href);
  });

  document.addEventListener("pointerdown", function (event) {
    var handle = event.target.closest("[data-favorite-drag]");
    if (handle) startFavouriteDrag(handle, event);
  });
  document.addEventListener("pointermove", moveFavouriteDrag);
  document.addEventListener("pointerup", finishFavouriteDrag);
  document.addEventListener("pointercancel", finishFavouriteDrag);

  window.addEventListener("popstate", function () {
    if (isAppPage(window.location.pathname)) navigateTo(window.location.href, false);
  });

  // Close the SSE stream when the page is cached (bfcache) and reopen on
  // restore, so Back/Forward can restore instantly instead of full-reloading.
  window.addEventListener("pagehide", function () { if (events) { events.close(); events = null; } });
  window.addEventListener("pageshow", function () { if (!events) openEvents(); });

  initPage(false);
  openEvents();
}());
