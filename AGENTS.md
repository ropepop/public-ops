# Agent Guidance

## Browser Use

- Use a single persistent browser profile for browser automation in this repo.
- Prefer the existing user profile shown by `browser-use profile list`. The primary shared profile is `Your Chrome`.
- Never create a new browser profile, never sync a new profile, and never switch to a throwaway profile just to get a task done.
- Reuse the same browser session across a task whenever possible so cookies, local storage, and login state stay warm.
- Work with the saved browser state first. Before starting an authenticated flow, check whether the shared profile already has usable cookies/session state for the site.
- After a successful login, leave the shared profile signed in unless the user explicitly asks to log out. The saved cookies are part of the working state for future agent runs.
- Preserve browsing data. Do not clear cookies, site data, storage, cache, or browser state unless the user explicitly asks.
- If cookies must be exported for handoff or recovery, keep the export under `state/browser-use/`, treat it as sensitive, and import/reuse it before asking the user to authenticate again.
- If profile-backed browsing is unavailable, prefer connecting to the user's existing browser before falling back to a clean browser.
- If a clean browser fallback is unavoidable, say so clearly because the experience will no longer match the user's real browsing state.
- Keep reusable browser session exports only under `state/browser-use/`.
