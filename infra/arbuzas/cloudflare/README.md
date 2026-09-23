# Ticket response privacy

`ticket-response-privacy-rule.json` is prepared but **not applied**. Add this
single rule to the existing zone-level `http_response_headers_transform`
ruleset; preserve every other rule. It matches only `ticket.jolkins.id.lv`.
It clears the browser's NEL policy and its `cf-nel` reporting endpoint, because
HTTP error reports can otherwise contain invitation URL query strings.

On September 22, 2026, the existing API token could read the zone but could not
access response rules (Cloudflare error 10000). The saved browser redirected to
Cloudflare login. Authenticated access with response-rule permission is required.
Do not disable NEL for the whole zone or expand the certificate token's scope.

After applying, verify both ordinary and invitation/manifest responses through
the public edge carry only the zero-age reporting policy; also check an invalid
invitation and an HTTP error response. Invitation and guest HTML already use
`no-store, no-transform`, which was verified to suppress analytics injection.
These header changes do not alter Cloudflare request-log retention.
