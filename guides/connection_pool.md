# Connection Pool Architecture

This guide explains the internal connection pool design used by the Go client's transport layer. Understanding this architecture helps with tuning, debugging, and extending the client.

## Pool Structure

Every policy (RoundRobin, Role, Mux, etc.) owns a `statusConnectionPool` that manages connections in two slices -- **ready** and **dead**:

```
statusConnectionPool
┌──────────────────────────────────────────────────────────────────┐
│                                                                  │
│  ready[]            (single slice, two logical partitions)       │
│  ┌──────────────────────────┬───────────────────────────┐        │
│  │        active            │         standby           │        │
│  │  ready[0 : activeCount]  │  ready[activeCount : len] │        │
│  │                          │                           │        │
│  │  Round-robin selection   │  Idle, promoted on demand │        │
│  │  Serves production       │  Health-checked before    │        │
│  │  traffic                 │  promotion                │        │
│  └──────────────────────────┴───────────────────────────┘        │
│                             ▲ activeCount boundary               │
│                                                                  │
│  dead[]             (separate slice)                             │
│  ┌──────────────────────────────────────────────────────┐        │
│  │  Failed connections, sorted by failure count         │        │
│  │  Subject to resurrection via health checks           │        │
│  │  Used as zombie fallback when ready[] is empty       │        │
│  └──────────────────────────────────────────────────────┘        │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

### Selection Priority

When `Next()` is called, the pool tries connections in this order:

```
1. Active partition    ready[0:activeCount]    Round-robin, warmup-aware
         │
         ▼ (all active exhausted or externally demoted)
2. Standby partition   ready[activeCount:]     Emergency promotion, no warmup
         │
         ▼ (no standby available)
3. Dead list           dead[]                  Zombie mode -- rotate and retry
         │
         ▼ (nothing anywhere)
4. Error               ErrNoConnections
```

## Active List Cap

The `activeListCap` controls how many connections serve production traffic simultaneously. This prevents fan-out overload in large clusters where routing requests to all nodes is wasteful.

```
                    activeListCap = 5
                         │
                         ▼
  ready[]:  [ A  B  C  D  E │ F  G  H  I  J  K ]
             ├─────────────┤ ├────────────────────┤
               active (5)          standby (6)

  With cap disabled (activeListCap = 0):
  ready[]:  [ A  B  C  D  E  F  G  H  I  J  K ]
             ├───────────────────────────────────┤
               all active (11), no standby
```

### Dynamic Recalculation

The cap is recalculated on every `DiscoveryUpdate`:

```
  activeListCapConfig = nil (auto-scale)
  ──────────────────────────────────────

    DiscoveryUpdate(poolSize=N):
      activeListCap <- N           (scales with cluster)

  activeListCapConfig = &5 (user-specified)
  ──────────────────────────────────────────

    DiscoveryUpdate(poolSize=N):
      activeListCap <- 5           (fixed, ignores cluster size)
```

The warmup parameters are also recalculated at this point:

```
  n = min(activeListCap, poolSize)    when cap > 0
  n = poolSize                        when cap = 0

  rounds    = clamp(n, 4, 16)         minWarmupRounds..maxWarmupRounds
  skipCount = rounds * 2              warmupSkipMultiple = 2

  Examples:
    poolSize=3,  cap=0  -> n=3  -> rounds=4,  skip=8
    poolSize=5,  cap=5  -> n=5  -> rounds=5,  skip=10
    poolSize=8,  cap=0  -> n=8  -> rounds=8,  skip=16
    poolSize=16, cap=0  -> n=16 -> rounds=16, skip=32
    poolSize=50, cap=10 -> n=10 -> rounds=10, skip=20
    poolSize=50, cap=0  -> n=50 -> rounds=16, skip=32   (clamped to max)
