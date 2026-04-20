# SHIP Pairing-2 Audit Follow-ups

Bugs identified during the hub connection-handling audit that are **specific to the `feature/shippairing-2` branch** — they do not exist on `dev`. Tracked here so they can be tackled after the dev-branch root-cause fixes (`fix/hub-connection-bugs`) land and shippairing-2 rebases on top.

## Context

The dev-branch audit found 10 bugs in the connection-handling primitives (locking, shutdown coordination, callback gating). Those are being fixed on `fix/hub-connection-bugs` via either targeted TDD or an architectural refactor (`connectionRegistry` + `Hub.ctx` + `WaitGroup`).

The four items below are **additional bugs** that shippairing-2 introduced — not because it rewrote connection management, but because its new features (AddCu device replacement, `ServiceIdentity` callback migration, etc.) added new code paths that funnel through the (already-buggy) primitives in shapes the dev branch doesn't exhibit.

**Many of these follow-ups may resolve themselves once the dev-branch fixes land.** Re-verify each one against the rebased code before writing tests or fixes.

---

## F1 — `coordinateConnectionInitations`: 4th `connectionAttemptRunning` leak path

**Location (shippairing-2):** `hub/hub_connections_retry.go` `coordinateConnectionInitations` ~lines 17–24

**Description:**
On shippairing-2, `coordinateConnectionInitations` was extended with a `service := h.ServiceForIdentifier(ski, "")` lookup followed by `if service == nil { return }` — but the early-return happens **after** `setConnectionAttemptRunning(ski, true)`. The flag is never cleared on this path, permanently silencing future reconnect attempts for that SKI until process restart.

Dev doesn't have this check at all (counter increment is the only thing between `setConnectionAttemptRunning` and the timer scheduling), so dev only suffers the 3 leak paths inside `prepareConnectionInitation`.

**Why shippairing-2-specific:** shippairing-2 added the service-nil check (likely to avoid scheduling timers for services that were unregistered between mDNS event and coordinator).

**Resolution check after dev fixes land:**
- If the dev-branch fix (option A) adds `defer h.setConnectionAttemptRunning(ski, false)` at the **top** of `coordinateConnectionInitations` (covering ALL exit paths), this follow-up is automatically resolved.
- If the dev fix only patches `prepareConnectionInitation`, this follow-up still applies.

**TDD test (if still needed):**
- File: `hub/hub_bugs_test.go`
- Name: `Test_F1_AttemptFlagStuckOnNilService`
- Setup: hub with no registered service for the test SKI
- Trigger: call `coordinateConnectionInitations(ski, entry)`
- Assert: `isConnectionAttemptRunning(ski) == false` after the call

**Fix sketch:** Add `defer h.setConnectionAttemptRunning(ski, false)` immediately after `h.setConnectionAttemptRunning(ski, true)`. Idempotent with `initateConnection`'s own defer.

---

## F2 — `HandleConnectionClosed` starts AddCu replacement timer for a replaced connection

**Location (shippairing-2):** `hub/hub_shipconnection.go` `HandleConnectionClosed` ~lines 25–63 (the AddCu replacement-tracker block ~lines 56–60)

**Description:**
On shippairing-2, when the bigger-SKI side wins a double-connection swap and closes the old connection, `HandleConnectionClosed` fires for the old (now-replaced) connection. In addition to the spurious `RemoteServiceDisconnected` callback (the dev-branch bug 3), shippairing-2 also starts the **15-minute AddCu replacement timer** for AddCu-paired devices. If the new connection's handshake fails or stalls for >15 min, that timer removes trust from a device that was never actually disconnected.

In practice the timer is usually cancelled by `HandleShipHandshakeStateUpdate` when the new handshake completes (`StopAddCuReplacementTimer`) — so this is mostly a UX hazard (spurious "device offline" → "device replaced" UI flicker) plus a tail-risk of trust loss if the new handshake takes pathologically long.

Dev has no AddCu logic at all → not present on dev.

