import "./native-mobile.css";

const MOBILE_QUERY = "(max-width: 700px)";
const mobileViewport = "width=device-width, initial-scale=1, viewport-fit=cover";
const media = window.matchMedia(MOBILE_QUERY);
const enhancedToggles = new WeakSet();

let autoCollapsed = false;
let reconcileTimer = 0;

const ensureViewport = () => {
  let viewport = document.querySelector('meta[name="viewport"]');
  if (!viewport) {
    viewport = document.createElement("meta");
    viewport.name = "viewport";
    document.head.append(viewport);
  }
  viewport.content = mobileViewport;
};

const findDrawer = () =>
  [...document.querySelectorAll('#main > [data-testid="collapsible"]')].find(
    (candidate) =>
      candidate.querySelector('[data-testid="sidebar-tabs"]') ||
      candidate.querySelector('[data-testid="collapsed-header"]'),
  ) || null;

const drawerIsOpen = (drawer) => Boolean(drawer.querySelector('[data-testid="sidebar-tabs"]'));

const activateToggle = (toggle) => {
  if (typeof toggle.click === "function") toggle.click();
  else toggle.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
};

const enhanceToggle = (drawer, open) => {
  const icon = drawer.querySelector('[data-testid="sidebarHeader-icon"]');
  if (!icon) return null;

  const target = icon;
  target.dataset.kittySidebarToggle = "true";
  target.setAttribute("role", "button");
  target.setAttribute("tabindex", "0");
  target.setAttribute("aria-expanded", String(open));
  target.setAttribute("aria-label", open ? "Close metric navigation" : "Open metric navigation");

  if (!enhancedToggles.has(target)) {
    target.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" && event.key !== " ") return;
      event.preventDefault();
      activateToggle(icon);
    });
    enhancedToggles.add(target);
  }
  return icon;
};

const reconcile = () => {
  reconcileTimer = 0;
  document.documentElement.dataset.kittyNetdataMobile = media.matches ? "active" : "desktop";

  const drawer = findDrawer();
  if (!drawer) return;

  if (!media.matches) {
    delete drawer.dataset.kittyMobileDrawer;
    return;
  }

  const open = drawerIsOpen(drawer);
  drawer.dataset.kittyMobileDrawer = open ? "open" : "closed";
  const icon = enhanceToggle(drawer, open);

  if (!autoCollapsed && open && icon) {
    autoCollapsed = true;
    window.requestAnimationFrame(() => activateToggle(icon));
  }
};

const scheduleReconcile = () => {
  if (reconcileTimer) return;
  reconcileTimer = window.setTimeout(reconcile, 40);
};

ensureViewport();
document.documentElement.dataset.kittyNetdataMobileShim = "ready";

const observer = new MutationObserver(scheduleReconcile);
observer.observe(document.documentElement, { childList: true, subtree: true });
media.addEventListener?.("change", scheduleReconcile);
scheduleReconcile();
