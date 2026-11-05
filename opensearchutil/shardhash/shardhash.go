// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package shardhash

// Hash computes the same hash as OpenSearch's
// Murmur3HashFunction.hash(String) in OperationRouting.java.
//
// The algorithm:
//  1. Encode the string as UTF-16 little-endian bytes (each char -> 2 bytes,
//     low byte first). This matches Java's String-to-byte conversion in
//     Murmur3HashFunction.hash(String).
//  2. Hash with MurmurHash3 x86 32-bit (seed = 0), the same variant used
//     by Lucene's StringHelper.murmurhash3_x86_32.
//
// Reference:
//
//	OpenSearch server/src/main/java/org/opensearch/cluster/routing/Murmur3HashFunction.java
//	Lucene's StringHelper.murmurhash3_x86_32 (seed=0)
func Hash(routing string) int32 {
	// Step 1: Encode as UTF-16 LE.
	// Java's Murmur3HashFunction.hash(String) iterates charAt(i), which
	// returns UTF-16 code units. Each code unit is written as 2 bytes
	// in little-endian order. Codepoints above U+FFFF (outside BMP) use
	// a surrogate pair (2 code units = 4 bytes).
	//
	// len(routing)*2 is a safe upper bound: the UTF-16 byte count never
	// exceeds 2x the UTF-8 byte count for any valid string.
	n := len(routing) * 2

	// Stack-allocate for typical routing values (up to 64 code units = 128 bytes).
	var stack [128]byte
	var buf []byte
	if n <= len(stack) {
		buf = stack[:n]
	} else {
		buf = make([]byte, n)
	}

	// Encode each rune as one or two UTF-16 LE code units.
	j := 0
	for _, r := range routing {
		if r <= 0xFFFF {
			// BMP character: single code unit.
			buf[j] = byte(r)        //nolint:gosec // intentional truncation for UTF-16 LE encoding
			buf[j+1] = byte(r >> 8) //nolint:gosec // intentional truncation for UTF-16 LE encoding
			j += 2
		} else {
			// Supplementary character: surrogate pair.
			r -= 0x10000
			hi := 0xD800 + (r>>10)&0x3FF // high surrogate
			lo := 0xDC00 + r&0x3FF       // low surrogate
			buf[j] = byte(hi)            //nolint:gosec // intentional truncation for UTF-16 LE encoding
			buf[j+1] = byte(hi >> 8)     // high byte of high surrogate
			buf[j+2] = byte(lo)          //nolint:gosec // intentional truncation for UTF-16 LE encoding
			buf[j+3] = byte(lo >> 8)     // high byte of low surrogate
			j += 4
		}
	}

	return murmurhash3X86_32(buf[:j], 0)
}

// ForRouting computes the shard number for a routing value, matching
// OpenSearch's OperationRouting.calculateScaledShardId:
//
//	hash  = Murmur3HashFunction.hash(effectiveRouting)
//	shard = Math.floorMod(hash, routingNumShards) / routingFactor
//
// routingNumShards is an index metadata value (typically much larger than
// numberOfShards) that allows future index splitting. For a newly created
// 5-shard index, routingNumShards is 640 and routingFactor is 128.
//
// Reference:
//
//	OperationRouting.java:calculateScaledShardId
//	MetadataCreateIndexService.java:calculateNumRoutingShards
func ForRouting(routing string, routingNumShards, numPrimaryShards int) int {
	shard, _ := ForDocument(routing, "", routingNumShards, numPrimaryShards, 0)

	return shard
}

// ForDocument computes the shard number for a document the way
// OpenSearch's OperationRouting.generateShardId does:
//
//	effectiveRouting = routing == "" ? id : routing
//	partitionOffset  = routingPartitionSize > 1 ? floorMod(hash(id), routingPartitionSize) : 0
//	shard = floorMod(hash(effectiveRouting) + partitionOffset, routingNumShards) / routingFactor
//
// The sum is an int32 addition that wraps on overflow, as in Java. A
// routingPartitionSize of 0 or 1 means the index is not partitioned, and the
// result equals [ForRouting] of the effective routing.
//
// The partition offset hashes the ID, so on a partitioned index the shard is not
// known without one, as for a document whose ID the server generates: the second
// result is false when routingPartitionSize is above 1 and id is empty.
func ForDocument(routing, id string, routingNumShards, numPrimaryShards, routingPartitionSize int) (int, bool) {
	if routingPartitionSize > 1 && id == "" {
		return 0, false
	}

	if routing == "" {
		routing = id
	}

	offset := 0
	if routingPartitionSize > 1 {
		offset = floorMod(Hash(id), routingPartitionSize)
	}

	return ForRoutingOffset(routing, offset, routingNumShards, numPrimaryShards), true
}

// ForRoutingOffset computes the shard for a routing value at a partition
// offset: floorMod(hash(routing) + offset, routingNumShards) / routingFactor,
// with the sum wrapping as in [ForDocument], which owns the partition rule.
//
// On a partitioned index the server's search target set for a routing value is
// this function over offsets 0 through routingPartitionSize-1. Offset 0 equals
// [ForRouting].
func ForRoutingOffset(routing string, offset, routingNumShards, numPrimaryShards int) int {
	hash := Hash(routing) + int32(offset) //nolint:gosec // offset < routingPartitionSize, an int32 index setting

	return floorMod(hash, routingNumShards) / (routingNumShards / numPrimaryShards)
}

// floorMod computes Java's Math.floorMod(a, b): the floor modulus that
// always returns a non-negative result for positive b.
//
// Go's % operator truncates toward zero, so (-1) % 4 == -1.
// Java's Math.floorMod floors toward negative infinity: floorMod(-1, 4) == 3.
func floorMod(a int32, b int) int {
	m := int(a) % b
	if m < 0 {
		m += b
	}
	return m
}
