"use strict";

var test = require("node:test");
var assert = require("node:assert/strict");

function eventElement(attributes) {
  var listeners = Object.create(null);
  var attrs = Object.assign({}, attributes || {});
  return {
    addEventListener: function (eventName, handler) {
      listeners[eventName] = listeners[eventName] || [];
      listeners[eventName].push(handler);
    },
    dispatch: function (eventName, overrides) {
      var event = Object.assign({
        target: this,
        preventDefault: function () {},
      }, overrides || {});
      (listeners[eventName] || []).slice().forEach(function (handler) {
        handler(event);
      });
    },
    getAttribute: function (name) {
      return attrs[name] || "";
    },
    listenerCount: function (eventName) {
      return (listeners[eventName] || []).length;
    },
  };
}

var effects = [];
var documentElement = { dataset: {} };

global.window = {
  TRAIN_APP_CONFIG: {},
  location: {
    href: "https://vilciens.kontrole.info/feed",
    pathname: "/feed",
    search: "",
    hash: "",
  },
  history: { replaceState: function () {} },
  localStorage: { getItem: function () { return null; } },
  ArrowJS: {
    reactive: function (value) {
      return new Proxy(value, {
        set: function (target, property, nextValue) {
          target[property] = nextValue;
          effects.forEach(function (effect) { effect(); });
          return true;
        },
      });
    },
    html: function () {
      var values = Array.prototype.slice.call(arguments, 1);
      var valueReader = values.find(function (value) { return typeof value === "function"; });
      return function (node) {
        var effect = function () {
          node.arrowRenderedHTML = valueReader ? valueReader() : "";
        };
        effects.push(effect);
        effect();
      };
    },
  },
};
global.document = {
  documentElement: documentElement,
  getElementById: function () { return null; },
  addEventListener: function () {},
};

delete require.cache[require.resolve("./app.js")];
var arrowApp = require("./app.js");

test("Arrow page slots update content and expose the live route marker", function () {
  var slot = { innerHTML: "stale" };

  assert.equal(arrowApp.__test__.updatePageSlot(slot, "publicDashboardStatusHTML", "first"), true);
  assert.equal(slot.arrowRenderedHTML, "first");
  assert.equal(slot.__trainArrowSlot, "publicDashboardStatusHTML");
  assert.equal(documentElement.dataset.trainUi, "arrow");

  arrowApp.__test__.updatePageSlot(slot, "publicDashboardStatusHTML", "second");
  assert.equal(slot.arrowRenderedHTML, "second");
});

test("Mini App Arrow rerenders keep one DOM listener and one action per click", function () {
  var toggle = eventElement({ "data-action": "toggle-checkin-dropdown" });
  var root = {
    querySelector: function () {
      return null;
    },
    querySelectorAll: function (selector) {
      return selector === "[data-action='toggle-checkin-dropdown']" ? [toggle] : [];
    },
  };

  arrowApp.__test__.resetState({
    authenticated: true,
    stationDepartures: [{ id: "train-1" }],
    checkInDropdownOpen: false,
  });

  arrowApp.__test__.bindMiniAppEvents(root);
  arrowApp.__test__.bindMiniAppEvents(root);

  assert.equal(toggle.listenerCount("click"), 1);
  toggle.dispatch("click");
  assert.equal(arrowApp.__test__.getState().checkInDropdownOpen, true);
});

test("same-route Arrow shell replacement reuses the Leaflet host and listeners", function () {
  var originalGetElementById = global.document.getElementById;
  var originalLeaflet = global.window.L;
  var containers = Object.create(null);
  var stableHost = { parentNode: null };
  var stableMap = { id: "leaflet-map" };
  var mapBuilds = 0;
  var listenerRegistrations = 0;

  function mapContainer(id) {
    return {
      id: id,
      child: null,
      appendChild: function (node) {
        if (node.parentNode && node.parentNode !== this && typeof node.parentNode.removeChild === "function") {
          node.parentNode.removeChild(node);
        }
        this.child = node;
        node.parentNode = this;
      },
      removeChild: function (node) {
        if (this.child === node) {
          this.child = null;
        }
        if (node.parentNode === this) {
          node.parentNode = null;
        }
      },
    };
  }

  var firstShell = mapContainer("mini-network-map");
  containers["mini-network-map"] = firstShell;
  global.document.getElementById = function (id) {
    return containers[id] || null;
  };
  global.window.L = {};

  var controller = arrowApp.__test__.createMapController();
  controller.ensureHost = function () {
    this.hostEl = stableHost;
    return stableHost;
  };
  controller.ensureMap = function () {
    if (!this.map) {
      mapBuilds += 1;
      listenerRegistrations += 1;
      this.map = stableMap;
    }
    return this.map;
  };
  controller.buildConfig = function () {
    return {
      bounds: [[56.95, 24.1]],
      modelKey: "network-map",
      viewKey: "network:mini-app",
    };
  };
  controller.updateLayers = function () {};
  controller.scheduleLayout = function () {};

  try {
    controller.sync("mini-network-map", { liveOnly: true });
    assert.equal(firstShell.child, stableHost);

    firstShell.removeChild(stableHost);
    var replacementShell = mapContainer("mini-network-map");
    containers["mini-network-map"] = replacementShell;
    controller.sync("mini-network-map", { liveOnly: true });

    assert.equal(replacementShell.child, stableHost);
    assert.equal(controller.hostEl, stableHost);
    assert.equal(controller.map, stableMap);
    assert.equal(mapBuilds, 1);
    assert.equal(listenerRegistrations, 1);
  } finally {
    global.document.getElementById = originalGetElementById;
    global.window.L = originalLeaflet;
  }
});
