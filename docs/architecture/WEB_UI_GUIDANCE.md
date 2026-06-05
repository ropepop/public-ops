# Web UI Guidance

This repo uses ArrowJS (`standardagents/arrow-js`, published as `@arrow-js/core`) as the required default for actively maintained browser-side UI.

## Policy

- Use ArrowJS for interactive web pages, live dashboards, mini apps, stream controls, incident feeds, presence lists, and other browser UI that changes after page load.
- Prefer small reactive islands over full-page HTML replacement. Keep stable page shells mounted and update only the list, detail panel, status, or control area that changed.
- Do not add React, Vue, jQuery, or another browser UI framework unless the user explicitly approves that decision for the workload.
- Do not add JavaScript only for consistency. Static or no-JS admin pages can stay static until they need browser-side interactivity.
- Treat first-load size as a real cost. ArrowJS is justified by calmer live updates and simpler state handling, not by making initial downloads smaller.

## Implementation Defaults

- Load a versioned ArrowJS asset before the page app script when the page app is not bundled.
- Bundle `@arrow-js/core` into the generated app script when that workload already has a browser build pipeline.
- Expose a page-level marker when the Arrow-backed path mounts, such as `document.documentElement.dataset.<service>Ui = "arrow"`, so local and live checks can prove the intended UI path ran.
- Keep server-side routing, auth, and data APIs unchanged unless the UI change explicitly needs them.
- Keep manual `innerHTML` replacement as a fallback or for static fragments only; do not make it the primary update mechanism for live browser UI.

## Verification

For browser UI changes:

- Rebuild the relevant web client or Arrow runtime bundle.
- Run the service-local web tests.
- Open the served page and confirm the Arrow-backed marker is present.
- Check the browser console for new JavaScript errors.
- For deployed changes, verify the live public or authenticated page after the deploy lands.