**Why shippairing-2-specific:** shippairing-2 added the AddCu replacement tracker.

**Resolution check after dev fixes land:**
- The dev-branch fix for bug 3 (gate on `UnregisterConnectionIfMatch`'s return value) **fully resolves this** when shippairing-2 rebases. The replacement-timer block sits below the same `RemoteServiceDisconnected` call and benefits from the same gate.
- If the rebase is non-trivial (the AddCu block was added between the unregister call and the disconnect callback), re-verify the gate covers both.

**TDD test (if still needed):**
- File: `hub/hub_bugs_test.go`
- Name: `Test_F2_AddCuReplacementTimerNotStartedForReplacedConnection`
- Setup: register `connOld` for SKI; replace via `keepThisConnection`/`registerConnection` with `connNew`; service has `PairingType == PairingTypeAddCu`
- Trigger: call `HandleConnectionClosed(connOld, true)`
- Assert: `addCuReplacementTracker.IsActiveFor(shipID) == false`

**Fix sketch:**
```go
if !h.UnregisterConnectionIfMatch(remoteSki, connection) {
    return // we never owned this slot — this connection was already replaced
}
// rest of HandleConnectionClosed unchanged
```

---

## F3 — `HandleShipHandshakeStateUpdate` 500-ms goroutine reads `service` pointer

**Location (shippairing-2):** `hub/hub_shipconnection.go` `HandleShipHandshakeStateUpdate` ~lines 148–153

**Description:**
On shippairing-2, the delayed callback is:
```go
go func() {
    <-time.After(time.Millisecond * 500)
    pairingIdentity := service.ToServiceIdentity()  // reads service state under no lock
    h.hubReader.ServicePairingDetailUpdate(pairingIdentity, pairingDetail)
}()
```
The closure captures `service *api.ServiceDetails` (a pointer). 500 ms later, `service.ToServiceIdentity()` reads the service's fields. If `UnregisterRemoteService` runs in that window and mutates the service (`SetTrusted(false)`, etc.), the goroutine reads concurrently → data race detected by `-race`.

Dev doesn't have this race because dev's callback signature is `ServicePairingDetailUpdate(ski string, detail *ConnectionStateDetail)` — captures `ski` (immutable string value) and `pairingDetail` (pointer to a freshly-constructed struct that nothing else mutates). No service-pointer dereference inside the goroutine.

**Why shippairing-2-specific:** shippairing-2's `ServiceIdentity` callback migration moved the `service.ToServiceIdentity()` call from before the goroutine to inside it.

**Resolution check after dev fixes land:**
- This is **not** resolved by the dev-branch fixes — it's a property of shippairing-2's callback signature, which dev doesn't have.
- Will need a fix on shippairing-2 specifically.

**TDD test:**
- File: `hub/hub_bugs_test.go` (on shippairing-2)
- Name: `Test_F3_ServicePointerRaceIn500msGoroutine`
- Required flag: `-race`
- Setup: register service with SKI; trigger `HandleShipHandshakeStateUpdate`
- Race window: immediately call `UnregisterRemoteService(identity)` which mutates the same `service`
- Assert: race detector clean over N iterations (N=20)

**Fix sketch:** Capture `pairingIdentity` **before** launching the goroutine:
```go
pairingIdentity := service.ToServiceIdentity()  // read state synchronously, while caller still holds context
go func() {
    <-time.After(time.Millisecond * 500)
    h.hubReader.ServicePairingDetailUpdate(pairingIdentity, pairingDetail)
}()
```
One-line move. `ServiceIdentity` is a value type (no sync primitives per CLAUDE.md), so the captured copy is race-free.

---

## F4 — Lock-order inversion in `startAddCuReplacementTimersForOfflineDevices`

**Location (shippairing-2):** `hub/hub.go` `startAddCuReplacementTimersForOfflineDevices` ~lines 589–606

**Description:**
On shippairing-2, this startup-scan function holds `muxReg.RLock` and calls `connectionForService(svc)`, which itself acquires `muxCon.RLock` and then re-acquires `muxReg.RLock` inside an iteration loop (to look up the service for each connection's SKI). Lock acquisition order: `muxReg → muxCon → muxReg`.

Go's `sync.RWMutex` is **not re-entrant** for nested `RLock` if a writer is queued: writers preempt new readers. Concurrent sequence that deadlocks:
- G1: holds outer `muxReg.RLock`, holds `muxCon.RLock`, blocks on inner `muxReg.RLock` (queued behind a pending writer)
- G2 (writer): waiting for `muxReg.Lock`, queued ahead of G1's inner `RLock`
- G3: holds `muxCon.Lock` somewhere, waiting for `muxReg.Lock` → eventually queues behind G2

If G3 is `addService` / `removeService` / similar, all three goroutines deadlock.

Dev has neither `startAddCuReplacementTimersForOfflineDevices` nor a `connectionForService` that nests `muxReg` reads — `connectionForSKI` on dev only takes `muxCon.RLock` and does no further locking. Not present on dev.

**Why shippairing-2-specific:** shippairing-2's `connectionForService` (lookup by service rather than SKI) iterates the connections map and resolves each connection's service via `muxReg`, creating the nested pattern.

**Resolution check after dev fixes land:**
- **Not** resolved by the dev-branch fixes — this is shippairing-2-specific code.
- The dev-branch architectural option B (`connectionRegistry`) might subsume `connectionForService` into the registry itself, making the muxReg nesting unnecessary. Worth re-checking after rebase.

**TDD test:**
- File: `hub/hub_bugs_test.go` (on shippairing-2)
- Name: `Test_F4_NestedRLockDeadlock`
- Approach: deadlock watchdog (`time.After(2*time.Second)` + `t.Fatal`)
- Setup: insert services + connections; spawn G1 calling `startAddCuReplacementTimersForOfflineDevices`; spawn G2 calling `addService` (writer on `muxReg`); use channels to synchronize so G2 queues for the writer lock while G1 is between its outer `muxReg.RLock` and its inner `muxReg.RLock`

**Fix sketch:** Snapshot candidate services into a local slice while holding the outer `muxReg.RLock`, release the lock, then call `connectionForService` per candidate without holding any registry lock:
```go
h.muxReg.RLock()
candidates := make([]*api.ServiceDetails, 0, len(h.remoteServices))
for _, svc := range h.remoteServices {
    if shouldStartTimer(svc) { candidates = append(candidates, svc) }
}
h.muxReg.RUnlock()
for _, svc := range candidates {
    if conn := h.connectionForService(svc); conn != nil { ... }
}
```

Same pattern fix as the standard nested-RWMutex anti-pattern.

---

## Cross-reference: dev-branch work that influences these follow-ups

| Follow-up | Auto-resolved by dev fix? | Re-test on shippairing-2 after rebase? |
|-----------|---------------------------|----------------------------------------|
| F1 (4th flag-leak path) | Yes if dev fix uses top-level defer in `coordinateConnectionInitations` | Yes |
| F2 (AddCu spurious replacement timer) | Yes — gate on `UnregisterConnectionIfMatch` covers the AddCu block too | Yes (verify the gate sits above the AddCu block in the rebased code) |
| F3 (500-ms service-pointer race) | No — shippairing-2-only callback signature | Always (write test, apply fix) |
| F4 (nested `muxReg` RLock deadlock) | No — shippairing-2-only function. Possibly subsumed if dev option B `connectionRegistry` lands | Yes — re-design `connectionForService` to use the new registry, then this nesting may evaporate |

## Order of work

1. Land dev-branch fixes (`fix/hub-connection-bugs`).
2. Rebase `feature/shippairing-2` onto the new dev tip.
3. Re-verify each follow-up against the rebased code (some may already be green).
4. For survivors: write failing test, apply fix, repeat.
