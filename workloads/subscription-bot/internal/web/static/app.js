(function () {
  const config = window.SUBSCRIPTION_APP_CONFIG || { apiBasePath: "", routeBasePath: "", publicBaseURL: "", mode: "app" };
  const appRoot = document.getElementById("app");
  const tg = telegramWebApp();
  const query = new URLSearchParams(window.location.search);

  const state = {
    loading: true,
    syncing: false,
    bootstrapped: false,
    error: "",
    notice: "",
    bootstrap: null,
    launchRequest: readLaunchParams(),
    section: config.mode === "admin" ? "admin" : "plans",
    defaultSection: config.mode === "admin" ? "admin" : "plans",
    adminView: config.mode === "admin" ? "overview" : "",
    defaultAdminView: config.mode === "admin" ? "overview" : "",
    selectedPlanId: query.get("plan_id") || "",
    selectedInvoiceId: query.get("invoice_id") || "",
    selectedServiceCode: "",
    joinInviteCode: query.get("invite_code") || "",
    supportMessage: "",
    generatedInvite: null,
    generatedInviteLaunch: null,
    planDataLoading: false,
    planData: null,
    invoiceActions: {},
    mainButtonAction: "",
    mainButtonValue: "",
    admin: {
      loading: false,
      overview: null,
      tickets: [],
      issues: [],
      recentPlans: [],
      reimbursements: [],
      alerts: [],
      entries: [],
    },
    createForm: {
      totalPrice: "",
      seatLimit: "2",
      renewalDate: defaultRenewalDate(),
      accessMode: "",
      sharingPolicyAck: true,
    },
    quote: {
      asset: "",
      network: "",
    },
    blockForm: {
      telegramId: "",
      reason: "",
    },
  };

  setupTelegram();

  appRoot.addEventListener("click", onClick);
  appRoot.addEventListener("submit", onSubmit);
  appRoot.addEventListener("change", onChange);
  appRoot.addEventListener("input", onInput);

  refreshAll();

  function telegramWebApp() {
    return window.Telegram && window.Telegram.WebApp ? window.Telegram.WebApp : null;
  }

  function setupTelegram() {
    if (!tg) {
      return;
    }
    try {
      tg.ready();
      tg.expand();
      if (typeof tg.enableClosingConfirmation === "function") {
        tg.enableClosingConfirmation();
      }
    } catch (_) {
      // Ignore Telegram WebApp capability issues outside Telegram.
    }
    applyTelegramTheme();
    applyViewportMetrics();
    if (typeof tg.onEvent === "function") {
      tg.onEvent("themeChanged", applyTelegramTheme);
      tg.onEvent("viewportChanged", applyViewportMetrics);
      tg.onEvent("mainButtonClicked", onTelegramMainButton);
      tg.onEvent("backButtonClicked", onTelegramBackButton);
    }
  }

  function applyTelegramTheme() {
    const root = document.documentElement;
    const params = (tg && tg.themeParams) || {};
    const values = {
      "--tg-bg-color": params.bg_color || "#0b1220",
      "--tg-secondary-bg-color": params.secondary_bg_color || "#111c2d",
      "--tg-surface-color": params.section_bg_color || params.secondary_bg_color || "#111c2d",
      "--tg-card-color": params.secondary_bg_color || "#111c2d",
      "--tg-text-color": params.text_color || "#f3f7ff",
      "--tg-hint-color": params.hint_color || "#8fa4c7",
      "--tg-link-color": params.link_color || "#63a4ff",
      "--tg-button-color": params.button_color || "#2e7cf6",
      "--tg-button-text-color": params.button_text_color || "#ffffff",
      "--tg-destructive-color": "#ff6b6b",
      "--tg-border-color": params.section_separator_color || "rgba(143, 164, 199, 0.22)",
      "--tg-accent-soft": rgba(params.button_color || "#2e7cf6", 0.16),
    };
    Object.keys(values).forEach(function (key) {
      root.style.setProperty(key, values[key]);
    });
    root.dataset.telegramTheme = params.bg_color && isLightColor(params.bg_color) ? "light" : "dark";
  }

  function applyViewportMetrics() {
    if (!tg) {
      return;
    }
    const root = document.documentElement;
    if (typeof tg.viewportStableHeight === "number" && tg.viewportStableHeight > 0) {
      root.style.setProperty("--tg-viewport-height", `${tg.viewportStableHeight}px`);
    }
    if (typeof tg.viewportHeight === "number" && tg.viewportHeight > 0) {
      root.style.setProperty("--tg-viewport-live-height", `${tg.viewportHeight}px`);
    }
  }

  function readLaunchParams() {
    const hash = new URLSearchParams((window.location.hash || "").replace(/^#/, ""));
    const unsafe = (tg && tg.initDataUnsafe) || {};
    return {
      startapp: stringOrEmpty(query.get("startapp") || unsafe.start_param || hash.get("tgWebAppStartParam")),
      section: stringOrEmpty(query.get("section")),
      admin_view: stringOrEmpty(query.get("admin_view")),
      plan_id: stringOrEmpty(query.get("plan_id")),
      invoice_id: stringOrEmpty(query.get("invoice_id")),
      invite_code: stringOrEmpty(query.get("invite_code")),
      mode: config.mode || "app",
    };
  }

  function bootstrapPath() {
    const values = new URLSearchParams();
    Object.keys(state.launchRequest).forEach(function (key) {
      const value = stringOrEmpty(state.launchRequest[key]);
      if (value) {
        values.set(key, value);
      }
    });
    const encoded = values.toString();
    return encoded ? `/bootstrap?${encoded}` : "/bootstrap";
  }

  function normalizedPrefix(value) {
    return stringOrEmpty(value).replace(/\/+$/, "");
  }

  function apiBasePath() {
    return normalizedPrefix(config.apiBasePath || config.basePath || "");
  }

  function routeBasePath() {
    return normalizedPrefix(config.routeBasePath !== undefined ? config.routeBasePath : (config.basePath || ""));
  }

  function routePath(path) {
    const cleanPath = path && path.charAt(0) === "/" ? path : `/${path || ""}`;
    return `${routeBasePath()}${cleanPath}`;
  }

  function launcherPath() {
    return routeBasePath() || "/";
  }

  function apiPath(path) {
    return `${apiBasePath()}/api/v1${path}`;
  }

  function request(path, options) {
    const settings = options || {};
    const requestOptions = {
      method: settings.method || "GET",
      credentials: "include",
      headers: Object.assign({}, settings.headers || {}),
    };
    if (settings.body !== undefined) {
      requestOptions.headers["Content-Type"] = "application/json";
      requestOptions.body = JSON.stringify(settings.body);
    }
    return fetch(apiPath(path), requestOptions).then(function (response) {
      return response.text().then(function (raw) {
        let payload = {};
        if (raw) {
          try {
            payload = JSON.parse(raw);
          } catch (_) {
            payload = { raw: raw };
          }
        }
        if (!response.ok) {
          const error = new Error(payload && payload.error ? payload.error : `Request failed (${response.status})`);
          error.status = response.status;
          error.payload = payload;
          throw error;
        }
        return payload;
      });
    });
  }

  function initDataFromEnvironment() {
    if (tg && typeof tg.initData === "string" && tg.initData.trim() !== "") {
      return tg.initData.trim();
    }
    const hash = new URLSearchParams((window.location.hash || "").replace(/^#/, ""));
    const hashInitData = hash.get("tgWebAppData");
    if (hashInitData) {
      return hashInitData;
    }
    const queryInitData = query.get("initData");
    if (queryInitData) {
      return queryInitData;
    }
    return "";
  }

  async function ensureSession() {
    try {
      await request("/session");
      return true;
    } catch (error) {
      if (error.status !== 401) {
        throw error;
      }
    }
    const initData = initDataFromEnvironment();
    if (!initData) {
      return false;
    }
    await request("/auth/telegram", { method: "POST", body: { initData: initData } });
    await request("/session");
    return true;
  }

  async function refreshAll() {
    state.loading = true;
    state.syncing = true;
    render();
    try {
      const hasSession = await ensureSession();
      if (!hasSession) {
        state.bootstrap = null;
        state.planData = null;
        state.invoiceActions = {};
        state.error = "";
        return;
      }
      const payload = await request(bootstrapPath());
      state.bootstrap = payload;
      state.error = "";
      state.invoiceActions = payload.invoiceActions || {};
      applyLaunch(payload.launch || {});
      syncCreatePlanForm();
      syncQuoteDefaults();
      if (state.selectedPlanId) {
        await loadPlanData(state.selectedPlanId);
      } else {
        state.planData = null;
      }
      if (isOperator()) {
        await loadAdminData();
      } else {
        clearAdminData();
      }
      state.bootstrapped = true;
    } catch (error) {
      state.bootstrap = null;
      state.planData = null;
      state.invoiceActions = {};
      state.error = String(error.message || error);
      notifyError();
    } finally {
      state.loading = false;
      state.syncing = false;
      render();
    }
  }

  function applyLaunch(launch) {
    const plans = (state.bootstrap && state.bootstrap.plans) || [];
    if (!state.selectedPlanId && launch.planId) {
      state.selectedPlanId = launch.planId;
    }
    if (!state.selectedPlanId && plans.length > 0) {
      state.selectedPlanId = planId(plans[0]);
    }
    if (launch.invoiceId) {
      state.selectedInvoiceId = launch.invoiceId;
    } else if (!state.selectedInvoiceId) {
      const invoice = selectedInvoice();
      state.selectedInvoiceId = invoice ? invoice.ID : "";
    }
    if (!state.selectedServiceCode && state.bootstrap && state.bootstrap.catalog && state.bootstrap.catalog.length > 0) {
      state.selectedServiceCode = state.bootstrap.catalog[0].ServiceCode;
    }
    if (!state.joinInviteCode && launch.inviteCode) {
      state.joinInviteCode = launch.inviteCode;
    }
    if (!state.bootstrapped) {
      state.defaultSection = launch.section || defaultSectionFromMode();
      state.section = state.defaultSection;
      if (launch.section === "admin") {
        state.defaultAdminView = launch.adminView || "overview";
        state.adminView = state.defaultAdminView;
      } else if (config.mode === "admin") {
        state.defaultAdminView = launch.adminView || "overview";
        state.adminView = state.defaultAdminView;
      }
    }
    if (launch.section === "admin" && launch.adminView && !state.bootstrapped) {
      state.adminView = launch.adminView;
    }
  }

  async function loadPlanData(planID) {
    state.planDataLoading = true;
    render();
    try {
      const view = selectedPlanViewById(planID);
      if (!view) {
        state.planData = null;
        return;
      }
      const ownerAccess = Boolean(view.IsOwner) || isOperator();
      const responses = await Promise.all([
        request(`/ledger?plan_id=${encodeURIComponent(planID)}`),
        ownerAccess ? request(`/plans/${encodeURIComponent(planID)}/members`) : Promise.resolve(null),
      ]);
      state.planData = {
        planId: planID,
        ledger: responses[0],
        members: responses[1] ? responses[1].members || [] : [],
      };
    } catch (error) {
      state.error = String(error.message || error);
      state.planData = null;
      notifyError();
    } finally {
      state.planDataLoading = false;
      render();
    }
  }

  function clearAdminData() {
    state.admin.loading = false;
    state.admin.overview = null;
    state.admin.tickets = [];
    state.admin.issues = [];
    state.admin.recentPlans = [];
    state.admin.reimbursements = [];
    state.admin.alerts = [];
    state.admin.entries = [];
  }

  async function loadAdminData() {
    if (!isOperator()) {
      clearAdminData();
      return;
    }
    state.admin.loading = true;
    render();
    try {
      const results = await Promise.all([
        request("/admin/overview"),
        request("/admin/support"),
        request("/admin/issues"),
        request("/admin/recent-plans"),
        request("/admin/reimbursements"),
        request("/admin/payment-alerts"),
        request("/admin/denylist"),
      ]);
      state.admin.overview = results[0];
      state.admin.tickets = results[1].tickets || [];
      state.admin.issues = results[2].issues || [];
      state.admin.recentPlans = results[3].plans || [];
      state.admin.reimbursements = results[4].reimbursements || [];
      state.admin.alerts = results[5].alerts || [];
      state.admin.entries = results[6].entries || [];
    } catch (error) {
      state.error = String(error.message || error);
      notifyError();
    } finally {
      state.admin.loading = false;
      render();
    }
  }

  function syncCreatePlanForm() {
    const service = selectedCatalogItem();
    if (service && !state.createForm.accessMode) {
      state.createForm.accessMode = service.AccessMode || "";
    }
  }

  function syncQuoteDefaults() {
    if (!state.bootstrap || !state.bootstrap.payments) {
      return;
    }
    if (!state.quote.asset) {
      state.quote.asset = state.bootstrap.payments.defaultAsset || "";
    }
    if (!state.quote.network) {
      state.quote.network = state.bootstrap.payments.defaultNetwork || "";
    }
    const allowedNetworks = availableNetworks(state.quote.asset);
    if (allowedNetworks.length > 0 && allowedNetworks.indexOf(state.quote.network) === -1) {
      state.quote.network = allowedNetworks[0];
    }
  }

  function selectedCatalogItem() {
    const catalog = (state.bootstrap && state.bootstrap.catalog) || [];
    return catalog.find(function (item) {
      return item.ServiceCode === state.selectedServiceCode;
    }) || catalog[0] || null;
  }

  function planId(view) {
    return view && view.Plan ? view.Plan.ID : "";
  }

  function selectedPlanViewById(planID) {
    const plans = (state.bootstrap && state.bootstrap.plans) || [];
    return plans.find(function (item) {
      return planId(item) === planID;
    }) || null;
  }

  function selectedPlanView() {
    return selectedPlanViewById(state.selectedPlanId);
  }

  function isOperator() {
    return Boolean(state.bootstrap && state.bootstrap.session && state.bootstrap.session.isOperator);
  }

  function allInvoices() {
    const invoices = [];
    const seen = {};
    const latest = state.bootstrap && state.bootstrap.latestInvoice;
    const plans = (state.bootstrap && state.bootstrap.plans) || [];
    if (latest && latest.ID) {
      invoices.push(latest);
      seen[latest.ID] = true;
    }
    plans.forEach(function (view) {
      if (view && view.OpenInvoice && view.OpenInvoice.ID && !seen[view.OpenInvoice.ID]) {
        invoices.push(view.OpenInvoice);
        seen[view.OpenInvoice.ID] = true;
      }
    });
    return invoices;
  }

  function invoiceById(invoiceID) {
    if (!invoiceID) {
      return null;
    }
    return allInvoices().find(function (invoice) {
      return invoice.ID === invoiceID;
    }) || null;
  }

  function selectedInvoice() {
    if (state.selectedInvoiceId) {
      const invoice = invoiceById(state.selectedInvoiceId);
      if (invoice) {
        return invoice;
      }
    }
    const view = selectedPlanView();
    if (view && view.OpenInvoice) {
      return view.OpenInvoice;
    }
    return state.bootstrap && state.bootstrap.latestInvoice ? state.bootstrap.latestInvoice : null;
  }

  function invoiceActionsFor(invoice) {
    if (!invoice || !invoice.ID) {
      return null;
    }
    return state.invoiceActions[invoice.ID] || null;
  }

  function availableNetworks(asset) {
    if (!state.bootstrap || !state.bootstrap.payments || !state.bootstrap.payments.networksByAsset) {
      return [];
    }
    return state.bootstrap.payments.networksByAsset[asset] || [];
  }

  async function submitCreatePlan() {
    const service = selectedCatalogItem();
    if (!service) {
      throw new Error("Choose an approved service first.");
    }
    const totalPriceMinor = moneyTextToMinor(state.createForm.totalPrice);
    const payload = await request("/plans", {
      method: "POST",
      body: {
        service_code: service.ServiceCode,
        total_price_minor: totalPriceMinor,
        seat_limit: Number(state.createForm.seatLimit),
        renewal_date: state.createForm.renewalDate,
        access_mode: state.createForm.accessMode,
        sharing_policy_ack: Boolean(state.createForm.sharingPolicyAck),
      },
    });
    state.notice = `${service.DisplayName} plan created.`;
    state.generatedInvite = payload.invite || null;
    state.generatedInviteLaunch = payload.launch || null;
    state.createForm.totalPrice = "";
    state.createForm.seatLimit = "2";
    state.createForm.renewalDate = defaultRenewalDate();
    notifySuccess();
    await refreshAll();
  }

  async function submitJoinPlan() {
    if (!state.joinInviteCode.trim()) {
      throw new Error("Paste an invite code first.");
    }
    const payload = await request("/plans/join", {
      method: "POST",
      body: { invite_code: state.joinInviteCode.trim() },
    });
    state.notice = "Plan joined.";
    state.joinInviteCode = "";
    if (payload && payload.invoice && payload.invoice.ID) {
      state.selectedInvoiceId = payload.invoice.ID;
      if (payload.invoice.PlanID) {
        state.selectedPlanId = payload.invoice.PlanID;
      }
      state.section = "invoice";
    }
    notifySuccess();
    await refreshAll();
  }

  async function regenerateInvite() {
    const view = selectedPlanView();
    if (!view) {
      throw new Error("Choose a plan first.");
    }
    const payload = await request(`/plans/${encodeURIComponent(planId(view))}/invite`, { method: "POST", body: {} });
    state.generatedInvite = payload.invite || null;
    state.generatedInviteLaunch = payload.launch || null;
    state.notice = payload && payload.invite && payload.invite.InviteCode
      ? `Fresh invite code: ${payload.invite.InviteCode}`
      : "Fresh invite created.";
    notifySuccess();
    render();
  }

  async function quoteSelectedInvoice() {
    const invoice = selectedInvoice();
    if (!invoice) {
      throw new Error("There is no open invoice to quote.");
    }
    const payload = await request(`/invoices/${encodeURIComponent(invoice.ID)}/quote`, {
      method: "POST",
      body: {
        pay_asset: state.quote.asset,
        network: state.quote.network,
      },
    });
    if (payload && payload.invoice && payload.invoice.ID && payload.payment_actions) {
      state.invoiceActions[payload.invoice.ID] = payload.payment_actions;
    }
    state.notice = "Payment quote refreshed.";
    notifySuccess();
    await refreshAll();
  }

  async function simulateSelectedInvoice() {
    const invoice = selectedInvoice();
    if (!invoice) {
      throw new Error("There is no open invoice to simulate.");
    }
    const amountAtomic = invoice.QuotedPayAmount || "";
    if (!amountAtomic) {
      throw new Error("Refresh the quote before running the sandbox payment.");
    }
    await request(`/invoices/${encodeURIComponent(invoice.ID)}/simulate`, {
      method: "POST",
      body: { amount_atomic: amountAtomic },
    });
    state.notice = "Sandbox payment submitted.";
    notifySuccess();
    await refreshAll();
  }

  async function submitSupport() {
    const view = selectedPlanView();
    if (!view) {
      throw new Error("Choose a plan before opening support.");
    }
    await request("/support", {
      method: "POST",
      body: {
        plan_id: planId(view),
        message: state.supportMessage.trim(),
      },
    });
    state.notice = "Support ticket opened.";
    state.supportMessage = "";
    notifySuccess();
    await refreshAll();
  }

  async function resolveSupportTicket(ticketID) {
    await request(`/admin/support/${encodeURIComponent(ticketID)}/resolve`, {
      method: "POST",
      body: { note: "Resolved from native Mini App" },
    });
    state.notice = `Support ticket ${ticketID} resolved.`;
    notifySuccess();
    await refreshAll();
  }

  async function submitBlockUser() {
    await request("/admin/denylist/block-user", {
      method: "POST",
      body: {
        telegram_id: Number(state.blockForm.telegramId),
        reason: state.blockForm.reason.trim(),
      },
    });
    state.notice = `Blocked Telegram ID ${state.blockForm.telegramId}.`;
    state.blockForm.telegramId = "";
    state.blockForm.reason = "";
    notifyImpact("heavy");
    await refreshAll();
  }

  async function copyTextValue(value, successText) {
    if (!value) {
      return;
    }
    try {
      await navigator.clipboard.writeText(value);
      state.notice = successText || "Copied.";
      notifySuccess();
    } catch (_) {
      state.notice = value;
    }
    render();
  }

  function openLink(urlValue) {
    if (!urlValue) {
      return;
    }
    if (tg && typeof tg.openLink === "function") {
      tg.openLink(urlValue);
      return;
    }
    window.open(urlValue, "_blank", "noopener,noreferrer");
  }

  function openTelegramLink(urlValue) {
    if (!urlValue) {
      return;
    }
    if (tg && typeof tg.openTelegramLink === "function") {
      tg.openTelegramLink(urlValue);
      return;
    }
    openLink(urlValue);
  }

  function shareText(text) {
    if (!text) {
      return;
    }
    const shareURL = `https://t.me/share/url?url=&text=${encodeURIComponent(text)}`;
    openTelegramLink(shareURL);
  }

  function onClick(event) {
    const actionButton = event.target.closest("[data-action]");
    if (!actionButton) {
      return;
    }
    event.preventDefault();
    runAction(actionButton.getAttribute("data-action"), actionButton.getAttribute("data-value") || "");
  }

  function onSubmit(event) {
    const form = event.target.closest("form[data-form]");
    if (!form) {
      return;
    }
    event.preventDefault();
    runAction(form.getAttribute("data-form"), "");
  }

  function onChange(event) {
    const target = event.target;
    if (!(target instanceof HTMLElement)) {
      return;
    }
    const form = target.closest("form[data-form]");
    if (form && form.getAttribute("data-form") === "create-plan" && target.name === "serviceCode") {
      state.selectedServiceCode = target.value;
      const service = selectedCatalogItem();
      state.createForm.accessMode = service ? service.AccessMode || "" : "";
      render();
      return;
    }
    if (form && form.getAttribute("data-form") === "quote-invoice" && target.name === "asset") {
      state.quote.asset = target.value;
      const networks = availableNetworks(state.quote.asset);
      state.quote.network = networks[0] || "";
      render();
      return;
    }
  }

  function onInput(event) {
    const target = event.target;
    if (!(target instanceof HTMLElement)) {
      return;
    }
    const form = target.closest("form[data-form]");
    if (!form) {
      return;
    }
    const formName = form.getAttribute("data-form");
    if (formName === "create-plan") {
      if (target.name === "totalPrice") {
        state.createForm.totalPrice = target.value;
      } else if (target.name === "seatLimit") {
        state.createForm.seatLimit = target.value;
      } else if (target.name === "renewalDate") {
        state.createForm.renewalDate = target.value;
      } else if (target.name === "sharingPolicyAck") {
        state.createForm.sharingPolicyAck = Boolean(target.checked);
      }
      render();
      return;
    }
    if (formName === "join-plan" && target.name === "inviteCode") {
      state.joinInviteCode = target.value;
      render();
      return;
    }
    if (formName === "support" && target.name === "message") {
      state.supportMessage = target.value;
      render();
      return;
    }
    if (formName === "quote-invoice") {
      if (target.name === "asset") {
        state.quote.asset = target.value;
      } else if (target.name === "network") {
        state.quote.network = target.value;
      }
      render();
      return;
    }
    if (formName === "block-user") {
      if (target.name === "telegramId") {
        state.blockForm.telegramId = target.value;
      } else if (target.name === "reason") {
        state.blockForm.reason = target.value;
      }
      render();
    }
  }

  async function runAction(action, value) {
    if (state.syncing && action !== "refresh-all") {
      return;
    }
    state.error = "";
    try {
      state.syncing = true;
      render();
      switch (action) {
        case "refresh-all":
          await refreshAll();
          return;
        case "nav-home":
          window.location.href = launcherPath();
          return;
        case "nav-app":
          window.location.href = routePath("/app");
          return;
        case "nav-admin":
          window.location.href = routePath("/admin");
          return;
        case "set-section":
          state.section = value;
          if (value === "admin" && !state.adminView) {
            state.adminView = "overview";
          }
          render();
          return;
        case "set-admin-view":
          state.section = "admin";
          state.adminView = value || "overview";
          render();
          return;
        case "select-plan":
          state.selectedPlanId = value;
          await loadPlanData(value);
          return;
        case "select-invoice":
          state.selectedInvoiceId = value;
          state.section = "invoice";
          render();
          return;
        case "open-generated-invite":
          openTelegramLink(state.generatedInviteLaunch && state.generatedInviteLaunch.telegramDeepLink);
          return;
        case "copy-generated-invite":
          await copyTextValue(state.generatedInvite && state.generatedInvite.InviteCode, "Invite code copied.");
          return;
        case "copy-generated-link":
          await copyTextValue(state.generatedInviteLaunch && state.generatedInviteLaunch.telegramDeepLink, "Join link copied.");
          return;
        case "regenerate-invite":
          await regenerateInvite();
          return;
        case "quote-invoice":
          await quoteSelectedInvoice();
          return;
        case "simulate-invoice":
          await simulateSelectedInvoice();
          return;
        case "copy-payment-reference":
          await copyTextValue(selectedInvoice() && selectedInvoice().PaymentRef, "Payment reference copied.");
          return;
        case "copy-payment-details":
          await copyTextValue(paymentDetailsText(), "Payment details copied.");
          return;
        case "share-payment":
          shareText(paymentDetailsText());
          return;
        case "create-plan":
          await submitCreatePlan();
          return;
        case "join-plan":
          await submitJoinPlan();
          return;
        case "support":
          await submitSupport();
          return;
        case "resolve-ticket":
          await resolveSupportTicket(value);
          return;
        case "block-user":
          await submitBlockUser();
          return;
        default:
          throw new Error(`Unknown action: ${action}`);
      }
    } catch (error) {
      state.error = String(error.message || error);
      notifyError();
    } finally {
      state.syncing = false;
      render();
    }
  }

  function onTelegramMainButton() {
    if (state.mainButtonAction) {
      runAction(state.mainButtonAction, state.mainButtonValue || "");
    }
  }

  function onTelegramBackButton() {
    if (state.section === "admin" && state.adminView && state.adminView !== state.defaultAdminView) {
      state.adminView = state.defaultAdminView;
      render();
      return;
    }
    if (state.section !== state.defaultSection) {
      state.section = state.defaultSection;
      if (state.section === "admin" && !state.adminView) {
        state.adminView = state.defaultAdminView || "overview";
      }
      render();
      return;
    }
    if (config.mode !== "launcher") {
      window.location.href = launcherPath();
    }
  }

  function render() {
    appRoot.innerHTML = `
      <main class="miniapp-shell">
        ${renderHeader()}
        ${renderNotice()}
        ${renderBody()}
      </main>
    `;
    syncTelegramChrome();
  }

  function syncTelegramChrome() {
    if (!tg) {
      return;
    }

    const showBack = config.mode !== "launcher" && (
      state.section !== state.defaultSection ||
      (state.section === "admin" && state.adminView && state.adminView !== state.defaultAdminView)
    );
    if (tg.BackButton) {
      if (showBack) {
        tg.BackButton.show();
      } else {
        tg.BackButton.hide();
      }
    }

    const mainButton = tg.MainButton;
    if (!mainButton) {
      return;
    }
    state.mainButtonAction = "";
    state.mainButtonValue = "";
    let text = "";
    switch (state.section) {
      case "create":
        text = "Create Plan";
        state.mainButtonAction = "create-plan";
        break;
      case "join":
        text = "Join Plan";
        state.mainButtonAction = "join-plan";
        break;
      case "invoice":
        if (selectedInvoice() && !selectedInvoice().QuotedPayAmount) {
          text = "Refresh Quote";
          state.mainButtonAction = "quote-invoice";
        } else if (paymentDetailsText()) {
          text = "Copy Payment";
          state.mainButtonAction = "copy-payment-details";
        }
        break;
      case "support":
        text = "Open Support";
        state.mainButtonAction = "support";
        break;
      case "admin":
        if (state.adminView === "denylist") {
          text = "Block User";
          state.mainButtonAction = "block-user";
        }
        break;
      default:
        break;
    }
    if (text) {
      mainButton.setText(text);
      if (state.syncing) {
        if (typeof mainButton.disable === "function") {
          mainButton.disable();
        }
      } else if (typeof mainButton.enable === "function") {
        mainButton.enable();
      }
      mainButton.show();
    } else {
      mainButton.hide();
    }
  }

  function renderHeader() {
    const session = state.bootstrap && state.bootstrap.session;
    const launch = state.bootstrap && state.bootstrap.launch;
    return `
      <header class="topbar">
        <div>
          <p class="eyebrow">Telegram Mini App</p>
          <h1 class="title">${escapeHTML(config.mode === "launcher" ? "Subscription launcher" : "Subscription app")}</h1>
          <p class="subtitle">${escapeHTML(config.mode === "launcher" ? "Open the right workspace for plans, billing, and operator triage." : "Plans, billing, support, and operator actions in one Telegram-native flow.")}</p>
        </div>
        <div class="topbar-meta">
          <span class="chip">${escapeHTML(session && session.username ? session.username : "telegram-user")}</span>
          ${launch && launch.botUsername ? `<span class="chip muted-chip">@${escapeHTML(launch.botUsername)}</span>` : ""}
          <button class="chip-button" data-action="refresh-all">${state.syncing ? "Refreshing..." : "Refresh"}</button>
        </div>
      </header>
    `;
  }

  function renderNotice() {
    const pieces = [];
    if (state.error) {
      pieces.push(`
        <section class="notice error">
          <strong>Needs attention</strong>
          <div>${escapeHTML(state.error)}</div>
        </section>
      `);
    }
    if (state.notice) {
      pieces.push(`
        <section class="notice success">
          <strong>Updated</strong>
          <div>${escapeHTML(state.notice)}</div>
        </section>
      `);
    }
    return pieces.join("");
  }

  function renderBody() {
    if (state.loading) {
      return renderLoading();
    }
    if (!state.bootstrap) {
      return renderUnauthenticated();
    }
    if (config.mode === "launcher") {
      return renderLauncher();
    }
    return `
      ${renderSummaryStrip()}
      ${renderSectionTabs()}
      <section class="page-card">
        ${renderSectionBody()}
      </section>
    `;
  }

  function renderLoading() {
    return `
      <section class="page-card">
        <div class="skeleton"></div>
        <div class="skeleton short"></div>
        <div class="skeleton"></div>
      </section>
    `;
  }

  function renderUnauthenticated() {
    const hasTelegramContext = Boolean(initDataFromEnvironment());
    return `
      <section class="page-card center-card">
        <h2>Open this from Telegram</h2>
        <p class="body-copy">The Mini App needs Telegram to provide signed launch data before it can open a billing session.</p>
        <div class="stack-list">
          <div class="info-row">
            <span class="metric-label">Telegram context</span>
            <strong>${hasTelegramContext ? "Detected" : "Missing"}</strong>
          </div>
          <div class="actions-row">
            <button class="primary-button" data-action="refresh-all">Try again</button>
            <a class="secondary-button" href="${escapeAttr(routePath("/app"))}">Open app route</a>
          </div>
        </div>
      </section>
    `;
  }

  function renderLauncher() {
    const plans = (state.bootstrap.plans || []).length;
    const overview = state.bootstrap.admin || {};
    return `
      <section class="launcher-grid">
        <div class="page-card">
          <p class="eyebrow">Default workspace</p>
          <h2>Open the billing app</h2>
          <p class="body-copy">This is the canonical Mini App surface for owners and members. It stays reachable in a normal browser for testing, but the real session comes from Telegram.</p>
          <div class="metric-grid">
            ${metricCard("Visible plans", String(plans))}
            ${metricCard("Latest due", money(invoiceDueMinor(state.bootstrap.latestInvoice)))}
            ${metricCard("Role", isOperator() ? "Operator" : "Owner or member")}
          </div>
          <div class="actions-row">
            <button class="primary-button" data-action="nav-app">Open app</button>
            <button class="secondary-button" data-action="refresh-all">Refresh</button>
          </div>
        </div>
        <div class="page-card">
          <p class="eyebrow">Operator</p>
          <h2>Open the triage console</h2>
          <p class="body-copy">Support queue, renewal issues, alerts, plans, and denylist controls all live inside the same Mini App shell.</p>
          <div class="metric-grid">
            ${metricCard("Open support", String(overview.SupportOpenTotal || 0))}
            ${metricCard("Renewal issues", String(overview.FailedRenewalsTotal || 0))}
            ${metricCard("Blocked actors", String(overview.BlockedActorsTotal || 0))}
          </div>
          <div class="actions-row">
            <button class="primary-button" data-action="nav-admin"${isOperator() ? "" : " disabled"}>Open operator app</button>
          </div>
        </div>
      </section>
    `;
  }

  function renderSummaryStrip() {
    const latestInvoice = selectedInvoice() || (state.bootstrap && state.bootstrap.latestInvoice);
    return `
      <section class="summary-strip">
        ${metricCard("Plans", String((state.bootstrap.plans || []).length))}
        ${metricCard("Latest due", money(invoiceDueMinor(latestInvoice)))}
        ${metricCard("Selected plan", selectedPlanView() ? selectedPlanView().Plan.ServiceName : "None")}
        ${metricCard("Payment provider", state.bootstrap.payments && state.bootstrap.payments.provider ? state.bootstrap.payments.provider : "unknown")}
      </section>
    `;
  }

  function renderSectionTabs() {
    const tabs = [
      { id: "plans", label: "Plans" },
      { id: "create", label: "Create" },
      { id: "join", label: "Join" },
      { id: "invoice", label: "Invoice" },
      { id: "ledger", label: "Ledger" },
      { id: "support", label: "Support" },
    ];
    if (isOperator()) {
      tabs.push({ id: "admin", label: "Admin" });
    }
    return `
      <nav class="tab-row">
        ${tabs.map(function (tab) {
          return `<button class="tab ${state.section === tab.id ? "active" : ""}" data-action="set-section" data-value="${escapeAttr(tab.id)}">${escapeHTML(tab.label)}</button>`;
        }).join("")}
      </nav>
    `;
  }

  function renderSectionBody() {
    switch (state.section) {
      case "create":
        return renderCreateSection();
      case "join":
        return renderJoinSection();
      case "invoice":
        return renderInvoiceSection();
      case "ledger":
        return renderLedgerSection();
      case "support":
        return renderSupportSection();
      case "admin":
        return renderAdminSection();
      case "plans":
      default:
        return renderPlansSection();
    }
  }

  function renderPlansSection() {
    const plans = state.bootstrap.plans || [];
    return `
      <div class="section-header">
        <div>
          <p class="eyebrow">Owner and member flow</p>
          <h2>Your plans</h2>
        </div>
        <div class="actions-row">
          <button class="secondary-button" data-action="set-section" data-value="create">Create plan</button>
          <button class="secondary-button" data-action="set-section" data-value="join">Join plan</button>
        </div>
      </div>
      ${plans.length ? `
        <div class="two-column">
          <aside class="panel-stack">
            ${plans.map(function (view) {
              const id = planId(view);
              return `
                <button class="list-card ${id === state.selectedPlanId ? "selected" : ""}" data-action="select-plan" data-value="${escapeAttr(id)}">
                  <div class="list-card-head">
                    <strong>${escapeHTML(view.Plan.ServiceName)}</strong>
                    <span class="chip ${view.IsOwner ? "" : "warm-chip"}">${escapeHTML(view.IsOwner ? "Owner" : "Member")}</span>
                  </div>
                  <div class="muted-copy">${escapeHTML(id)} · ${view.MemberCount}/${view.Plan.SeatLimit} seats</div>
                  <div class="muted-copy">${view.OpenInvoice ? `Invoice ${escapeHTML(view.OpenInvoice.Status)}` : "No open invoice"}</div>
                </button>
              `;
            }).join("")}
          </aside>
          <div>
            ${renderSelectedPlanCard()}
          </div>
        </div>
      ` : `
        <div class="empty-card">
          <strong>No plans yet</strong>
          <p class="body-copy">Create a compliant plan or paste an invite code to join one.</p>
        </div>
      `}
    `;
  }

  function renderCreateSection() {
    const service = selectedCatalogItem();
    const catalog = (state.bootstrap && state.bootstrap.catalog) || [];
    return `
      <div class="section-header">
        <div>
          <p class="eyebrow">Owner flow</p>
          <h2>Create a compliant plan</h2>
        </div>
      </div>
      <form class="form-grid" data-form="create-plan">
        <label class="field">
          <span>Approved service</span>
          <select name="serviceCode">
            ${catalog.map(function (item) {
              return `<option value="${escapeAttr(item.ServiceCode)}"${item.ServiceCode === state.selectedServiceCode ? " selected" : ""}>${escapeHTML(item.DisplayName)}</option>`;
            }).join("")}
          </select>
        </label>
        <div class="two-column compact">
          <label class="field">
            <span>Monthly total (USDC)</span>
            <input name="totalPrice" value="${escapeAttr(state.createForm.totalPrice)}" placeholder="18.00">
          </label>
          <label class="field">
            <span>Seat limit</span>
            <input name="seatLimit" inputmode="numeric" value="${escapeAttr(state.createForm.seatLimit)}">
          </label>
        </div>
        <label class="field">
          <span>Renewal date</span>
          <input type="date" name="renewalDate" value="${escapeAttr(state.createForm.renewalDate)}">
        </label>
        <div class="info-card">
          <strong>${escapeHTML(service ? service.AccessMode || "Approved access mode" : "Approved access mode")}</strong>
          <div class="muted-copy">${escapeHTML(service && service.SharingPolicyNote ? service.SharingPolicyNote : "Use official family, team, or invite-based access only.")}</div>
        </div>
        <label class="checkbox-row">
          <input type="checkbox" name="sharingPolicyAck"${state.createForm.sharingPolicyAck ? " checked" : ""}>
          <span>I confirm this plan will use the service's allowed family, team, or invite flow.</span>
        </label>
        <div class="actions-row">
          <button class="primary-button" type="submit"${disabledAttr()}>${state.syncing ? "Creating..." : "Create plan"}</button>
          <button class="secondary-button" type="button" data-action="set-section" data-value="plans">Back to plans</button>
        </div>
        ${renderGeneratedInviteCard()}
      </form>
    `;
  }

  function renderJoinSection() {
    const latestInvoice = state.bootstrap && state.bootstrap.latestInvoice;
    return `
      <div class="section-header">
        <div>
          <p class="eyebrow">Member flow</p>
          <h2>Join with an invite</h2>
        </div>
      </div>
      <form class="form-grid" data-form="join-plan">
        <label class="field">
          <span>Invite code</span>
          <input name="inviteCode" placeholder="Paste the owner's invite code" value="${escapeAttr(state.joinInviteCode)}">
        </label>
        <div class="info-card">
          <strong>${latestInvoice ? `Latest invoice ${escapeHTML(latestInvoice.ID)}` : "No open invoice yet"}</strong>
          <div class="muted-copy">${latestInvoice ? `Still due ${money(invoiceDueMinor(latestInvoice))}.` : "Once you join a plan, your newest invoice will appear here."}</div>
        </div>
        <div class="actions-row">
          <button class="primary-button" type="submit"${disabledAttr()}>${state.syncing ? "Joining..." : "Join plan"}</button>
        </div>
      </form>
    `;
  }

  function renderInvoiceSection() {
    const invoice = selectedInvoice();
    const actions = invoiceActionsFor(invoice);
    if (!invoice) {
      return `
        <div class="empty-card">
          <strong>No invoice available</strong>
          <p class="body-copy">Refresh the app or join a plan first.</p>
        </div>
      `;
    }
    return `
      <div class="section-header">
        <div>
          <p class="eyebrow">Billing</p>
          <h2>Invoice ${escapeHTML(invoice.ID)}</h2>
        </div>
        <span class="chip ${invoiceDueMinor(invoice) > 0 ? "warm-chip" : ""}">${escapeHTML(invoice.Status)}</span>
      </div>
      <div class="stack-list">
        <div class="info-card">
          <strong>Amount due ${money(invoiceDueMinor(invoice))}</strong>
          <div class="muted-copy">Base ${money(invoice.BaseMinor)} · Fee ${money(invoice.FeeMinor)} · Paid ${money(invoice.PaidMinor)}</div>
        </div>
        <form class="form-grid" data-form="quote-invoice">
          <div class="two-column compact">
            <label class="field">
              <span>Pay asset</span>
              <select name="asset">
                ${(state.bootstrap.payments.allowedAssets || []).map(function (asset) {
                  return `<option value="${escapeAttr(asset)}"${asset === state.quote.asset ? " selected" : ""}>${escapeHTML(asset)}</option>`;
                }).join("")}
              </select>
            </label>
            <label class="field">
              <span>Network</span>
              <select name="network">
                ${availableNetworks(state.quote.asset).map(function (network) {
                  return `<option value="${escapeAttr(network)}"${network === state.quote.network ? " selected" : ""}>${escapeHTML(network)}</option>`;
                }).join("")}
              </select>
            </label>
          </div>
          <div class="actions-row">
            <button class="primary-button" type="submit"${disabledAttr()}>Refresh quote</button>
            ${state.bootstrap.payments.simulateEnabled && invoice.QuotedPayAmount ? `<button class="secondary-button" type="button" data-action="simulate-invoice"${disabledAttr()}>Simulate payment</button>` : ""}
          </div>
        </form>
        ${invoice.QuotedPayAmount ? `
          <div class="info-card">
            <strong>${escapeHTML(invoice.QuotedPayAmount)} ${escapeHTML(invoice.PayAsset || state.quote.asset)}</strong>
            <div class="muted-copy">Network ${escapeHTML(invoice.Network || state.quote.network)} · Reference ${escapeHTML(invoice.PaymentRef || "not set")}</div>
          </div>
        ` : ""}
        ${actions ? `
          <div class="actions-row">
            ${actions.copyReference ? '<button class="secondary-button" data-action="copy-payment-reference">Copy reference</button>' : ""}
            ${actions.copyPaymentDetails ? '<button class="secondary-button" data-action="copy-payment-details">Copy details</button>' : ""}
            ${actions.shareText ? '<button class="secondary-button" data-action="share-payment">Share</button>' : ""}
          </div>
        ` : ""}
      </div>
    `;
  }

  function renderLedgerSection() {
    const view = selectedPlanView();
    const ledger = state.planData && state.planData.ledger;
    return `
      <div class="section-header">
        <div>
          <p class="eyebrow">Plan ledger</p>
          <h2>${escapeHTML(view ? view.Plan.ServiceName : "Select a plan")}</h2>
        </div>
      </div>
      ${view ? "" : `
        <div class="empty-card">
          <strong>No plan selected</strong>
          <p class="body-copy">Choose a plan from the Plans section first.</p>
        </div>
      `}
      ${state.planDataLoading ? '<div class="skeleton"></div>' : ""}
      ${ledger ? `
        <div class="ledger-layout">
          ${renderLedgerTable("Invoices", ["ID", "Status", "Due", "Total"], (ledger.Invoices || []).map(function (invoice) {
            return [invoice.ID, invoice.Status, shortDate(invoice.DueAt), money(invoice.TotalMinor)];
          }))}
          ${renderLedgerTable("Payments", ["ID", "Asset", "Status", "Amount"], (ledger.Payments || []).map(function (payment) {
            return [payment.ID, payment.Asset, payment.SettlementStatus, money(payment.AmountReceived)];
          }))}
          ${renderLedgerTable("Credits", ["ID", "Status", "Remaining", "Note"], (ledger.Credits || []).map(function (credit) {
            return [credit.ID, credit.Status, money(credit.RemainingMinor), credit.Note || "—"];
          }))}
          ${renderLedgerTable("Events", ["Event", "Entity", "When", "Payload"], (ledger.Events || []).map(function (entry) {
            return [entry.EventName, entry.EntityID, shortDateTime(entry.CreatedAt), trim(entry.PayloadJSON, 80)];
          }))}
        </div>
      ` : ""}
    `;
  }

  function renderSupportSection() {
    const view = selectedPlanView();
    return `
      <div class="section-header">
        <div>
          <p class="eyebrow">Support</p>
          <h2>${escapeHTML(view ? view.Plan.ServiceName : "Select a plan")}</h2>
        </div>
      </div>
      ${view ? `
        <form class="form-grid" data-form="support">
          <label class="field">
            <span>What needs attention?</span>
            <textarea name="message" placeholder="Describe the billing or access issue">${escapeHTML(state.supportMessage)}</textarea>
          </label>
          <div class="actions-row">
            <button class="primary-button" type="submit"${disabledAttr()}>Open support ticket</button>
          </div>
        </form>
      ` : `
        <div class="empty-card">
          <strong>No plan selected</strong>
          <p class="body-copy">Pick a plan before opening support.</p>
        </div>
      `}
    `;
  }

  function renderAdminSection() {
    if (!isOperator()) {
      return `
        <div class="empty-card">
          <strong>Operator access required</strong>
          <p class="body-copy">Open this route with an operator Telegram account.</p>
        </div>
      `;
    }
    return `
      <nav class="subtab-row">
        ${["overview", "support", "issues", "alerts", "plans", "denylist"].map(function (view) {
          return `<button class="tab ${state.adminView === view ? "active" : ""}" data-action="set-admin-view" data-value="${escapeAttr(view)}">${escapeHTML(adminViewLabel(view))}</button>`;
        }).join("")}
      </nav>
      <div class="admin-body">
        ${renderAdminView()}
      </div>
    `;
  }

  function renderAdminView() {
    switch (state.adminView) {
      case "support":
        return renderSupportQueue();
      case "issues":
        return renderRenewalIssues();
      case "alerts":
        return renderPaymentAlerts();
      case "plans":
        return renderRecentPlans();
      case "denylist":
        return renderDenyList();
      case "overview":
      default:
        return renderAdminOverview();
    }
  }

  function renderAdminOverview() {
    const overview = state.admin.overview || state.bootstrap.admin || {};
    return `
      <div class="summary-strip">
        ${metricCard("Users", String(overview.UsersTotal || 0))}
        ${metricCard("Open support", String(overview.SupportOpenTotal || 0))}
        ${metricCard("Renewal issues", String(overview.FailedRenewalsTotal || 0))}
        ${metricCard("Alerts", String(overview.PaymentAlertsTotal || 0))}
        ${metricCard("Blocked", String(overview.BlockedActorsTotal || 0))}
        ${metricCard("Payout due", money(overview.PayoutDueMinor || 0))}
      </div>
    `;
  }

  function renderSelectedPlanCard() {
    const view = selectedPlanView();
    if (!view) {
      return `
        <div class="empty-card">
          <strong>No plan selected</strong>
          <p class="body-copy">Choose a plan from the left to inspect members, invoices, and ledger details.</p>
        </div>
      `;
    }
    const canSeeMembers = Boolean(view.IsOwner) || isOperator();
    return `
      <div class="stack-list">
        <div class="info-card">
          <div class="list-card-head">
            <strong>${escapeHTML(view.Plan.ServiceName)}</strong>
            <span class="chip">${escapeHTML(view.Plan.AccessMode)}</span>
          </div>
          <div class="muted-copy">${escapeHTML(view.Plan.ID)} · ${money(view.Plan.TotalPriceMinor)} · ${view.MemberCount}/${view.Plan.SeatLimit} seats</div>
        </div>
        <div class="actions-row">
          <button class="secondary-button" data-action="set-section" data-value="invoice">Invoice</button>
          <button class="secondary-button" data-action="set-section" data-value="ledger">Ledger</button>
          <button class="secondary-button" data-action="set-section" data-value="support">Support</button>
          ${canSeeMembers ? '<button class="secondary-button" data-action="regenerate-invite">Generate invite</button>' : ""}
        </div>
        ${renderGeneratedInviteCard()}
        ${canSeeMembers ? renderMembersPanel() : ""}
      </div>
    `;
  }

  function renderGeneratedInviteCard() {
    if (!state.generatedInvite || !state.generatedInvite.InviteCode) {
      return "";
    }
    return `
      <div class="info-card">
        <strong>Invite ${escapeHTML(state.generatedInvite.InviteCode)}</strong>
        <div class="muted-copy">${state.generatedInviteLaunch && state.generatedInviteLaunch.telegramDeepLink ? escapeHTML(state.generatedInviteLaunch.telegramDeepLink) : "Use the code or the Telegram app link to join."}</div>
        <div class="actions-row">
          <button class="secondary-button" data-action="copy-generated-invite">Copy code</button>
          ${state.generatedInviteLaunch && state.generatedInviteLaunch.telegramDeepLink ? '<button class="secondary-button" data-action="copy-generated-link">Copy join link</button>' : ""}
          ${state.generatedInviteLaunch && state.generatedInviteLaunch.telegramDeepLink ? '<button class="secondary-button" data-action="open-generated-invite">Open in Telegram</button>' : ""}
        </div>
      </div>
    `;
  }

  function renderMembersPanel() {
    const members = state.planData && state.planData.members ? state.planData.members : [];
    return `
      <div class="panel-stack">
        <p class="eyebrow">Members</p>
        ${members.length ? members.map(function (member) {
          return `
            <div class="list-card">
              <strong>${escapeHTML(member.Username || String(member.UserTelegramID || member.UserID || "member"))}</strong>
              <div class="muted-copy">Seat ${escapeHTML(member.SeatStatus)} · Latest invoice ${escapeHTML(member.LatestInvoiceID || "not created yet")}</div>
            </div>
          `;
        }).join("") : `
          <div class="empty-card">
            <strong>No members yet</strong>
            <p class="body-copy">Generate an invite to bring the first member in.</p>
          </div>
        `}
      </div>
    `;
  }

  function renderSupportQueue() {
    return `
      <div class="panel-stack">
        ${state.admin.tickets.length ? state.admin.tickets.map(function (item) {
          return `
            <div class="list-card">
              <div class="list-card-head">
                <strong>${escapeHTML(item.Username || String(item.UserTelegramID || (item.Ticket && item.Ticket.UserID) || "member"))}</strong>
                <span class="chip warm-chip">${escapeHTML((item.Ticket && item.Ticket.ID) || "")}</span>
              </div>
              <div class="muted-copy">${escapeHTML(item.PlanServiceName || "Plan")} · ${escapeHTML((item.Ticket && item.Ticket.Body) || "")}</div>
              <div class="muted-copy">Invoice ${escapeHTML(item.LatestInvoiceID || "not linked")} · ${escapeHTML(item.LatestStatus || "unknown")}</div>
              <div class="actions-row">
                <button class="primary-button" data-action="resolve-ticket" data-value="${escapeAttr(item.Ticket.ID)}"${disabledAttr()}>Resolve</button>
              </div>
            </div>
          `;
        }).join("") : `
          <div class="empty-card">
            <strong>Queue is clear</strong>
            <p class="body-copy">There are no open support tickets right now.</p>
          </div>
        `}
      </div>
    `;
  }

  function renderRenewalIssues() {
    return `
      <div class="panel-stack">
        ${state.admin.issues.length ? state.admin.issues.map(function (issue) {
          return `
            <div class="list-card">
              <strong>${escapeHTML(issue.PlanServiceName || "Plan")}</strong>
              <div class="muted-copy">${escapeHTML(issue.Username || String(issue.UserTelegramID || issue.UserID || "member"))} · ${escapeHTML(issue.Kind)} · due ${money(issue.AmountDueMinor || 0)}</div>
              <div class="muted-copy">${issue.DueAt ? shortDate(issue.DueAt) : "No due date"} · invoice ${escapeHTML(issue.InvoiceID || "not linked")}</div>
            </div>
          `;
        }).join("") : `
          <div class="empty-card">
            <strong>No renewal issues</strong>
            <p class="body-copy">Grace, suspended, and underpaid seats will appear here.</p>
          </div>
        `}
      </div>
    `;
  }

  function renderRecentPlans() {
    return `
      <div class="panel-stack">
        ${state.admin.recentPlans.length ? state.admin.recentPlans.map(function (plan) {
          return `
            <div class="list-card">
              <strong>${escapeHTML(plan.ServiceName)}</strong>
              <div class="muted-copy">${escapeHTML(plan.ID)} · ${plan.SeatLimit} seats · ${money(plan.TotalPriceMinor)}</div>
            </div>
          `;
        }).join("") : `
          <div class="empty-card">
            <strong>No recent plans</strong>
            <p class="body-copy">New plan creation will surface here.</p>
          </div>
        `}
      </div>
    `;
  }

  function renderPaymentAlerts() {
    return `
      <div class="panel-stack">
        ${state.admin.alerts.length ? state.admin.alerts.map(function (alert) {
          return `
            <div class="list-card">
              <strong>${escapeHTML(alert.EventName)}</strong>
              <div class="muted-copy">${escapeHTML(alert.ProviderInvoice || "No provider invoice")} · ${escapeHTML(alert.Detail || "")}</div>
            </div>
          `;
        }).join("") : `
          <div class="empty-card">
            <strong>No alerts</strong>
            <p class="body-copy">Provider mismatches and denied settlement references will show up here.</p>
          </div>
        `}
      </div>
    `;
  }

  function renderDenyList() {
    return `
      <div class="panel-stack">
        ${state.admin.entries.length ? state.admin.entries.map(function (entry) {
          return `
            <div class="list-card">
              <strong>${escapeHTML(entry.EntryType)} ${escapeHTML(entry.EntryValue)}</strong>
              <div class="muted-copy">${escapeHTML(entry.Reason || "No reason recorded")}</div>
            </div>
          `;
        }).join("") : `
          <div class="empty-card">
            <strong>No blocked actors</strong>
            <p class="body-copy">Blocked Telegram IDs and payment references will appear here.</p>
          </div>
        `}
        <form class="form-grid" data-form="block-user">
          <label class="field">
            <span>Telegram ID</span>
            <input name="telegramId" inputmode="numeric" value="${escapeAttr(state.blockForm.telegramId)}" placeholder="123456789">
          </label>
          <label class="field">
            <span>Reason</span>
            <textarea name="reason" placeholder="Fraud review, abuse pattern, or payment risk">${escapeHTML(state.blockForm.reason)}</textarea>
          </label>
          <div class="actions-row">
            <button class="primary-button danger" type="submit"${disabledAttr()}>Block user</button>
          </div>
        </form>
      </div>
    `;
  }

  function renderLedgerTable(title, headers, rows) {
    const safeRows = rows && rows.length ? rows : [["No rows yet", "—", "—", "—"]];
    return `
      <div class="table-card">
        <strong>${escapeHTML(title)}</strong>
        <table class="ledger-table">
          <thead>
            <tr>${headers.map(function (header) { return `<th>${escapeHTML(header)}</th>`; }).join("")}</tr>
          </thead>
          <tbody>
            ${safeRows.map(function (columns) {
              return `<tr>${columns.map(function (column) {
                return `<td>${escapeHTML(String(column))}</td>`;
              }).join("")}</tr>`;
            }).join("")}
          </tbody>
        </table>
      </div>
    `;
  }

  function paymentDetailsText() {
    const invoice = selectedInvoice();
    const actions = invoiceActionsFor(invoice);
    if (actions && actions.copyPaymentDetails) {
      return actions.copyPaymentDetails;
    }
    if (!invoice || !invoice.PaymentRef) {
      return "";
    }
    return [
      `Invoice ${invoice.ID}`,
      `Quoted amount: ${invoice.QuotedPayAmount || "not set"} ${invoice.PayAsset || state.quote.asset}`,
      `Network: ${invoice.Network || state.quote.network}`,
      `Reference: ${invoice.PaymentRef}`,
    ].join("\n");
  }

  function metricCard(label, value) {
    return `
      <div class="metric-card">
        <span class="metric-label">${escapeHTML(label)}</span>
        <strong>${escapeHTML(value)}</strong>
      </div>
    `;
  }

  function adminViewLabel(view) {
    switch (view) {
      case "support":
        return "Support";
      case "issues":
        return "Issues";
      case "alerts":
        return "Alerts";
      case "plans":
        return "Plans";
      case "denylist":
        return "Denylist";
      default:
        return "Overview";
    }
  }

  function disabledAttr() {
    return state.syncing ? " disabled" : "";
  }

  function defaultSectionFromMode() {
    return config.mode === "admin" ? "admin" : "plans";
  }

  function invoiceDueMinor(invoice) {
    if (!invoice) {
      return 0;
    }
    return Math.max(
      (Number(invoice.TotalMinor) || 0) - (Number(invoice.CreditAppliedMinor) || 0) - (Number(invoice.PaidMinor) || 0),
      0
    );
  }

  function money(minor) {
    return `${(Number(minor || 0) / 100).toFixed(2)} USDC`;
  }

  function moneyTextToMinor(value) {
    const raw = String(value || "").trim();
    if (!/^\d+(?:\.\d{1,2})?$/.test(raw)) {
      throw new Error("Monthly total must look like 18 or 18.00.");
    }
    const parts = raw.split(".");
    return (Number(parts[0]) || 0) * 100 + Number((parts[1] || "").padEnd(2, "0") || 0);
  }

  function shortDate(raw) {
    if (!raw) {
      return "—";
    }
    const date = new Date(raw);
    if (Number.isNaN(date.getTime())) {
      return String(raw);
    }
    return date.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
  }

  function shortDateTime(raw) {
    if (!raw) {
      return "—";
    }
    const date = new Date(raw);
    if (Number.isNaN(date.getTime())) {
      return String(raw);
    }
    return date.toLocaleString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  }

  function defaultRenewalDate() {
    const date = new Date();
    date.setMonth(date.getMonth() + 1, 1);
    return date.toISOString().slice(0, 10);
  }

  function trim(value, max) {
    const text = String(value || "");
    return text.length <= max ? text : `${text.slice(0, max - 1)}…`;
  }

  function stringOrEmpty(value) {
    return String(value || "").trim();
  }

  function notifySuccess() {
    if (tg && tg.HapticFeedback && typeof tg.HapticFeedback.notificationOccurred === "function") {
      tg.HapticFeedback.notificationOccurred("success");
    }
  }

  function notifyError() {
    if (tg && tg.HapticFeedback && typeof tg.HapticFeedback.notificationOccurred === "function") {
      tg.HapticFeedback.notificationOccurred("error");
    }
  }

  function notifyImpact(style) {
    if (tg && tg.HapticFeedback && typeof tg.HapticFeedback.impactOccurred === "function") {
      tg.HapticFeedback.impactOccurred(style || "medium");
    }
  }

  function rgba(hex, alpha) {
    const normalized = String(hex || "").replace("#", "");
    if (normalized.length !== 6) {
      return `rgba(46, 124, 246, ${alpha})`;
    }
    const r = parseInt(normalized.slice(0, 2), 16);
    const g = parseInt(normalized.slice(2, 4), 16);
    const b = parseInt(normalized.slice(4, 6), 16);
    return `rgba(${r}, ${g}, ${b}, ${alpha})`;
  }

  function isLightColor(hex) {
    const normalized = String(hex || "").replace("#", "");
    if (normalized.length !== 6) {
      return false;
    }
    const r = parseInt(normalized.slice(0, 2), 16);
    const g = parseInt(normalized.slice(2, 4), 16);
    const b = parseInt(normalized.slice(4, 6), 16);
    return ((r * 299) + (g * 587) + (b * 114)) / 1000 > 160;
  }

  function escapeHTML(value) {
    return String(value == null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  function escapeAttr(value) {
    return escapeHTML(value);
  }
}());
