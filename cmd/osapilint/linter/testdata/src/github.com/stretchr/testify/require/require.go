// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package require is a minimal stand-in for github.com/stretchr/testify/require
// used only by analysistest fixtures. The analyzer keys off the import path and
// function names, so the bodies are irrelevant - the signatures just need to
// take `any` so the fixture code type-checks the way real testify does.
package require

type TestingT interface {
	Errorf(format string, args ...interface{})
}

func Equal(t TestingT, expected, actual interface{}, msgAndArgs ...interface{})    {}
func NotEqual(t TestingT, expected, actual interface{}, msgAndArgs ...interface{}) {}
func Greater(t TestingT, e1, e2 interface{}, msgAndArgs ...interface{})            {}
func GreaterOrEqual(t TestingT, e1, e2 interface{}, msgAndArgs ...interface{})     {}
func Less(t TestingT, e1, e2 interface{}, msgAndArgs ...interface{})               {}
func LessOrEqual(t TestingT, e1, e2 interface{}, msgAndArgs ...interface{})        {}
func NoError(t TestingT, err error, msgAndArgs ...interface{})                     {}
func True(t TestingT, value bool, msgAndArgs ...interface{})                       {}
