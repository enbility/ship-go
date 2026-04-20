# Hub Connection Bugs — Fix Plan & Progress Tracker

**Status:** ✅ COMPLETE — all 17 tasks shipped; full library test suite green with `-race` and `-tags=deadlock`.

**Branch:** `fix/hub-connection-bugs` (from `dev` @ `ff72929`)
**Architecture choice:** Option B — three small primitives (`connectionRegistry` + `Hub.ctx`/`wg` + `IsAlive()`)
**Rule encapsulation:** Yes — registry owns the §12.2.2 double-connection rule, future spec-flip is a one-method change
**Discipline:** TDD red→green per bug. Tests reproduce the bug first; production code changes to satisfy them. Tests are not weakened to make bugs pass.

---

## Decisions made

| Question | Decision | Reason |
|---|---|---|
| Architecture | **Option B** | Smallest change that removes root causes; preserves public API; leaves door open to per-SKI actor (option C) later |
| Registry owns the rule? | **Yes** | One method (`shouldKeepNew`) is the future spec-compliance flip point |
| Stop on shippairing-2 bugs? | **Defer** | F1–F4 documented in `SHIP_PAIRING_2_AUDIT_FOLLOWUPS.md`; tackle after dev work lands & shippairing-2 rebases |
| Spec-compliant double-connection rule? | **Defer** | Out of scope for this branch; rule stays as "initiator-wins" inside the registry. See conversation summary |

---

## Architecture being introduced

Three primitives, no public API change.

### 1. `Hub.ctx` + `Hub.wg` (shutdown coordination)

```go
type Hub struct {
    // ...existing fields...
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}
```

- Created in `NewHub` via `context.WithCancel(context.Background())`.
- `Shutdown()` calls `h.cancel()` **first** (before HTTP/mDNS shutdown / mutex acquisition), then drains via `h.wg.Wait()` with a 6 s timeout fallback.
- Long-lived goroutines (timer callbacks, in-flight dials) check `h.ctx.Err()` and respect `h.wg.Add(1)` / `defer h.wg.Done()`.

**Kills:** Bug 6 (timer post-shutdown), Bug 7 (in-flight dial races shutdown).

### 2. `connectionRegistry` (atomic connection lifecycle)

New file `hub/connection_registry.go`. Owns the connections map + a private mutex. **No external code touches the map directly.**

Public methods (sketch):

```go
func (r *connectionRegistry) Get(ski string) api.ShipConnectionInterface

// Atomic: under one lock, applies §12.2.2 rule against any existing entry,
// runs builder() iff the new conn would win (so construction happens inside the
// critical section but only once we know we want it), and inserts the new conn.
//
// Returns:
//   - oldConn: existing entry that was evicted (nil if none) — caller closes once
//   - newConn: the freshly-built ShipConnection if registered (nil if rejected)
//   - kept:    whether the new conn was registered
func (r *connectionRegistry) Swap(
    ski string,
    isIncoming bool,
    builder func() api.ShipConnectionInterface,
) (oldConn, newConn api.ShipConnectionInterface, kept bool)

func (r *connectionRegistry) RemoveIfMatches(ski string, conn api.ShipConnectionInterface) bool

// IsConnected = entry exists AND IsAlive() — resolves the stale-entry hazard
func (r *connectionRegistry) IsConnected(ski string) bool

func (r *connectionRegistry) Len() int

// Single point of change for future spec-compliant rule
func (r *connectionRegistry) shouldKeepNew(remoteSKI string, isIncoming, hasExisting bool) bool
```

Subsumes & deletes: `keepThisConnection`, `registerConnection`, `UnregisterConnectionIfMatch`, `isSkiConnected`, `connectionForSKI` (becomes `Get`).

