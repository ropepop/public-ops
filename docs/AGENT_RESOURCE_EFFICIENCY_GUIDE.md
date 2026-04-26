# Agent Resource Efficiency Guide

This page is a shared playbook for agents working on apps where compute, storage, database work, polling, and network traffic have a real cost.

It is written to be useful across workloads, but it is grounded in a real train-bot investigation completed on 2026-03-26:

- Local investigation notes: `workloads/train-bot/docs/spacetime-investigation-2026-03-26.md`
- SpacetimeDB pricing reference: <https://spacetimedb.com/pricing>
- SpacetimeDB pricing announcement: <https://spacetimedb.com/blog/all-new-spacetimedb-pricing>

## Purpose

When an app feels slow or expensive, do not guess.

Work from a simple rule:

1. Find the hot path.
2. Count how often it runs.
3. Measure how much data it reads, writes, and sends.
4. Remove repeated work before doing micro-optimizations.

In most apps, cost is dominated by a small number of patterns:

- repeated polling
- broad scans
- fanout reads
- whole-record rewrites
- background cleanup that touches everything
- duplicate imports or refreshes

## Resource Cost Model

If the app is backed by SpacetimeDB Maincloud, the billing model is based on six resources:

- table storage
- bandwidth egress
- CPU instructions
- index seeks
- bytes written
- bytes read

Even when a platform exposes a simpler "function calls" estimate, treat that as a shortcut, not the ground truth.

Translate the billing model into engineering terms:

- Bytes read: full-table scans, repeated list calls, rebuilding the same derived view many times.
- Bytes written: rewriting large records, updating rows on read paths, unnecessary heartbeats.
- CPU instructions: loops over large collections, repeated sorting, repeated parsing, per-item recomputation.
- Index seeks: many small lookups in loops, especially nested lookups.
- Bandwidth: large response payloads, frequent polling, duplicative client fetches.
- Storage: large hot rows, retained history, embedded arrays that grow forever.

## Default Rules For Agents

- Treat every periodic refresh as a production billing decision.
- Treat every write-on-read pattern as suspicious.
- Treat `scan everything, then filter in code` as a likely cost bug.
- Treat `one request that calls N more requests` as a likely cost bug.
- Treat "append one item by rewriting the whole blob" as a likely scale problem.
- Prefer removing repeated work over shaving a few milliseconds off one call.
- Compare raw database timing with full app endpoint timing so you do not blame the wrong layer.

## High-Value Smells

### 1. Polling Loops

Polling multiplies cost fast.

Quick mental math:

- every 30 seconds = 120 calls per hour per open client
- every 15 seconds = 240 calls per hour per open client
- two procedures per refresh doubles those numbers

If the page is public, assume users may leave it open for long periods.

Prefer:

- subscriptions or push updates when available
- slower refresh for low-value views
- separate refresh rates for heavy and light data
- conditional refresh only when the page is visible

### 2. Fanout Reads

This is the classic pattern:

- fetch a list
- loop the list
- fetch more data per item
- sometimes fetch the same per-item data twice

This usually hurts bytes read, CPU, index seeks, and end-user latency at the same time.

Prefer:

- one batched procedure that returns the whole view
- precomputed summaries
- one indexed query over many item-specific queries

### 3. Full Scans In Application Code

Common smell:

- iterate every row
- filter in code
- sort in code

If an indexed lookup exists, use it.
If the same filtered view is requested often, build a narrower access path or a dedicated summary shape.

Red flag examples:

- `iter()` over all rows in a hot path
- loading a full day of records just to answer one station, one train, or one user
- scanning all activity rows every minute

### 4. Writes On Read Paths

A read endpoint should not save data unless that write is required for correctness.

Typical bad examples:

- updating `lastSeenAt` on every view refresh
- refreshing a session heartbeat every time settings are fetched
- rewriting a whole user row when nothing meaningful changed

This is especially expensive when the record contains embedded arrays or nested state.

Prefer:

- read-only procedures for read flows
- separate lightweight heartbeat tables if product needs presence
- write only when a value actually changed
- skip no-op saves

### 5. Whole-Record Rewrites

If one user action appends one comment but rewrites the full comment list, the full vote list, and the full timeline list, cost will grow with history.

This pattern is easy to miss because it works fine at small scale.

Prefer:

- narrower rows
- append-only event tables
- small hot summaries plus separate history tables
- bounded retained arrays if a full split is not practical yet

### 6. Background Sweeps

Background cleanup often becomes the most expensive path because it runs forever.

Bad cleanup jobs usually:

- run too often
- scan every row every time
- rewrite rows even when nothing changed
- delete large historical sets one row at a time

Prefer:

- run less often unless there is a real product reason
- skip unchanged rows
- target only expired ranges or known partitions
- keep cleanup work proportional to what actually expired

### 7. Duplicate Refresh Or Import Work

Look for startup flows or scheduled jobs that reload the same data twice.

A duplicated import or refresh may be acceptable once, but it is often pure waste and usually easy to remove.

### 8. Heavy Public Views

A public page is usually the highest-volume surface.

Assume:

- it may be opened anonymously
- it may be shared widely
- it may stay open in a tab

Any large public payload with a short refresh interval should be treated as a cost hotspot even if the single-call latency looks acceptable.

## Preferred Design Patterns

