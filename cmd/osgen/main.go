// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// osgen is the unified code generator for the OpenSearch Go client.
// It reads the OpenSearch API specification and generates typed path builder
// structs and API consumer files.
//
// Usage:
//
//	osgen paths -spec <openapi-spec.yaml> [-groups g1,g2] [-pkg path] [-o out.go] [-test-out out_test.go]
//	osgen api   -spec <openapi-spec.yaml> [-out dir] [-plugins-out dir]
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "osgen: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		usage()
		return nil
	}

	switch os.Args[1] {
	case "paths":
		os.Args = append(os.Args[:1], os.Args[2:]...)
		return runPaths()
	case "api":
		os.Args = append(os.Args[:1], os.Args[2:]...)
		return runAPI()
	case "-h", "-help", "--help", "help":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: osgen <command> [flags]

Commands:
  paths   Generate typed path builder structs from the OpenAPI spec
  api     Generate API consumer files (Req, Params, Resp) from the OpenAPI spec

Run "osgen <command> -help" for command-specific flags.`)
}
