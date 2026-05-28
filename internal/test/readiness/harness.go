// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package readiness

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strconv"
	"time"

	"github.com/stretchr/testify/require"
)

// DefaultBudgets is the per-layer time budget consumed when computing
// a Wait deadline. The total deadline is the sum of budgets for every
// layer up to and including the target layer.
//
// Tuned for CI pessimism: re-running a flaky CI job is more expensive
// than letting a slow test take a couple extra minutes, so the budgets
// are 2-4x what a healthy cluster needs. LayerClusterJoin is the long
// pole because cold JVM startup + plugin init + cluster formation can
// chew through 90+ seconds on a constrained GHA runner.
var DefaultBudgets = map[State]time.Duration{
	LayerTCP:         30 * time.Second,
	LayerHTTP:        60 * time.Second,
	LayerClusterJoin: 180 * time.Second,
	LayerStatsReady:  120 * time.Second,
	LayerConnReady:   45 * time.Second,
}

// DefaultClientStateBudget is the time allowed for client-state bits
// to settle once a node reaches LayerConnReady.
const DefaultClientStateBudget = 45 * time.Second

// DefaultPollInterval is the harness loop cadence.
const DefaultPollInterval = 1 * time.Second

// MaxRegressions is the per-node ceiling on layer regressions before the
// harness aborts with a "flapping" diagnostic.
const MaxRegressions = 3

// config holds the resolved Wait configuration. Built via Options.
type config struct {
	expectedNodes     int
	minNodes          int
	layerChecks       []LayerCheck
	clientStateChecks []ClientStateCheck
	fsmChecks         []FSMCheck
	readyFuncs        []ReadyFunc
	budgets           map[State]time.Duration
	clientStateBudget time.Duration
	pollInterval      time.Duration
	maxRegressions    uint32
}

// FSMCheck is a closure-based observer invoked once per harness tick.
// It can advance any node in cluster (via cluster.Node().Advance) and
// update client-state bits (via cluster.Node().UpdateClientState).
//
// Returning a non-nil error aborts the harness with the diagnostic;
// transient errors should be recorded via cluster.RecordError and the
// function should return nil.
//
// FSMChecks let callers wire in domain-specific signal sources (e.g. a
// transport-pool observation that requires types we don't want to import
// into this package) without paying for an adapter package.
type FSMCheck func(ctx context.Context, cluster *Cluster) error

// ReadyFunc is a boolean gate evaluated once per harness tick alongside
// cluster.Satisfied(). Use it for domain-specific conditions that don't
// fit the FSM layer/state model (e.g. "this HTTP operation succeeds").
type ReadyFunc func() bool

// Option configures a Wait invocation.
type Option func(*config)

// WithExpectedNodes overrides the expected node count. By default the
// count comes from OPENSEARCH_NODE_COUNT (or 1 if unset).
func WithExpectedNodes(n int) Option {
	return func(c *config) { c.expectedNodes = n }
}

// WithMinNodes sets the minimum number of nodes that must satisfy the
// target. If unset, defaults to the expected node count (every node
// must satisfy the target).
func WithMinNodes(n int) Option {
	return func(c *config) { c.minNodes = n }
}

// WithLayerCheck registers a check that promotes nodes to the layer
// returned by check.Layer(). Multiple checks for the same layer are
// run in registration order.
func WithLayerCheck(check LayerCheck) Option {
	return func(c *config) { c.layerChecks = append(c.layerChecks, check) }
}

// WithClientStateCheck registers a check that mutates upper-half bits
// once nodes reach LayerConnReady.
func WithClientStateCheck(check ClientStateCheck) Option {
	return func(c *config) { c.clientStateChecks = append(c.clientStateChecks, check) }
}

// WithFSMCheck registers a closure-based observer invoked once per
// harness tick. Use it to wire in signals whose types live in packages
// that internal/test/readiness must not import (notably the transport
// package, which would create an import cycle through testutil).
func WithFSMCheck(fn FSMCheck) Option {
	return func(c *config) { c.fsmChecks = append(c.fsmChecks, fn) }
}

// WithReadyFunc registers a boolean predicate that must return true
// alongside cluster.Satisfied() for Wait to return successfully. Use it
// for domain-specific gates (e.g. "this HTTP operation succeeds") that
// don't fit the FSM layer/state model.
func WithReadyFunc(fn ReadyFunc) Option {
	return func(c *config) { c.readyFuncs = append(c.readyFuncs, fn) }
}

// WithLayerBudget overrides the time budget for a specific layer. The
// total Wait deadline is the sum of budgets for every layer up to and
// including the target.
func WithLayerBudget(layer State, d time.Duration) Option {
	return func(c *config) { c.budgets[layer] = d }
}

// WithClientStateBudget overrides the upper-bit settling budget added to
// the deadline when the target requires any client-state bits.
func WithClientStateBudget(d time.Duration) Option {
	return func(c *config) { c.clientStateBudget = d }
}

// WithPollInterval overrides the harness loop cadence.
func WithPollInterval(d time.Duration) Option {
	return func(c *config) { c.pollInterval = d }
}

// WithMaxRegressions overrides the per-node regression ceiling.
func WithMaxRegressions(n uint32) Option {
	return func(c *config) { c.maxRegressions = n }
}

// TestingT is the subset of *testing.T the harness needs. It matches
// require.TestingT (Errorf + FailNow). Production callers pass
// *testing.T; tests of the harness itself pass a recording mock.
type TestingT = require.TestingT