### Batched View Builders

If the UI needs a dashboard, map, departures board, or incident list, prefer one server-side or database-side procedure that returns the full view shape.

This is usually better than:

- one list call plus many per-item detail calls
- repeating the same derivation logic in multiple layers

### Hot Summary + Cold History

Separate frequently-read summary data from bulky historical detail.

Examples:

- current train status separate from full report history
- active rider state separate from all user preferences and prior actions
- incident summary separate from comments and vote history

### Read Paths That Stay Read-Only

Create explicit read helpers that do not mutate state.

If a helper named `ensureX` inserts or updates records, do not use it casually inside hot read flows.

### Incremental Maintenance

When a view depends on derived counts or summaries, update those incrementally as writes happen instead of recomputing from scratch on every read.

### Bounded Responses

Return only what the UI actually needs:

- limited timeline items
- limited comments
- compact cards for list pages
- detail payloads only for the selected item

## Investigation Workflow For Agents

Use this process when you are asked to review resource efficiency.

### Step 1. Identify The Most Expensive User Journey

Examples:

- public dashboard refresh
- signed-in mini-app idle refresh
- station search and departure board
- incident submission
- background cleanup
- startup import

### Step 2. Count Work Per Refresh Or Action

Write down:

- how many calls happen
- how often they repeat
- whether each call reads, writes, or both
- whether the call fans out internally

For browser apps, include auto-refresh intervals.
For jobs, include the schedule frequency.

### Step 3. Compare Database Time With Full Endpoint Time

Measure both:

- raw database/procedure timing
- full app endpoint timing

This separates:

- database inefficiency
- app/server inefficiency
- network or proxy overhead

### Step 4. Inspect Data Shape

Check:

- which rows are large
- which fields are embedded arrays
- which paths rewrite those rows
- whether retention keeps them growing

### Step 5. Inspect Access Shape

Search for:

- `iter()`
- broad `list` calls
- per-item follow-up reads
- repeated sorting and filtering
- reads that also call `put`, `insert`, `upsert`, or `delete`

### Step 6. Rank By Total Cost, Not By Style Preference

Estimate:

- cost per call
- calls per user per hour
- calls per job run
- expected concurrency

The true top hotspot is the one with the biggest repeated footprint, not necessarily the ugliest code.

## Search Tactics

When auditing a codebase, these searches are usually high signal:

```sh
rg -n "setInterval|setTimeout|Ticker|NewTicker|cron|schedule"
rg -n "iter\\(|filter\\(|find\\(" path/to/db/module
rg -n "insert\\(|update\\(|upsert\\(|delete\\(|put" path/to/db/module
rg -n "ensure[A-Z]" .
rg -n "List|Get|Fetch|CallProcedure|ServiceGet|ServiceList" .
```

Then inspect whether:

- a read path calls a write helper
- a loop causes per-item database access
- a periodic loop triggers heavy views

## Train-Bot Lessons That Generalize

The train-bot investigation produced a few durable lessons:

- A batched dashboard procedure was far cheaper than a server path that fanned out into many reads.
- A raw database procedure can be healthy even when the app endpoint around it is slow.
- Public polling creates a steady cost floor even when each individual call is "fast enough."
- A cleanup job that touches every row every minute can become a top cost driver.
- Saving a large rider or activity blob on small changes creates write amplification.
- Scanning all activity rows and filtering in code gets more expensive every day as history grows.

## Review Checklist

Before you call a resource-efficiency task done, verify all of these:

- The hottest user or job path is identified.
- Polling intervals are documented.
- Fanout calls per refresh are counted.
- Broad scans are identified.
- Write-on-read behavior is identified.
- Large row rewrite patterns are identified.
- At least one live timing or representative benchmark was captured if possible.
- Findings are ranked by likely total spend, not just by latency.
- Recommended fixes start with removing repeated work.

## What To Recommend First

In most cases, recommend fixes in this order:

1. Remove duplicated work.
2. Remove write-on-read behavior.
3. Batch fanout reads into one view builder.
4. Replace full scans with indexed or narrower access paths.
5. Split large hot rows from bulky history.
6. Slow or gate polling where product allows.
7. Reduce payload size.
8. Tune smaller CPU details only after the large structural issues are fixed.

## Non-Goals

Do not spend time on tiny optimizations while any of these remain true:

- one request causes many follow-up requests
- a background job rewrites unchanged rows
- a read path performs writes
- a public view polls aggressively with large payloads
- a hot procedure scans whole tables

## Output Template For Future Audits

When writing up findings for a future app, use this shape:

### Resource Model

- Which platform meters matter here?
- Which code patterns map to those meters?

### Measured Hot Paths

- What was timed?
- How often does it run?
- How large are the payloads?

### Ranked Findings

- Which path is probably the biggest total spender?
- Why?
- What evidence supports that ranking?

### Fix Order

- What should be changed first?
- Which changes reduce repeated work immediately?
- Which deeper schema or architecture changes can wait?

## Final Reminder

Fast enough is not the same as cheap enough.

A call that looks acceptable in isolation can still dominate billing if it:

- runs constantly
- runs publicly
- rewrites too much data
- scans too much data
- or triggers more work behind the scenes than the UI suggests

Always reason about cost as:

`cost per call x calls per hour x number of active clients/jobs`
