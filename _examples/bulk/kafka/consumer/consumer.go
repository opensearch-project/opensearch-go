// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package consumer

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/opensearch-project/opensearch-go/opensearchutil"
)

type Consumer struct {
	BrokerURL string
	TopicName string

	Indexer opensearchutil.BulkIndexer
	reader  *kafka.Reader

	startTime     time.Time
	totalMessages int64
	totalErrors   int64
	totalBytes    int64
}

func (c *Consumer) Run(ctx context.Context) (err error) {
	if c.Indexer == nil {
		panic(fmt.Sprintf("%T.Indexer is nil", c))
	}
	c.startTime = time.Now()

	c.reader = kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{c.BrokerURL},
		GroupID: "go-elasticsearch-demo",
		Topic:   c.TopicName,
		// MinBytes: 1e+6, // 1MB
		// MaxBytes: 5e+6, // 5MB

		ReadLagInterval: 1 * time.Second,
	})

	for {
		msg, err := c.reader.ReadMessage(ctx)
		if err != nil {
			return fmt.Errorf("reader: %s", err)
		}
		// log.Printf("%v/%v/%v:%s\n", msg.Topic, msg.Partition, msg.Offset, string(msg.Value))

		if err := c.Indexer.Add(ctx,
			opensearchutil.BulkIndexerItem{
				Action: "create",
				Body:   bytes.NewReader(msg.Value),
				OnSuccess: func(ctx context.Context, item opensearchutil.BulkIndexerItem, res opensearchutil.BulkIndexerResponseItem) {
					// log.Printf("Indexed %s/%s", res.Index, res.DocumentID)
				},
				OnFailure: func(ctx context.Context, item opensearchutil.BulkIndexerItem, res opensearchutil.BulkIndexerResponseItem, err error) {
					if err != nil {
						log.Println(err)
					} else {
						if res.Error.Type != "" {
							// log.Printf("%s:%s", res.Error.Type, res.Error.Reason)
						} else {
							// log.Printf("%s/%s %s (%d)", res.Index, res.DocumentID, res.Result, res.Status)
						}

					}
				},
			}); err != nil {
			return fmt.Errorf("indexer: %s", err)
		}
	}
	c.reader.Close()
	c.Indexer.Close(ctx)

	return nil
}

type Stats struct {
	Duration      time.Duration
	TotalLag      int64
	TotalMessages int64
	TotalErrors   int64
	TotalBytes    int64
	Throughput    float64
}

func (c *Consumer) Stats() Stats {
	if c.reader == nil || c.Indexer == nil {
		return Stats{}
	}

	duration := time.Since(c.startTime)
	readerStats := c.reader.Stats()

	c.totalMessages += readerStats.Messages
	c.totalErrors += readerStats.Errors
	c.totalBytes += readerStats.Bytes

	rate := float64(c.totalMessages) / duration.Seconds()

	return Stats{
		Duration:      duration,
		TotalLag:      readerStats.Lag,
		TotalMessages: c.totalMessages,
		TotalErrors:   c.totalErrors,
		TotalBytes:    c.totalBytes,
		Throughput:    rate,
	}
}
