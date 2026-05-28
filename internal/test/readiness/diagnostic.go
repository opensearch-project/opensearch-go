// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package readiness

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Diagnostic returns a multi-line human-readable dump of the aggregator's
// current state. Used as the trailer on every fatal Wait outcome:
// timeout, terminal error, ctx cancel, flapping abort.
//
// Format goal: a reader scanning CI logs should be able to identify the
// failing layer and the responsible node within seconds. Per-node lines
// list current state, missing bits relative to target, and time spent in
// the current state. Recent transitions are summarized so regressions
// (the smoking gun) stand out.
func (c *Cluster) Diagnostic() string {
	c.mu.Lock()
	polls := c.mu.polls
	lastErr := c.mu.lastErr
	lastResp := append([]byte(nil), c.mu.lastResp...)
	nodes := make([]*NodeFSM, 0, len(c.mu.nodes))
	for _, n := range c.mu.nodes {
		nodes = append(nodes, n)
	}
	c.mu.Unlock()

	sort.Slice(nodes, func(i, j int) bool {
		// Stable, readable order: by name then id.
		if nodes[i].Name != nodes[j].Name {
			return nodes[i].Name < nodes[j].Name
		}
		return nodes[i].NodeID < nodes[j].NodeID
	})

	var b strings.Builder
	elapsed := time.Since(c.Started).Round(time.Millisecond)

	ready := 0
	for _, n := range nodes {
		if n.Has(c.Target) {
			ready++
		}
	}

	fmt.Fprintf(&b, "readiness: %d/%d nodes satisfy %v (need %d), %d polls over %s\n",
		ready, len(nodes), c.Target, c.MinNodes, polls, elapsed)
	if lastErr != nil {
		fmt.Fprintf(&b, "  last transient error: %v\n", lastErr)
	} else {
		b.WriteString("  last transient error: <nil>\n")
	}
	if last := c.LastPolledAt(); !last.IsZero() {
		fmt.Fprintf(&b, "  last poll: %s\n", last.Format(time.RFC3339Nano))
	}

	if len(nodes) == 0 {
		fmt.Fprintf(&b, "\nNo nodes observed yet. Cluster expected %d.\n", c.Expected)
	} else {
		b.WriteString("\nPer-node state:\n")
		now := time.Now()
		for _, n := range nodes {
			renderNode(&b, n, c.Target, now)
		}
	}

	if len(lastResp) > 0 {
		fmt.Fprintf(&b, "\nLast response (%d bytes):\n  %s\n",
			len(lastResp), strings.ReplaceAll(string(lastResp), "\n", "\n  "))
	}

	return b.String()
}

func renderNode(b *strings.Builder, n *NodeFSM, target State, now time.Time) {
	snap := n.Snapshot()
	state := snap.State
	missing := state.Missing(target)
	timeIn := time.Duration(0)
	if !snap.EnteredAt.IsZero() {
		timeIn = now.Sub(snap.EnteredAt).Round(time.Millisecond)
	}

	name := n.Name
	if name == "" {
		name = "<unnamed>"
	}
	label := name
	if id := n.NodeID; id != "" && id != name && !strings.HasPrefix(name, id) {
		label = fmt.Sprintf("%s (%s)", name, id)
	}

	fmt.Fprintf(b, "  %-30s %-50s in %s", label, state.String(), timeIn)

	if snap.Regressions > 0 {
		fmt.Fprintf(b, "  regressed=%d", snap.Regressions)
	}

	if missing != 0 {
		fmt.Fprintf(b, "  missing=%v", missing)
	}

	b.WriteString("\n")

	// Last few transitions surface regressions and stalls. Limit to keep
	// the dump readable when N tests print diagnostics back-to-back.
	hist := snap.History
	if len(hist) > 0 {
		const maxLines = 5
		start := 0
		if len(hist) > maxLines {
			start = len(hist) - maxLines
		}
		for _, tr := range hist[start:] {
			marker := "    "
			if tr.Regression {
				marker = "  ! "
			}
			fmt.Fprintf(b, "%s%s: %v -> %v",
				marker, tr.At.Format("15:04:05.000"), tr.From, tr.To)
			if tr.Note != "" {
				fmt.Fprintf(b, " (%s)", tr.Note)
			}
			b.WriteString("\n")
		}
	}
}
