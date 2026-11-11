// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport_test

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// hotKeyObserver counts ?routing= values per index in a bounded space-saving
// top-K (Metwally et al.), so memory stays at K entries per index however many
// distinct routing values the traffic carries.
type hotKeyObserver struct {
	opensearchtransport.BaseConnectionObserver

	k int

	// One mutex guards every index; sample every Nth event or shard by index
	// if OnRoute contention shows up.
	mu struct {
		sync.Mutex
		indices map[string]*indexCounts
	}
}

// indexCounts is the top-K state for one index.
type indexCounts struct {
	values map[string]int // routing value -> estimated count
	total  int            // routed requests with a routing value
}

func newHotKeyObserver(k int) *hotKeyObserver {
	o := &hotKeyObserver{k: k}
	o.mu.indices = map[string]*indexCounts{}
	return o
}

// OnRoute implements opensearchtransport.ConnectionObserver.
func (o *hotKeyObserver) OnRoute(event opensearchtransport.RouteEvent) {
	if event.RoutingValue == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	ic := o.mu.indices[event.IndexName]
	if ic == nil {
		ic = &indexCounts{values: make(map[string]int, o.k)}
		o.mu.indices[event.IndexName] = ic
	}
	ic.total++

	if _, ok := ic.values[event.RoutingValue]; ok || len(ic.values) < o.k {
		ic.values[event.RoutingValue]++
		return
	}
	// Full: evict the minimum-count entry; the newcomer inherits min+1.
	minKey, minCount := "", -1
	for key, n := range ic.values {
		if minCount < 0 || n < minCount || (n == minCount && key < minKey) {
			minKey, minCount = key, n
		}
	}
	delete(ic.values, minKey)
	ic.values[event.RoutingValue] = minCount + 1
}

// Hot returns the most frequent routing value for index when the index has at
// least minRequests routed requests and that value's share is at least share.
// The share is a space-saving estimate, which can overcount a value by up to
// the count of the entry it evicted, so it is an upper bound.
func (o *hotKeyObserver) Hot(index string, minRequests int, share float64) (string, float64, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	ic := o.mu.indices[index]
	if ic == nil || ic.total < minRequests {
		return "", 0, false
	}
	var topKey string
	top := -1
	for key, n := range ic.values {
		if n > top {
			topKey, top = key, n
		}
	}
	if top < 0 {
		return "", 0, false
	}
	topShare := float64(top) / float64(ic.total)
	return topKey, topShare, topShare >= share
}

// ExampleConnectionObserver_hotRoutingKey finds a hot ?routing= value, which
// sends its load to the same index.routing_partition_size shards and which
// client-side spread cannot move. An OnRoute observer finds the value by
// counting RoutingValue per index in a bounded top-K.
func ExampleConnectionObserver_hotRoutingKey() {
	obs := newHotKeyObserver(32)

	for i := range 1000 {
		ev := opensearchtransport.RouteEvent{IndexName: "orders"}
		switch {
		case i%10 < 6:
			ev.RoutingValue = "tenant-a"
		case i%10 < 9:
			ev.RoutingValue = "tenant-" + strconv.Itoa(i) // many distinct keys, forces eviction
		}
		// i%10 == 9: no routing value, ignored.
		obs.OnRoute(ev)
	}

	if key, share, ok := obs.Hot("orders", 500, 0.5); ok {
		fmt.Printf("hot routing value %q: %.0f%% of routed requests\n", key, share*100)
	}
	// Output: hot routing value "tenant-a": 67% of routed requests
}
