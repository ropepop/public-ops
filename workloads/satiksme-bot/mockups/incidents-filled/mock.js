(function () {
  "use strict";

  var mockBaseTime = new Date();
  var localNickname = "Local Preview";
  var blueprints = [
    {
      id: "vehicle:bus-22-abrenes",
      subjectName: "Abrenes iela",
      lastReportName: "Autobuss 22 uz Lidostu",
      lastReporter: "Amber Scout 241",
      lastReportOffsetMin: 4,
      votes: { ongoing: 8, cleared: 1, userValue: "" },
      events: [
        { id: "event-1", name: "Autobuss 22 uz Lidostu", nickname: "Forest Signal 338", offsetMin: 17 },
        { id: "event-2", name: "Autobuss 22 uz Lidostu", nickname: "Copper Voyager 517", offsetMin: 11 },
        { id: "event-3", name: "Autobuss 22 uz Lidostu", nickname: "Amber Scout 241", offsetMin: 4 }
      ],
      comments: [
        { id: "comment-1", nickname: "Willow Beacon 284", body: "Kontrole pie vidējām durvīm, virzienā uz centru.", offsetMin: 9 },
        { id: "comment-2", nickname: "Granite Courier 611", body: "Pārbaudīja biļetes diviem pasažieriem un izkāpa pie Centrāltirgus.", offsetMin: 5 }
      ]
    },
    {
      id: "stop:3009",
      subjectName: "Centrāltirgus",
      lastReportName: "Pieturas kontrole",
      lastReporter: "Silver Rider 452",
      lastReportOffsetMin: 7,
      votes: { ongoing: 5, cleared: 2, userValue: "" },
      events: [
        { id: "event-4", name: "Pieturas kontrole", nickname: "North Falcon 167", offsetMin: 21 },
        { id: "event-5", name: "Pieturas kontrole", nickname: "Silver Rider 452", offsetMin: 7 }
      ],
      comments: [
        { id: "comment-3", nickname: "Quiet Pilot 333", body: "Kontrolieri stāv pie tuneļa ieejas no tirgus puses.", offsetMin: 6 },
        { id: "comment-4", nickname: "Bright Atlas 908", body: "Pašlaik veidojas neliela rinda pie validatora.", offsetMin: 3 }
      ]
    },
    {
      id: "vehicle:tram-7-13janvara",
      subjectName: "13. janvāra iela",
      lastReportName: "Tramvajs 7 uz Ķengaragu",
      lastReporter: "Harbor Pilot 604",
      lastReportOffsetMin: 12,
      votes: { ongoing: 4, cleared: 3, userValue: "" },
      events: [
        { id: "event-6", name: "Tramvajs 7 uz Ķengaragu", nickname: "Cloud Lantern 725", offsetMin: 24 },
        { id: "event-7", name: "Tramvajs 7 uz Ķengaragu", nickname: "Harbor Pilot 604", offsetMin: 12 }
      ],
      comments: [
        { id: "comment-5", nickname: "Mellow Voyager 417", body: "Kontrole vagonā tuvāk pirmās klases galam.", offsetMin: 10 }
      ]
    },
    {
      id: "vehicle:trol-15-zemitani",
      subjectName: "Zemitāni",
      lastReportName: "Trolejbuss 15 uz Ķīpsalu",
      lastReporter: "Copper Drifter 784",
      lastReportOffsetMin: 19,
      votes: { ongoing: 3, cleared: 1, userValue: "" },
      events: [
        { id: "event-8", name: "Trolejbuss 15 uz Ķīpsalu", nickname: "Cedar Watcher 256", offsetMin: 29 },
        { id: "event-9", name: "Trolejbuss 15 uz Ķīpsalu", nickname: "Copper Drifter 784", offsetMin: 19 }
      ],
      comments: [
        { id: "comment-6", nickname: "River Traveler 563", body: "Kontrolieri pārvietojas uz aizmuguri, pagaidām mierīgi.", offsetMin: 16 }
      ]
    }
  ];

  function timestamp(offsetMinutes) {
    return new Date(mockBaseTime.getTime() - (offsetMinutes * 60000)).toISOString();
  }

  function materializeIncident(blueprint) {
    return {
      id: blueprint.id,
      subjectName: blueprint.subjectName,
      lastReportName: blueprint.lastReportName,
      lastReporter: blueprint.lastReporter,
      lastReportAt: timestamp(blueprint.lastReportOffsetMin),
      votes: {
        ongoing: blueprint.votes.ongoing,
        cleared: blueprint.votes.cleared,
        userValue: blueprint.votes.userValue || ""
      },
      events: blueprint.events.map(function (item) {
        return {
          id: item.id,
          name: item.name,
          nickname: item.nickname,
          createdAt: timestamp(item.offsetMin)
        };
      }),
      comments: blueprint.comments.map(function (item) {
        return {
          id: item.id,
          nickname: item.nickname,
          body: item.body,
          createdAt: timestamp(item.offsetMin)
        };
      }),
      draft: ""
    };
  }

  var incidents = blueprints.map(materializeIncident);
  var selectedIncidentId = incidents[0] ? incidents[0].id : "";

  function byId(id) {
    return incidents.find(function (item) {
      return item.id === id;
    }) || null;
  }

  function escapeHtml(value) {
    return String(value || "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function pad(value) {
    return String(value).padStart(2, "0");
  }

  function clock(value) {
    var at = new Date(value);
    return pad(at.getHours()) + ":" + pad(at.getMinutes());
  }

  function relativeAge(value) {
    var diffSeconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000));
    if (diffSeconds < 60) {
      return "tikko";
    }
    if (diffSeconds < 3600) {
      return "pirms " + Math.floor(diffSeconds / 60) + " min";
    }
    if (diffSeconds < 86400) {
      return "pirms " + Math.floor(diffSeconds / 3600) + " h";
    }
    return "pirms " + Math.floor(diffSeconds / 86400) + " d";
  }

  function voteLabel(votes) {
    return "Kontrole: " + votes.ongoing + " · Nav kontrole: " + votes.cleared;
  }

  function setStatus(text) {
    var node = document.getElementById("status-pill");
    if (node) {
      node.textContent = text;
    }
  }

  function renderList() {
    var node = document.getElementById("incident-list");
    if (!node) {
      return;
    }
    node.innerHTML = incidents.map(function (item) {
      var selected = item.id === selectedIncidentId ? " selected-card" : "";
      return (
        '<button class="detail-card incident-card' + selected + '" data-select-incident="' + escapeHtml(item.id) + '">' +
          '<div class="station-card-header">' +
            '<h3>' + escapeHtml(item.subjectName) + '</h3>' +
            '<span class="station-selected-pill">' + escapeHtml(relativeAge(item.lastReportAt)) + '</span>' +
          '</div>' +
          '<div class="meta"><span>' + escapeHtml(item.lastReportName) + '</span><span>Pēdējais: ' + escapeHtml(item.lastReporter) + '</span></div>' +
          '<div class="meta"><span>' + escapeHtml(voteLabel(item.votes)) + '</span><span>' + item.comments.length + ' komentāri</span></div>' +
        '</button>'
      );
    }).join("");
  }

  function renderComments(item) {
    if (!item.comments.length) {
      return '<p class="empty">Komentāru vēl nav.</p>';
    }
    return item.comments.map(function (comment) {
      return (
        '<article class="favorite-card">' +
          '<h3>' + escapeHtml(comment.nickname) + '</h3>' +
          '<div class="meta"><span>' + escapeHtml(clock(comment.createdAt)) + '</span></div>' +
          '<p>' + escapeHtml(comment.body) + '</p>' +
        '</article>'
      );
    }).join("");
  }

  function renderEvents(item) {
    if (!item.events.length) {
      return '<p class="empty">Aktivitātes vēl nav.</p>';
    }
    return item.events.map(function (event) {
      return (
        '<article class="favorite-card">' +
          '<h3>' + escapeHtml(event.name) + '</h3>' +
          '<div class="meta"><span>' + escapeHtml(event.nickname) + '</span><span>' + escapeHtml(clock(event.createdAt)) + '</span></div>' +
        '</article>'
      );
    }).join("");
  }

  function renderDetail() {
    var node = document.getElementById("incident-detail");
    var item = byId(selectedIncidentId);
    var ongoingClass = item && item.votes.userValue === "ONGOING" ? "button-primary" : "button-secondary";
    var clearedClass = item && item.votes.userValue === "CLEARED" ? "button-primary" : "button-secondary";
    if (!node || !item) {
      return;
    }
    node.innerHTML =
      '<div class="stack">' +
        '<div class="badge">' + escapeHtml(item.subjectName) + '</div>' +
        '<section class="detail-card">' +
          '<h3>' + escapeHtml(item.lastReportName) + '</h3>' +
          '<div class="meta"><span>Pēdējais: ' + escapeHtml(item.lastReporter) + '</span><span>' + escapeHtml(relativeAge(item.lastReportAt)) + '</span></div>' +
          '<div class="button-row">' +
            '<button class="button ' + ongoingClass + '" data-vote="ONGOING">Kontrole</button>' +
            '<button class="button ' + clearedClass + '" data-vote="CLEARED">Nav kontrole</button>' +
          '</div>' +
          '<p class="report-note">' + escapeHtml(voteLabel(item.votes)) + '</p>' +
          '<p class="report-note">Šis ir atsevišķs lokālais makets. Nekas netiek sūtīts uz īsto sistēmu.</p>' +
          '<div class="field">' +
            '<label for="comment-body">Anonīms komentārs</label>' +
            '<textarea id="comment-body" placeholder="Pievieno īsu komentāru">' + escapeHtml(item.draft) + '</textarea>' +
          '</div>' +
          '<div class="button-row">' +
            '<button class="button button-primary" id="comment-submit">Pievienot komentāru</button>' +
          '</div>' +
        '</section>' +
        '<section class="detail-card"><h3>Aktivitāte</h3><div class="card-list">' + renderEvents(item) + '</div></section>' +
        '<section class="detail-card"><h3>Komentāri</h3><div class="card-list">' + renderComments(item) + '</div></section>' +
      '</div>';
  }

  function render() {
    renderList();
    renderDetail();
  }

  function recordVote(value) {
    var item = byId(selectedIncidentId);
    if (!item) {
      return;
    }
    var previous = item.votes.userValue;
    if (previous === "ONGOING") {
      item.votes.ongoing = Math.max(0, item.votes.ongoing - 1);
    }
    if (previous === "CLEARED") {
      item.votes.cleared = Math.max(0, item.votes.cleared - 1);
    }
    if (value === "ONGOING") {
      item.votes.ongoing += 1;
    }
    if (value === "CLEARED") {
      item.votes.cleared += 1;
    }
    item.votes.userValue = value;
    item.lastReportName = value === "ONGOING" ? "Balsojums: stāv" : "Balsojums: nestāv";
    item.lastReporter = localNickname;
    item.lastReportAt = new Date().toISOString();
    item.events.push({
      id: "event-local-" + Date.now(),
      name: item.lastReportName,
      nickname: localNickname,
      createdAt: item.lastReportAt
    });
    setStatus("Lokālais balsojums atjaunināts");
    render();
  }

  function recordComment() {
    var item = byId(selectedIncidentId);
    var input = document.getElementById("comment-body");
    var body = input ? String(input.value || "").trim() : "";
    if (!item || !body) {
      setStatus("Komentārs ir vajadzīgs");
      return;
    }
    var createdAt = new Date().toISOString();
    item.comments.push({
      id: "comment-local-" + Date.now(),
      nickname: localNickname,
      body: body,
      createdAt: createdAt
    });
    item.events.push({
      id: "event-comment-" + Date.now(),
      name: "Anonīms komentārs",
      nickname: localNickname,
      createdAt: createdAt
    });
    item.lastReportName = "Anonīms komentārs";
    item.lastReporter = localNickname;
    item.lastReportAt = createdAt;
    item.draft = "";
    setStatus("Lokālais komentārs pievienots");
    render();
  }

  document.addEventListener("click", function (event) {
    var selectButton = event.target.closest("[data-select-incident]");
    var voteButton = event.target.closest("[data-vote]");
    if (selectButton) {
      selectedIncidentId = selectButton.getAttribute("data-select-incident");
      setStatus("Lokālais incidents makets");
      render();
      return;
    }
    if (voteButton) {
      recordVote(voteButton.getAttribute("data-vote"));
      return;
    }
    if (event.target.id === "comment-submit") {
      recordComment();
    }
  });

  document.addEventListener("input", function (event) {
    if (event.target.id !== "comment-body") {
      return;
    }
    var item = byId(selectedIncidentId);
    if (item) {
      item.draft = event.target.value;
    }
  });

  render();
})();