// Wait blocks until at least the configured number of nodes satisfy
// target, or the per-layer deadlines expire. On failure it invokes
// require.Failf (or require.NoErrorf) with a structured diagnostic dump
// and returns; for real *testing.T this terminates the goroutine via
// runtime.Goexit, so the trailing returns are belt-and-suspenders for
// mock TestingT implementations that allow execution to continue.
//
// The ctx parameter is propagated to every check Probe and is also used
// to bound the wait when its deadline precedes the per-layer-budget
// deadline. Pass t.Context() at the call site unless the caller wants a
// derived ctx with custom timeout or values.
//
// The caller wires in observers via WithLayerCheck, WithClientStateCheck,
// WithFSMCheck, and WithReadyFunc. Wait itself contains no protocol logic;
// the cluster-lens and transport-lens closures live in the consumer
// packages (v5preview/opensearchapi/testutil and opensearchtransport tests respectively)
// to keep readiness free of any client-package imports.
func Wait(t TestingT, ctx context.Context, target State, opts ...Option) {
	if h, ok := t.(interface{ Helper() }); ok {
		h.Helper()
	}

	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.minNodes <= 0 || cfg.minNodes > cfg.expectedNodes {
		cfg.minNodes = cfg.expectedNodes
	}
	if target != 0 && len(cfg.layerChecks) == 0 && len(cfg.fsmChecks) == 0 {
		require.Failf(t, "readiness misconfigured",
			"target %v requires layer or state progression but no LayerCheck or FSMCheck is registered", target)
		return
	}

	cluster := NewCluster(cfg.expectedNodes, cfg.minNodes, target)

	deadline := cluster.Started.Add(totalBudget(cfg, target))
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	tick := time.NewTicker(cfg.pollInterval)
	defer tick.Stop()

	for {
		cluster.IncPolls()
		cluster.SetLastPolledAt(time.Now())

		// Run every layer check; each is responsible for advancing only
		// nodes that have reached its prerequisite layer. A non-nil
		// return is terminal: auth failure, scheme mismatch, etc.
		for _, lc := range cfg.layerChecks {
			if lc.Layer()&target == 0 {
				continue // not required for this target
			}
			err := lc.Probe(ctx, cluster)
			require.NoErrorf(t, err, "readiness aborted: terminal error from %T\n%s",
				lc, cluster.Diagnostic())
			if err != nil {
				return // explicit guard for mock TestingT implementations
			}
		}

		// Run client-state checks only if the target requires upper bits.
		if target&stateMask != 0 {
			for _, cc := range cfg.clientStateChecks {
				if cc.Bits()&target == 0 {
					continue
				}
				err := cc.Probe(ctx, cluster)
				require.NoErrorf(t, err, "readiness aborted: terminal error from %T\n%s",
					cc, cluster.Diagnostic())
				if err != nil {
					return
				}
			}
		}

		// Run any FSM-mutating closures. These can advance layers and
		// update client-state bits regardless of target shape.
		for _, fn := range cfg.fsmChecks {
			err := fn(ctx, cluster)
			require.NoErrorf(t, err, "readiness aborted: terminal error from FSMCheck\n%s",
				cluster.Diagnostic())
			if err != nil {
				return
			}
		}

		// Flapping check: any node exceeding the regression ceiling.
		for _, n := range cluster.Nodes() {
			if n.Regressions() > cfg.maxRegressions {
				require.Failf(t, "readiness flapping",
					"node %s regressed %d times (max %d)\n%s",
					n.NodeID, n.Regressions(), cfg.maxRegressions, cluster.Diagnostic())
				return
			}
		}

		if cluster.Satisfied() && allReady(cfg.readyFuncs) {
			return
		}

		if time.Now().After(deadline) {
			require.Failf(t, "readiness timeout",
				"timed out after %s\n%s",
				time.Since(cluster.Started).Round(time.Millisecond),
				cluster.Diagnostic())
			return
		}

		select {
		case <-ctx.Done():
			require.Failf(t, "readiness ctx cancelled",
				"%v\n%s", ctx.Err(), cluster.Diagnostic())
			return
		case <-tick.C:
		}
	}
}

// ErrTerminal wraps an error to signal that the harness should abort
// immediately rather than treating the error as transient.
var ErrTerminal = errors.New("readiness: terminal error")

// AsTerminal wraps err so that returning it from a check causes Wait
// to abort with the diagnostic. Both ErrTerminal and the underlying
// err are preserved in the wrapper for errors.Is checks.
func AsTerminal(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrTerminal, err)
}

func defaultConfig() config {
	budgets := make(map[State]time.Duration, len(DefaultBudgets))
	maps.Copy(budgets, DefaultBudgets)
	return config{
		expectedNodes:     expectedNodesFromEnv(),
		budgets:           budgets,
		clientStateBudget: DefaultClientStateBudget,
		pollInterval:      DefaultPollInterval,
		maxRegressions:    MaxRegressions,
	}
}

func expectedNodesFromEnv() int {
	v := os.Getenv("OPENSEARCH_NODE_COUNT")
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// totalBudget sums the per-layer budgets for every layer bit set in
// target, plus the client-state settling budget when upper bits are
// required.
func totalBudget(cfg config, target State) time.Duration {
	var total time.Duration
	for layer, d := range cfg.budgets {
		// A layer's budget contributes when its bit is part of target.
		// Because layers are cumulative, every layer up to and including
		// the highest target layer is counted.
		if layer&target == layer {
			total += d
		}
	}
	if target&stateMask != 0 {
		total += cfg.clientStateBudget
	}
	return total
}

// allReady returns true if every ReadyFunc returns true. Empty slice -> true.
func allReady(fns []ReadyFunc) bool {
	for _, fn := range fns {
		if !fn() {
			return false
		}
	}
	return true
}