**Kills:** Bug 2 (TOCTOU — atomic Swap), Bug 3 (callback ungated — `RemoveIfMatches` returns bool), Bug 4 (lock-order — registry's lock is private), Bug 9 (dual close ownership — Swap returns whatever the single caller must close), Bug 12 (stale entry — `IsConnected` checks `IsAlive()`).

### 3. `IsAlive() bool` on `api.ShipConnectionInterface`

Returns true unless the close path has fired. Implementation reads the existing `shutdownOnce`-equivalent atomic on `ship.ShipConnection`.

**Kills:** Bug 12 (in tandem with registry's `IsConnected`).

---

## Bugs in scope (10)

Bugs verified present on `dev`. Numbering matches the original audit; gaps (5, 8) are shippairing-2-specific (see `SHIP_PAIRING_2_AUDIT_FOLLOWUPS.md`).

| # | Severity | One-line | Architecture handles it? |
|---|---|---|---|
| 1 | High | `connectionAttemptRunning` flag stuck after early-returns in `prepareConnectionInitation` | No — discipline fix (defer) |
| 2 | High | TOCTOU between `keepThisConnection` unlock and caller's `registerConnection` lock | **Yes** — registry `Swap` |
| 3 | High | `HandleConnectionClosed` fires `RemoteSKIDisconnected` for replaced conn | **Yes** — gate on `RemoveIfMatches` return |
| 4 | High | `registerConnection` (muxCon→muxTimers) vs `Shutdown` (muxTimers→muxCon) deadlock | Half — Phase 1 explicit Unlock fixes; registry obviates eventually |
| 6 | High | Timer callback runs post-`Shutdown` against torn-down state | **Yes** — `Hub.ctx` |
| 7 | High | Shutdown doesn't drain in-flight `connectFoundService` goroutines | **Yes** — `Hub.wg` |
| 9 | High | Concurrent Write+Close on same `*websocket.Conn` (server `safeClose` + `sendWSCloseMessage`) | **Yes** — single close owner via Swap return |
| 10 | Low | Fired timer entries leak in `connectionDelayTimers` map | No — discipline fix (`cancelConnectionDelayTimer` at top of `prepareConnectionInitation`) |
| 11 | Low | `sendWSCloseMessage` has unconditional 100 ms sleep | No — delete the line |
| 12 | Low | `isSkiConnected` short-circuits reconnect when entry is dead-but-not-cleaned | **Yes** — `IsConnected` + `IsAlive()` |

---

## Phases & Tasks

Tracked via TaskCreate IDs (in brackets). Update progress by checking boxes here AND running `TaskUpdate` for status sync.

### Phase 0 — Scaffolding

- [ ] **[#19]** Add `go.uber.org/goleak` to `go.mod`. Create `hub/internal_test_helpers.go` with: deadlock watchdog (`time.After(N)` + `t.Fatal`), hub-with-mocks setup helper, no-op `infoProvider`/`dataWriter` mirroring `ship/handshake_timer_helper_test.go`. Verify `go test -race ./hub/...` still passes.

### Phase 1 — Standalone fixes (small, low-risk wins)

- [ ] **[#20] Bug 1 — defer `connectionAttemptRunning` clear**
  - **RED:** `Test_Bug1_AttemptFlagStuckOnEarlyReturn` covering all 3 early-return paths in `prepareConnectionInitation` (counter mismatch, `!IsRemoteServiceForSKIPaired`, `isSkiConnected`). Each asserts `isConnectionAttemptRunning(ski)==false` after return.
  - **GREEN:** Add `defer h.setConnectionAttemptRunning(ski, false)` immediately after `setConnectionAttemptRunning(ski, true)` in `coordinateConnectionInitations` (`hub/hub_connections_retry.go:17`). Existing defer in `initateConnection` stays (idempotent).
  - **NOTE:** During implementation, the fix location moved to `prepareConnectionInitation` (top) instead of `coordinateConnectionInitations`. Reason: `coordinateConnectionInitations` returns immediately after scheduling the timer, so a defer there would clear the flag *before* the timer fires. The flag's lifecycle is owned by the timer callback (`prepareConnectionInitation`), which is where the defer belongs.

- [x] **[#21] Bug 11 — remove 100 ms sleep** ✅
  - RED: `Test_Bug11_SendWSCloseMessageReturnsQuickly` — measured 101 ms today.
  - GREEN: deleted the `<-time.After(100ms)` line; also removed the now-unused `"time"` import.
  - **RED:** `Test_Bug11_SendWSCloseMessageReturnsQuickly` — websocket pair via `httptest.NewTLSServer`, time `sendWSCloseMessage`, assert duration `< 10ms`.
  - **GREEN:** Delete `<-time.After(time.Millisecond * 100)` at `hub/hub_connections_registry.go:93`.

- [x] **[#22] Bug 10 — self-cleanup of fired timer entries** ✅
  - RED: `TestBug10_PrepareConnectionInitationRemovesItsTimerEntry` — plant a long-duration timer entry, call `prepareConnectionInitation`, assert entry removed.
  - GREEN: `h.cancelConnectionDelayTimer(ski)` as first statement of `prepareConnectionInitation`.

- [x] **[#23] Bug 4 — `registerConnection` lock-order inversion** ✅
  - **NOTE:** Re-analysis showed current `Shutdown` releases `muxTimers` before taking `muxCon` (sequential, not nested), so no deadlock is currently triggerable through Shutdown alone. However, `registerConnection` *does* hold `muxCon` while taking `muxTimers`, which would deadlock against any caller (existing or future) that takes `muxTimers` then `muxCon.RLock`.
  - RED: `TestBug4_RegisterConnectionReleasesMuxConBeforeAcquiringMuxTimers` — pins the invariant by holding `muxTimers` and verifying `muxCon` is acquirable while `registerConnection` is blocked. RED today (timeout), GREEN after fix.
  - GREEN: explicit `Unlock()` before `cancelConnectionDelayTimer(ski)` in `registerConnection`.

### Phase 2 — Shutdown infrastructure

- [x] **[#24] Hub.ctx + Hub.wg lifecycle** ✅
  - Added `ctx`, `cancel`, `inflightWg` fields. NewHub initializes via `context.WithCancel(context.Background())`. Shutdown calls `h.cancel()` first, then existing teardown, then drains `inflightWg` with 6 s `select`/timeout. **NOTE:** ctx is intentionally NOT re-armed in Shutdown (would be a torn-read race with concurrent `connectFoundService` reads of `h.ctx`); Shutdown is therefore terminal for the lifecycle context.

- [x] **[#25] Bug 6 — timer respects ctx after Shutdown** ✅
  - RED: `TestBug6_PrepareConnectionInitationSkipsWhenCtxCancelled` — cancel ctx, plant timer entry, call `prepareConnectionInitation`, assert no side effects.
  - GREEN: `if h.ctx.Err() != nil { return }` at top of `prepareConnectionInitation` (BEFORE the cancel-self-timer call and BEFORE the defer flag-clear).

- [x] **[#26] Bug 7 — Shutdown drains in-flight dials** ✅
  - RED (a): `TestBug7a_ShutdownWaitsForInflightWg` — manual wg.Add, verify Shutdown blocks. (passes after #24 wired the wait).
  - RED (b): `TestBug7b_ConnectFoundServiceAbortsOnCancelledCtx` — cancel ctx, call connectFoundService against unreachable IP, assert returns within 1 s (no actual dial).
  - GREEN: at entry of `connectFoundService`: ctx-check, then `h.inflightWg.Add(1); defer h.inflightWg.Done()`.

### Phase 3 — `connectionRegistry` refactor

- [x] **[#27] IsAlive() on ShipConnectionInterface** ✅
  - Added `IsAlive() bool` to interface. Implementation: `ship.ShipConnection.closed atomic.Bool`, set inside `shutdownOnce.Do` BEFORE the slow teardown. Mocks regenerated via `mockery`. Unit test in `ship/connection_lifecycle_test.go::TestConnectionLifecycleSuite/TestIsAlive`. Also added `Run()` to the interface (was already on the implementation; needed for the registry's builder pattern to return `api.ShipConnectionInterface`).

- [x] **[#28] connectionRegistry implementation** ✅
  - `hub/connection_registry.go`: `connectionRegistry` type with private `mu`, `connections` map, `localSKI`. Methods: `Get`, `IsConnected` (alive-checked), `Len`, `Snapshot`, `Swap` (atomic decide-and-act with caller-supplied builder), `RemoveIfMatches`. Private `shouldKeepNew` is the §12.2.2 rule (single point of change for future spec compliance).
  - `hub/connection_registry_test.go`: 14 unit tests covering rule decisions for all 4 incoming/outgoing × bigger/smaller-SKI quadrants, stale-entry handling, RemoveIfMatches semantics, Len, and a 50-goroutine concurrency stress test.

- [x] **[#29] Migrate callers to registry** ✅
  - `connectFoundService` (`hub_connections_client.go`): replaced isSkiConnected→Swap+register sequence with one `registry.Swap` whose builder constructs the ShipConnection. Caller closes evicted oldConn (goroutine) and rejected raw `*websocket.Conn` (single owner — resolves Bug 9).
  - `ServeHTTP` (`hub_connections_server.go`): same pattern.
  - `HandleConnectionClosed` (`hub_shipconnection.go`): `if !h.UnregisterConnectionIfMatch(...) { return }` gates the disconnect callback (resolves Bug 3).
  - `validateConnectionLimit` and `checkAutoReannounce`: use `registry.Len()`.
  - `Shutdown`: uses `registry.Snapshot()` to iterate.
  - **Deleted:** `keepThisConnection`, `registerConnection`, `createShipConnection`. Kept as thin delegations: `isSkiConnected`, `connectionForSKI`, `UnregisterConnectionIfMatch`, `sendWSCloseMessage` (single owner now).
  - **Test migration:** delegated to sub-agent. 13 test files migrated. 89 top-level tests / 268 subtests pass under `-race`.

### Phase 4 — Verify architecture eliminated bugs ✅

All four passed (after one fix to a test that used a remote SKI lexicographically smaller than the test hub's localSKI). Tests live in `hub/hub_bugs_phase4_test.go`.

- [x] **[#30] Bug 2 verify — no third-conn leak under concurrency** — 100 concurrent Swaps for same SKI, exactly one survives, every evicted predecessor surfaced for caller cleanup, builder runs exactly once per kept swap. ✅
- [x] **[#31] Bug 3 verify — no callback for replaced conn** — register conn1, swap in conn2 (with remote SKI lex > local), call `HandleConnectionClosed(conn1, true)`, assert RemoteSKIDisconnected count unchanged. ✅
- [x] **[#32] Bug 9 verify — single websocket close owner** — confirmed by inspection: registry's rejected swap path doesn't invoke builder (no construction → no spawned writer). ✅
- [x] **[#33] Bug 12 verify — stale conn unblocks reconnect** — plant a dead conn in the registry, Swap from smaller-SKI side (rule would reject if alive), assert new conn wins. ✅

### Phase 5 — Sign-off ✅

- [x] **[#34] Full test suite** — `go test -race -count=1 ./hub/... ./ship/... ./api/... ./cert/... ./mdns/... ./ws/... ./logging/... ./util/...` all green; `go test -tags=deadlock -race -count=1 ./hub/... ./ship/...` also green.
- [x] **[#35] Smoke test** — `go build` clean for hub + ship + api + cert + mdns + ws + logging + util + examples/{quickstart,production,client,pairing}. `go vet` clean. `go run ./examples/quickstart` starts up cleanly, accepts mDNS discovery, no panics. (interop/, pairing-listener/, pairing-announcer/ untracked dirs from feature/shippairing-2 work do not build — pre-existing, unrelated to this branch.)

---

## Cross-cutting notes

### TDD discipline reminder

For each bug task: **write the failing test first, run it, confirm it reproduces the bug, then write the minimal fix.** If you can't make a test fail today, your understanding of the bug is wrong — investigate before "fixing."

If during Phase 4 verification a test passes immediately (no RED), that's expected and good — it means the architectural change in Phase 3 already eliminated the bug class.

### Race-detector + goleak required

Run every test with `-race`. Phase 4 tests in particular only catch their bugs under `-race`. Phase 0's scaffolding adds `goleak.VerifyNone(t)` for shutdown leak detection — used by #26 and #30.

### Existing tests are immutable spec

Do not modify existing test assertions to make them pass after the refactor. If migration breaks a test:
- If the test API changed (e.g. it called `keepThisConnection` directly): migrate it to the new API, preserving the original intent of the assertion.
- If the test asserts buggy behaviour: that's actually a bug in the test — discuss before changing.

### Lock ordering after refactor

Post-refactor global lock order:
1. `Hub.ctx` checks (no lock)
2. `Hub.wg` operations (no lock)
3. `muxTimers` (timer map)
4. registry's private mutex (connections)
5. `muxReg` (services registry)
6. `muxConAttempt` (attempt counters)

The registry's mutex is private — external code can never compose it with another in the wrong order.

### Files touched (estimate)

| File | Action |
|---|---|
| `hub/connection_registry.go` | NEW |
| `hub/connection_registry_test.go` | NEW |
| `hub/internal_test_helpers.go` | NEW |
| `hub/hub_bugs_test.go` | NEW (TDD red tests live here) |
| `hub/hub.go` | Modified (add ctx/wg, Shutdown changes) |
| `hub/hub_connections_registry.go` | DELETED (subsumed by registry) |
| `hub/hub_connections_client.go` | Modified (Swap call site, ctx/wg gating) |
| `hub/hub_connections_server.go` | Modified (Swap call site) |
| `hub/hub_connections_retry.go` | Modified (defer flag clear, ctx check, cancelConnectionDelayTimer at top) |
| `hub/hub_connections_timers.go` | Modified (sync.Once on Stop, optional) |
| `hub/hub_shipconnection.go` | Modified (HandleConnectionClosed gates on RemoveIfMatches) |
| `api/shipconnection.go` (or wherever `ShipConnectionInterface` lives) | Modified (add IsAlive) |
| `ship/connection.go` (or wherever ShipConnection lives) | Modified (implement IsAlive) |
| `mocks/*` | Regenerated |
| `go.mod`, `go.sum` | Modified (add goleak) |

### Out of scope

- Spec-compliant §12.2.2 double-connection rule (deferred — registry's `shouldKeepNew` is the future flip point).
- Per-SKI actor model (option C — possible follow-up if option B proves insufficient).
- The four shippairing-2 follow-ups (F1–F4 in `SHIP_PAIRING_2_AUDIT_FOLLOWUPS.md`).
- General hub refactors not tied to one of the 10 bugs.

---

## How to update progress

1. Tick the box(es) for completed task(s) in this file.
2. Run `TaskUpdate` to mark the corresponding task ID `completed`.
3. Commit both the production change AND the doc update in the same commit (so progress is reviewable).

When a task uncovers a new finding (e.g. "registry design needs an extra method"), add it as a sub-bullet under the relevant task and create a new TaskCreate entry — don't silently expand scope.