```

## Standby Promotion

There are two promotion paths -- **graceful** and **emergency** -- and a **rotation** mechanism that keeps standby connections exercised.

### Graceful Promotion

Triggered when a connection leaves the active partition (failure, discovery removal, or overload demotion) and standby connections exist.

```
  OnFailure(C)                         asyncPromoteStandby()
  ─────────────                        ─────────────────────

  1. Move C: active -> dead             1. Claim standby S (TryLock + lcStandbyWarming)
  2. Schedule resurrection             2. Health-check S (N consecutive checks)
  3. Snapshot: hasStandby?             3. If healthy:
  4. If yes -> go asyncPromoteStandby      Promote S: standby -> active (with warmup)
                                       4. If unhealthy:
                                          Move S: standby -> dead

  Timeline:
  ─────────────────────────────────────────────────────────────────
  t0: OnFailure(C)  ->  active=[A B D E]  standby=[F G]  dead=[C]
  t1: health-check F (pass #1)
  t2: health-check F (pass #2)
  t3: health-check F (pass #3)
  t4: promote F     ->  active=[A B D E F*] standby=[G]  dead=[C]
                                       (* = warming up)
```

### Emergency Promotion

Triggered inside `Next()` when all active connections are exhausted (dead or skipped). No health check, no warmup -- immediate service.

```
  Next() -> no active connections available
  ─────────────────────────────────────────

  1. tryStandbyWithLock()
  2. Take next standby at boundary
  3. Strip lcNeedsWarmup (maximize throughput)
  4. Advance activeCount++

  active=[--all exhausted--]  standby=[F G]
     ↓
  active=[F]  standby=[G]    (F promoted without warmup)
```

### Standby Rotation

Triggered periodically by the discovery cycle to keep standby connections exercised and fresh. Each rotation health-checks a standby, promotes it with warmup, and relies on cap enforcement to demote the excess active connection once warmup completes.

```
  rotateStandby(count=1)
  ──────────────────────

  Before:  active=[A B C D E]  standby=[F G H]  cap=5

  1. Claim F (lcStandbyWarming)
  2. Health-check F (pass *3)
  3. Promote F -> active with warmup
  4. active=[A B C D E F*]  standby=[G H]  (temporarily over cap)
  5. F finishes warmup -> deferredCapEnforcement()
  6. Demote oldest non-warming -> standby

  After:   active=[B C D E F]  standby=[A G H]  cap=5
```

## Connection Warmup

When a connection joins the active partition (from standby, dead, or discovery), it goes through a non-linear warmup ramp that gradually increases the fraction of requests it accepts.

### Warmup Curve

The warmup uses two counters packed into the atomic state word:

- **rounds** -- decremented each time a request is accepted
- **skipCount** -- decremented on each `Next()` call; when 0, accept and advance round

The skip count per round decays along a **smoothstep** (Hermite) curve:

```
skip = maxSkip * (1 - 3t^2 + 2t^3)     where t = roundsElapsed / maxRounds
```

This produces an S-shaped acceptance ramp: slow start while the JVM interprets cold bytecode, accelerating middle as C1/C2 JIT compilation kicks in, and a steep finish as the JVM reaches steady-state optimization. The skip count always reaches 0 by the final round, regardless of the `skipCount / rounds` ratio -- no sudden jump to 100%.

```
  warmupState(rounds=16, skipCount=32)
  smoothstep: skip = maxSkip * (1 - 3t^2 + 2t^3)
  (each █ = 1 request skipped before next accept)

  R16 ████████████████████████████████ 32   3% ╮
  R15 ███████████████████████████████  31   3% │ slow soak:
  R14 ██████████████████████████████   30   3% │ JVM interprets
  R13 █████████████████████████████    29   3% ╯ cold bytecode
  R12 ███████████████████████████      27   4% ╮
  R11 ████████████████████████         24   4% │ accelerating:
  R10 █████████████████████            21   5% │ C1/C2 JIT
  R9  ██████████████████               18   5% │ compiles hot
  R8  ████████████████                 16   6% │ methods
  R7  █████████████                    13   7% │
  R6  ██████████                       10   9% ╯
  R5  ███████                           7  13% ╮
  R4  █████                             5  17% │ decelerating:
  R3  ██                                2  33% │ smooth arrival
  R2  █                                 1  50% │ at full traffic
  R1                                    0 100% ╯
```

The smoothstep curve is designed around the JVM's HotSpot JIT compiler on the server side. When a node restarts (or a new connection routes traffic to a node that hasn't seen this client's request patterns), the JVM is running in interpreted mode. The slow initial trickle gives the C1 (client) and C2 (server) compilers time to profile, compile, and deoptimize/recompile hot code paths -- search execution, bulk indexing, aggregation pipelines, and codec paths -- without the node being hammered with full production load while it's still running unoptimized bytecode.

### Skip Count Scaling (Smoothstep)

The smoothstep formula `skip = maxSkip * (R^3 - 3d^2R + 2d^3) / R^3` is computed with integer arithmetic (no floating point). It guarantees the skip count decays to 0 by the final round for any `skipCount / rounds` ratio:

```
  warmupState(16, 32) -- default (skipCount = rounds * 2)
  Round 16->1: skip 32,31,30,29,27,24,21,18,16,13,10,7,5,2,1,0

  warmupState(8, 16)  -- smaller pool
  Round 8->1: skip 16, 15, 13, 10,  8,  5,  2,  0

  warmupState(4, 8)   -- minimal pool (4-node cluster)
  Round 4->1: skip  8,  6,  4,  1
```

### Starvation Prevention

If all active connections are warming and skip, the pool falls back to the connection with the fewest remaining skips (closest to its next accept):

```
  Next() round-robin scan:
    A: warming, skip=5 -> skipped
    B: warming, skip=2 -> skipped  <- bestWarmingConn (lowest skip)
    C: warming, skip=7 -> skipped

  All skipped -> return B (starvation prevention)
```

## Shared Connections Across Policies

Each policy owns its own `statusConnectionPool` with independent ready/dead lists and active/standby partitions. However, all pools that contain a given node share the **same `*Connection` pointer**. The `Connection.state` is an `atomic.Int64` -- changes to lifecycle bits propagate immediately to all pools.

```
  ┌─────────────────────┐   ┌──────────────────────┐
  │ RoundRobinPolicy    │   │ RolePolicy("data")   │
  │                     │   │                      │
  │ pool.ready:         │   │ pool.ready:          │
  │   [*A, *B, *C, *D]  │   │   [*A, *C]           │
  │ pool.dead:          │   │ pool.dead:           │
  │   []                │   │   []                 │
  └────────┬────────────┘   └────────┬─────────────┘
           │                         │
           │      ┌──────────────────┤
           │      │                  │
           ▼      ▼                  ▼
  ┌────────────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐
  │ Connection A   │  │ Conn B  │  │ Conn C  │  │ Conn D  │
  │ state: atomic  │  │ state   │  │ state   │  │ state   │
  │ URL: :9200     │  │ :9201   │  │ :9202   │  │ :9203   │
  │ roles: [data]  │  │ [ingest]│  │ [data]  │  │ [ingest]│
  └────────────────┘  └─────────┘  └─────────┘  └─────────┘
```

### Cross-Pool Failure Propagation

When a connection fails in one pool, the atomic lifecycle bits change immediately. Other pools detect this on the next `Next()` call:

```
  1. RoundRobin.OnFailure(A)
     └─ CAS: state = lcDead|lcNeedsWarmup       (atomic, instant)
     └─ Move A: roundrobin.ready -> roundrobin.dead

  2. RolePolicy("data").Next()
     └─ Reads A.state -> sees no position bits (lcActive|lcStandby == 0)
     └─ Upgrades to write lock -> evicts A from role.ready -> role.dead

  Result: A is in dead list of BOTH pools
  Recovery: first pool to resurrect A clears deadSince, both detect it
```

### Independent Partitions

Each pool manages its own active/standby boundary independently. Connection A can be active in one pool and standby in another -- the position bits (`lcActive`, `lcStandby`) are set per-pool during pool operations.

```
  RoundRobin pool:  A=active,  B=standby     (activeListCap=3)
  RolePolicy pool:  A=active                 (activeListCap=1)

  These are independent -- A serving traffic in both pools is normal.
  Only dead/overloaded state (no position bits) triggers cross-pool eviction.
```

## Connection State Word

Each `Connection` stores its full state in a single `atomic.Int64`, enabling lock-free reads and CAS-based updates:

```
  64-bit connState layout:

  63              52 51                      26 25                       0
  ┌────────────────┬──────────────────────────┬──────────────────────────┐
  │   lifecycle    │     warmupConfig         │      warmupState         │
  │   (12 bits)    │     (26 bits)            │      (26 bits)           │
  └────────────────┴──────────────────────────┴──────────────────────────┘
      connLifecycle      warmupManager              warmupManager
                         (immutable template)       (working counters)
```

### Lifecycle Bits (bits 63-52)

```
  Bit   Hex    Name             Group        Description
  ────  ─────  ───────────────  ──────────   ──────────────────────────────
   0    0x001  lcReady          Readiness    Believed functional
   1    0x002  lcUnknown        Readiness    Status uncertain; needs check

   2    0x004  lcActive         Position     In active partition, serving
   3    0x008  lcStandby        Position     In standby partition, idle

   4    0x010  lcNeedsWarmup    Metadata     Needs warmup before full traffic
   5    0x020  lcOverloaded     Metadata     Node resource overload
   6    0x040  lcHealthChecking Metadata     Health check goroutine running
   7    0x080  lcDraining       Metadata     HTTP/2 GOAWAY; no new requests

   8    0x100  lcNeedsHardware  Extended     Needs hardware info (/_nodes/_local/http,os)
   9-11        (reserved)       Extended     Available for future use

  Readiness: exactly one of {lcReady, lcUnknown} must be set
  Position:  at most one of {lcActive, lcStandby}; neither = dead list
  Metadata:  independent flags, freely combinable
  Extended:  independent flags for hardware discovery and future use
```

### Common State Combinations

```
  State                    Bits          Meaning
  ───────────────────────  ───────────   ─────────────────────────────────
  lcReady|lcActive         0x005         Normal active connection
  lcReady|lcStandby        0x009         Healthy but idle in standby
  lcUnknown                0x002         Dead (no position bits)
  lcUnknown|lcStandby      0x00A         Standby, needs health check
  lcUnknown|lcStandby|     0x01A         Claimed for standby warming
    lcNeedsWarmup
  lcReady|lcActive|        0x015         Active, going through warmup
    lcNeedsWarmup
  lcUnknown|lcOverloaded   0x022         Dead due to overload
  lcReady|lcStandby|       0x029         Standby, overloaded
    lcOverloaded
  lcUnknown|lcDraining     0x082         Dead, HTTP/2 GOAWAY received
  lcUnknown|lcNeedsWarmup| 0x112         New connection, needs hardware info
    lcNeedsHardware
```

### Warmup Manager (26 bits each)

Each warmup manager packs two 8-bit fields into the lower 16 bits of its 26-bit slot (upper 10 bits reserved):

```
  25              16 15        8 7         0
  ┌────────────────┬───────────┬───────────┐
  │   reserved     │  rounds   │ skipCount │
  │   (10 bits)    │  (8 bits) │ (8 bits)  │
  └────────────────┴───────────┴───────────┘

  warmupConfig (bits 51-26): immutable template, set once by startWarmup()
  warmupState  (bits 25-0):  working counters, decremented by tryWarmupSkip()

  When both managers are zero (lower 52 bits all zero), the connection is
  fully warmed -- this gives a zero-cost fast path in Next().
```

## Connection Lifecycle Transitions

```
                         ┌─────────────┐
                         │  Discovery  │
                         │   (new)     │
                         └──────┬──────┘
                                │
                                ▼
          ┌──────────────────────────────────────────────────────┐
          │  lcUnknown | lcNeedsWarmup | lcNeedsHardware        │
          │  (initial state for new conns)                      │
          └──────────────┬──────────────────┬────────────────────┘
                         │                  │
              health check               health check
                passed                     failed
                         │                  │
                         ▼                  ▼
    ┌────────────────────────┐    ┌───────────────────────────────┐
    │  lcActive|lcNeedsWarmup│    │  lcUnknown | lcNeedsHardware  │
    │  (active + warming up) │    │  (dead list, hardware unknown) │
    └───────────┬────────────┘    └────────┬──────────────────────┘
                │                          │
         warmup completes          scheduleResurrect
                │                          │
                ▼                          │
    ┌────────────────────────┐             │
    │  lcReady | lcActive    │◄────────────┘
    │  (fully active)        │     resurrection
    └─────────┬──────────────┘
              │
              │ OnFailure() or
              │ overload detected
              ▼
    ┌──────────────────────────────┐  ┌─────────────────────────┐
    │  lcUnknown | lcNeedsHardware │  │ lcStandby|lcOverloaded  │
    │  (dead, hardware re-check)   │  │ (parked, stats poller   │
    └──────────────────────────────┘  │  manages lifecycle)     │
                                      └─────────────────────────┘
```

When a connection transitions to dead via `OnFailure()`, `lcNeedsHardware` is set so that hardware info is re-verified on resurrection -- the node may have been replaced with different hardware during the outage.

### CAS-Based Lifecycle Transitions

The `casLifecycle()` method atomically modifies lifecycle bits with conflict detection. It uses a CAS loop because lock-free warmup decrements may modify the lower 56 bits concurrently:

```
  casLifecycle(current, conflict, set, clear):
    mask = conflict | set | clear
    next = (lifecycle | set) &^ clear

    If masked bits changed since snapshot -> bail (concurrent transition)
    If next == current -> bail (no-op)
    Otherwise -> CAS and return true
```

This is replacing `advanceLifecycle()` throughout the codebase for safer concurrent state transitions with explicit conflict detection.

## Resurrection and Backoff

Dead connections are resurrected via scheduled health checks with cluster-aware exponential backoff:

```
  resurrectTimeout = max(healthTimeout, rateLimitedTimeout, minimumFloor) + jitter

  healthTimeout      = baseTimeout * (liveNodes / totalNodes)
  baseTimeout        = initial * 2^min(failures-1, cutoff)
  rateLimitedTimeout = (liveNodes * clientsPerServer) / serverMaxNewConnsPerSec

  Capacity model (auto-derived from server core count, default 8):
    clientsPerServer        = coreCount              (default: 8)
    serverMaxNewConnsPerSec = coreCount * 4          (default: 32)

  Live   Dead   Timeout   Behavior
  ────   ────   ───────   ──────────────────────────────────
    0     150   500ms     All dead: most aggressive
   10     140   ~2.5s     Rate limit: (10 * 8) / 32
   50     100   ~12.5s    Rate limit dominates
  100      50   30s       Capped at max
  149       1   30s       Nearly healthy: most conservative
```

### Draining Quiescing

When an HTTP/2 stream reset is observed (e.g., RST_STREAM/REFUSED_STREAM), the connection requires multiple consecutive successful health checks before resurrection (default: 3). This gives the server time to fully quiesce.

```
  Stream reset detected
     │
     ▼
  drainingQuiescingRemaining = 3
     │
     ├─ health check pass -> remaining = 2 (skip resurrection)
     ├─ health check pass -> remaining = 1 (skip resurrection)
     ├─ health check pass -> remaining = 0 (allow resurrection)
     │
     ▼
  resurrectWithLock() proceeds
```

## Weighted Round-Robin

In heterogeneous clusters where nodes have different core counts, the client distributes traffic proportionally to each node's capacity using weighted round-robin selection.

### How Weights Are Determined

Node discovery calls `/_nodes/http,os` which returns each node's `allocated_processors` count. The client normalizes these into integer weights using GCD (greatest common divisor) reduction:

```
  Cluster cores:  [8, 16]          -> GCD=8  -> weights: [1, 2]
  Cluster cores:  [8, 16, 24]      -> GCD=8  -> weights: [1, 2, 3]
  Cluster cores:  [8, 16, 32, 40]  -> GCD=8  -> weights: [1, 2, 4, 5]
  Cluster cores:  [24, 32, 40]     -> GCD=8  -> weights: [3, 4, 5]
  Cluster cores:  [8, 8, 8]        -> GCD=8  -> weights: [1, 1, 1]  (homogeneous)
```

Nodes whose core count is unknown (e.g., hardware info not yet discovered) get a default weight of 1.

### Duplicate Pointers for O(1) Selection

Rather than an O(n) weighted walk per request, the pool duplicates `*Connection` pointers in `ready[]` according to weight. A connection with `weight=3` gets 3 entries:

```
  ready[] layout for weights [1, 2, 3]:
    [A, B, B, C, C, C]
     ├─── active ─────┤ (activeCount = 6)
```

The existing `getNextActiveConnWithLock()` stays O(1) -- it increments an atomic counter and modulos by `activeCount` to select a slot. No changes to the hot path. The cost is paid during add/remove (once every ~5min during discovery), not every request.

### Add/Remove with Weights

Functions that manipulate `ready[]` handle multiple entries per connection:

- **`removeFromReadyWithLock(c)`** -- removes ALL entries for `c`, not just the first match. Uses an in-place filter that scans the entire slice, tracking how many were in the active partition to adjust `activeCount`.
- **`appendToReadyActiveWithLock(c)`** -- inserts `c.weight` copies into the active partition.
- **`appendToReadyStandbyWithLock(c)`** -- appends `c.weight` copies to the standby partition.
- **`enforceActiveCapWithLock()`** -- when counting non-warming connections, deduplicates using a `seen` set. When demoting overflow, removes all copies of each demoted connection.
- **`OnFailure(c)`** -- calls `removeFromReadyWithLock(c)` which handles all copies. No other change needed.

### activeCount Semantics

`activeCount` counts slots (entries), not unique connections. A 3-node cluster with weights [1,2,3] has `activeCount=6`. This is correct because the round-robin counter modulos by `activeCount` to hit all weighted slots.

### Hardware Info Discovery

Each connection tracks whether it needs hardware info via the `lcNeedsHardware` lifecycle bit:

- **Set on creation**: All new connections start with `lcNeedsHardware`
- **Set on failure**: When a connection dies, `lcNeedsHardware` is set -- the node may have been replaced with different hardware
- **Cleared**: When hardware info is successfully obtained, either from cluster-wide `/_nodes/http,os` during discovery or per-node `/_nodes/_local/http,os` during a health check

When a health check is due on a connection with `lcNeedsHardware` set, the client substitutes `/_nodes/_local/http,os` for the normal health check endpoint. This gets hardware info without an extra request -- one health check cycle is traded for hardware discovery.

### Capacity Model

The capacity model auto-derives all rate-limiting parameters from the server's core count (discovered via `/_nodes/http,os`):

```
  Primary input:  allocatedProcessors (auto-discovered, default: 8)

  Derived values:
    clientsPerServer        = coreCount              (1 active client per core)
    serverMaxNewConnsPerSec = coreCount * 4          (OS queue depth scaling)
    healthCheckRate         = coreCount * 0.10       (10% of core budget)

  These derived values feed into:
    - Resurrection timeout rate limiting
    - Active list cap calculation
    - Cluster health refresh interval
```

On each discovery cycle, the capacity model is recalculated using the minimum `allocatedProcessors` across all nodes (the smallest node is the bottleneck for rate limiting).

## Cluster Health Probe State

Each connection tracks whether `/_cluster/health?local=true` is available using a 2-bit atomic bitfield:

```
  Bit 0 (0x01): clusterHealthProbed     -- probe has been attempted
  Bit 1 (0x02): clusterHealthAvailable  -- probe succeeded

  State    Probed  Available  Meaning
  ──────   ──────  ─────────  ───────────────────────────────
  0x00     no      no         Pending -- never probed
  0x01     yes     no         Unavailable -- 401/403 response
  0x03     yes     yes        Available -- endpoint works
```

When available, the client uses cluster health data for two-phase readiness checks (shard initialization) and for red-cluster load shedding.
