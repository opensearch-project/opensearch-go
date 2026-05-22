// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package readiness_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/internal/test/readiness"
)

// fakeLayerCheck advances every observed node to its layer after a fixed
// number of polls. Used to exercise the harness loop without a real cluster.
type fakeLayerCheck struct {
	layer   readiness.State
	delay   int // polls before advancing
	polls   atomic.Int32
	nodeIDs []string
}

func (f *fakeLayerCheck) Layer() readiness.State { return f.layer }

func (f *fakeLayerCheck) Probe(_ context.Context, c *readiness.Cluster) error {
	n := f.polls.Add(1)
	for _, id := range f.nodeIDs {
		node := c.Node(id, id, "127.0.0.1")
		if int(n) >= f.delay && node.State()&f.layer != f.layer {
			node.Advance(f.layer, "fake advance")
		}
	}
	return nil
}

// failingLayerCheck always returns a terminal error.
type failingLayerCheck struct {
	layer readiness.State
	err   error
}

func (f *failingLayerCheck) Layer() readiness.State                          { return f.layer }
func (f *failingLayerCheck) Probe(context.Context, *readiness.Cluster) error { return f.err }

// partialAdvance advances only the first advanceCount nodes to its layer.
type partialAdvance struct {
	layer        readiness.State
	advanceCount int
	nodeIDs      []string
}

func (p *partialAdvance) Layer() readiness.State { return p.layer }

func (p *partialAdvance) Probe(_ context.Context, c *readiness.Cluster) error {
	for i, id := range p.nodeIDs {
		node := c.Node(id, id, "127.0.0.1")
		if i < p.advanceCount {
			node.Advance(p.layer, "partial")
		}
	}
	return nil
}

// recordingT implements require.TestingT. Failures are recorded
// instead of terminating the goroutine so the harness loop's
// belt-and-suspenders return-after-Failf actually exits.
type recordingT struct {
	mu       sync.Mutex
	failed   atomic.Bool
	messages []string
}

func (r *recordingT) Helper() {}

func (r *recordingT) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

func (r *recordingT) FailNow() { r.failed.Store(true) }

func (r *recordingT) lastMessage() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.messages) == 0 {
		return ""
	}
	return r.messages[len(r.messages)-1]
}

func TestWait_SatisfiesAfterAdvance(t *testing.T) {
	t.Parallel()

	check := &fakeLayerCheck{
		layer:   readiness.LayerStatsReady,
		delay:   2,
		nodeIDs: []string{"node-a", "node-b", "node-c"},
	}

	readiness.Wait(t, t.Context(), readiness.LayerStatsReady,
		readiness.WithExpectedNodes(3),
		readiness.WithLayerCheck(check),
		readiness.WithPollInterval(10*time.Millisecond),
		readiness.WithLayerBudget(readiness.LayerTCP, 1*time.Second),
		readiness.WithLayerBudget(readiness.LayerHTTP, 1*time.Second),
		readiness.WithLayerBudget(readiness.LayerClusterJoin, 1*time.Second),
		readiness.WithLayerBudget(readiness.LayerStatsReady, 1*time.Second),
	)
}

func TestWait_TimesOutWithDiagnostic(t *testing.T) {
	t.Parallel()

	mt := &recordingT{}
	check := &fakeLayerCheck{
		layer:   readiness.LayerStatsReady,
		delay:   1 << 30, // never advance within this run
		nodeIDs: []string{"node-a", "node-b"},
	}

	readiness.Wait(mt, t.Context(), readiness.LayerStatsReady,
		readiness.WithExpectedNodes(2),
		readiness.WithLayerCheck(check),
		readiness.WithPollInterval(5*time.Millisecond),
		readiness.WithLayerBudget(readiness.LayerTCP, 10*time.Millisecond),
		readiness.WithLayerBudget(readiness.LayerHTTP, 10*time.Millisecond),
		readiness.WithLayerBudget(readiness.LayerClusterJoin, 10*time.Millisecond),
		readiness.WithLayerBudget(readiness.LayerStatsReady, 10*time.Millisecond),
	)

	require.True(t, mt.failed.Load(), "expected FailNow to fire")
	msg := mt.lastMessage()
	require.Contains(t, msg, "readiness timeout")
	require.Contains(t, msg, "Per-node state:")
	require.Contains(t, msg, "node-a")
	require.Contains(t, msg, "node-b")
}

func TestWait_TerminalErrorAborts(t *testing.T) {
	t.Parallel()

	mt := &recordingT{}
	terminal := errors.New("auth boom")
	check := &failingLayerCheck{
		layer: readiness.LayerStatsReady,
		err:   terminal,
	}

	readiness.Wait(mt, t.Context(), readiness.LayerStatsReady,
		readiness.WithExpectedNodes(1),
		readiness.WithLayerCheck(check),
		readiness.WithPollInterval(5*time.Millisecond),
	)

	require.True(t, mt.failed.Load())
	msg := mt.lastMessage()
	require.Contains(t, msg, "auth boom")
	require.Contains(t, msg, "readiness aborted")
}

func TestWait_HonorsMinNodes(t *testing.T) {
	t.Parallel()

	// Three observable nodes but only two ever advance. MinNodes=2 should pass.
	check := &partialAdvance{
		layer:        readiness.LayerStatsReady,
		advanceCount: 2,
		nodeIDs:      []string{"a", "b", "c"},
	}

	readiness.Wait(t, t.Context(), readiness.LayerStatsReady,
		readiness.WithExpectedNodes(3),
		readiness.WithMinNodes(2),
		readiness.WithLayerCheck(check),
		readiness.WithPollInterval(5*time.Millisecond),
		readiness.WithLayerBudget(readiness.LayerTCP, 1*time.Second),
		readiness.WithLayerBudget(readiness.LayerHTTP, 1*time.Second),
		readiness.WithLayerBudget(readiness.LayerClusterJoin, 1*time.Second),
		readiness.WithLayerBudget(readiness.LayerStatsReady, 1*time.Second),
	)
}

func TestWait_FlappingNodeAborts(t *testing.T) {
	t.Parallel()

	mt := &recordingT{}
	check := &flappingCheck{nodeID: "flaky"}

	readiness.Wait(mt, t.Context(), readiness.LayerStatsReady,
		readiness.WithExpectedNodes(1),
		readiness.WithLayerCheck(check),
		readiness.WithPollInterval(2*time.Millisecond),
		readiness.WithMaxRegressions(2),
		readiness.WithLayerBudget(readiness.LayerTCP, 1*time.Second),
		readiness.WithLayerBudget(readiness.LayerHTTP, 1*time.Second),
		readiness.WithLayerBudget(readiness.LayerClusterJoin, 1*time.Second),
		readiness.WithLayerBudget(readiness.LayerStatsReady, 1*time.Second),
	)

	require.True(t, mt.failed.Load())
	require.Contains(t, mt.lastMessage(), "readiness flapping")
}

// flappingCheck advances a node to LayerStatsReady and then immediately
// regresses it to LayerHTTP within the same probe so the node never
// satisfies the target. Each probe call generates one regression.
type flappingCheck struct {
	nodeID string
}

func (f *flappingCheck) Layer() readiness.State { return readiness.LayerStatsReady }

func (f *flappingCheck) Probe(_ context.Context, c *readiness.Cluster) error {
	node := c.Node(f.nodeID, f.nodeID, "127.0.0.1")
	node.Advance(readiness.LayerStatsReady, "up")
	node.Advance(readiness.LayerHTTP, "down") // regression
	return nil
}
