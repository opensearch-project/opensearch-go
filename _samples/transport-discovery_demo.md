# Discovery Demo

This demo demonstrates the complete node discovery flow with request routing.

## What It Shows

1. **Phase 1: Initial Request** - Seed URLs are used as `coordinating_only` nodes
2. **Phase 2: Discovery** - Client discovers actual cluster nodes with roles
3. **Phase 3: Request Routing** - Requests routed based on operation type and node roles

## Requirements

- OpenSearch cluster running on `localhost:9200` and `localhost:9201`
- The demo will discover a third node at `localhost:9202` if available

## Running

```bash
go run transport-discovery_demo.go
```

## Key Features Demonstrated

### Metrics Tracking

The demo shows the new client-side metrics:

- **Live/Dead Connections**: Current connection pool state
- **Connections Promoted/Demoted**: Lifecycle tracking (resurrections and failures)
- **Zombie Connections**: Dead connections forcibly retried when no live connections available
- **Health Checks**: Baseline and cluster health check counts
- **Overloaded Servers**: Number of servers currently marked as overloaded

### Request Routing

After discovery completes:

- **Bulk operations** -> Route to ingest nodes
- **Search operations** -> Route to data/search nodes
- **General operations** -> Round-robin across all nodes

### Discovery Flow

Watch the debug logs (when not in CI) to see:

1. Seed URLs added to `coordinator_only` policy
2. Initial request uses seed URL
3. Discovery runs automatically
4. Seed URLs removed from `coordinator_only` after nodes with actual roles discovered
5. Router takes over with role-based routing

## Configuration

The demo uses fast timeouts for quick demonstration:

- Discovery interval: 5 seconds
- Health check timeout: 2 seconds
- Resurrection timeouts: 1-10 seconds

Production should use the default values (much longer intervals).
