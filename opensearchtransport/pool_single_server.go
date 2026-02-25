// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import "net/url"

// singleServerPool is a trivial connection pool for single-node clusters.
// All operations are no-ops except Next(), which returns the single connection.
type singleServerPool struct {
	connection *Connection

	metrics *metrics
}

// Compile-time check that singleServerPool implements ConnectionPool.
var _ ConnectionPool = (*singleServerPool)(nil)

// Next returns the single connection.
func (cp *singleServerPool) Next() (*Connection, error) {
	return cp.connection, nil
}

// OnSuccess is a no-op for single connection pool.
func (cp *singleServerPool) OnSuccess(*Connection) {}

// OnFailure is a no-op for single connection pool.
func (cp *singleServerPool) OnFailure(*Connection) error { return nil }

// URLs returns the list of URLs of available connections.
func (cp *singleServerPool) URLs() []*url.URL { return []*url.URL{cp.connection.URL} }

func (cp *singleServerPool) connections() []*Connection { return []*Connection{cp.connection} }
