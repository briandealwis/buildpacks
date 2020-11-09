// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gcpbuildpack

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestParseTraceContext(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		found    bool
		traceId  string
		parentId string
	}{
		{name: "no sampling", value: "00-0af7651916cd43dd8448eb211c80319c-00f067aa0ba902b7-00", found: true, traceId: "0af7651916cd43dd8448eb211c80319c", parentId: "00f067aa0ba902b7"},
		{name: "sampled flag", value: "00-0af7651916cd43dd8448eb211c80319c-00f067aa0ba902b7-01", found: true, traceId: "0af7651916cd43dd8448eb211c80319c", parentId: "00f067aa0ba902b7"},
		{name: "unhandled version", value: "01-0af7651916cd43dd8448eb211c80319c-00f067aa0ba902b7-01", found: false},
		{name: "invalid flags", value: "01-0af7651916cd43dd8448eb211c80319c-00f067aa0ba902b7-02", found: false},
		{name: "invalid flags", value: "01-0af7651916cd43dd8448eb211c80319c-00f067aa0ba902b7-20", found: false},
		{name: "trace id too small", value: "01-07651916cd43dd8448eb211c80319c-00f067aa0ba902b7-00", found: false},
		{name: "trace id too big", value: "01-0ffad7651916cd43dd8448eb211c80319c-00f067aa0ba902b7-00", found: false},
		{name: "parent id too small", value: "00-0af7651916cd43dd8448eb211c80319c-f067aa0ba902b7-01", found: false},
		{name: "parent id too small", value: "00-0af7651916cd43dd8448eb211c80319c-0000f067aa0ba902b7-01", found: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			os.Setenv("TRACEPARENT", test.value)
			traceId, parentId, found := parseTraceContext()
			if test.found {
				if !found {
					t.Errorf("Unable to parse trace context")
				}
				if test.traceId != traceId {
					t.Errorf("traceId not as expected\nwanted: %s\n  got: %s", test.traceId, traceId)
				}
				if test.parentId != parentId {
					t.Errorf("parentId not as expected\nwanted: %s\n  got: %s", test.parentId, parentId)
				}
			} else {
				if found {
					t.Errorf("Shouldn't have been able to parse trace context")
				}
			}
		})
	}
}

func TestGenerateSpanPrefix(t *testing.T) {
	prefix := generateSpanPrefix("this is a test")
	if len(prefix) != 6 {
		t.Errorf("Span prefix should be 6 bytes but got %d", len(prefix))
	}
	expected := "fa26be19de6b"
	v := fmt.Sprintf("%x", prefix)
	if expected != v {
		t.Errorf("expected prefix to be %q but got %q", expected, v)
	}
}

func TestMarshalSpan(t *testing.T) {
	startTime := time.Date(2019, time.December, 25, 0, 0, 0, 200, time.UTC)
	endTime := time.Date(2019, time.December, 25, 23, 59, 59, 100, time.UTC)
	parentSpanId := "FEDCBA9876543210" // 8 bytes as 16 hex-coded digits
	spanId := "0123456789ABCDEF"       // 8 bytes as 16 hex-coded digits
	spanName := "projects/projectId/traces/traceId/spans/" + spanId
	longDescription := "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat"

	tests := []struct {
		name         string
		span         spanInfo
		parentSpanId string
		expected     map[string]interface{}
	}{
		{
			name: "short name and no parent",
			span: spanInfo{name: "short", start: startTime, end: endTime},
			expected: map[string]interface{}{
				"name":        spanName,
				"spanId":      spanId,
				"displayName": map[string]interface{}{"value": "short", "truncatedByteCount": 0},
				"startTime":   "2019-12-25T00:00:00.0000002Z",
				"endTime":     "2019-12-25T23:59:59.0000001Z",
			},
		},
		{
			name:         "short name and a parent",
			span:         spanInfo{name: "short", start: startTime, end: endTime},
			parentSpanId: parentSpanId,
			expected: map[string]interface{}{
				"name":         spanName,
				"spanId":       spanId,
				"displayName":  map[string]interface{}{"value": "short", "truncatedByteCount": 0},
				"startTime":    "2019-12-25T00:00:00.0000002Z",
				"endTime":      "2019-12-25T23:59:59.0000001Z",
				"parentSpanId": parentSpanId,
			},
		},
		{
			name:         "long name and a parent",
			span:         spanInfo{name: longDescription, start: startTime, end: endTime},
			parentSpanId: parentSpanId,
			expected: map[string]interface{}{
				"name":         spanName,
				"spanId":       spanId,
				"displayName":  map[string]interface{}{"value": longDescription[0:128], "truncatedByteCount": len(longDescription) - 128},
				"startTime":    "2019-12-25T00:00:00.0000002Z",
				"endTime":      "2019-12-25T23:59:59.0000001Z",
				"parentSpanId": parentSpanId,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := marshalSpan(spanName, spanId, test.parentSpanId, &test.span)

			if diff := cmp.Diff(test.expected, result); diff != "" {
				t.Errorf("MarshalSpan() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWriteTrace(t *testing.T) {
	tmpDir := t.TempDir()
	startTime := time.Date(2019, time.December, 25, 0, 0, 0, 0, time.UTC)
	endTime := time.Date(2019, time.December, 25, 23, 59, 59, 0, time.UTC)

	spans := []*spanInfo{
		{name: "span1", start: startTime, end: endTime},
		{name: "span2", start: startTime, end: endTime},
	}

	writeTrace(tmpDir, "/cnb/buildpacks/foo/bin/detect", "projectId", "traceId", "", spans)

	file, err := os.Open(filepath.Join(tmpDir, "_cnb_buildpacks_foo_bin_detect"))
	if err != nil {
		t.Fatal("trace file does not exist")
	}
	content, err := ioutil.ReadAll(file)
	if err != nil {
		t.Fatal("unable to read trace file")
	}
	expected := `{"spans":[` +
		`{"displayName":{"truncatedByteCount":0,"value":"span1"},"endTime":"2019-12-25T23:59:59Z","name":"projects/projectId/traces/traceId/spans/a776ff0aecd90001","parentSpanId":"a776ff0aecd90000","spanId":"a776ff0aecd90001","startTime":"2019-12-25T00:00:00Z"},` +
		`{"displayName":{"truncatedByteCount":0,"value":"span2"},"endTime":"2019-12-25T23:59:59Z","name":"projects/projectId/traces/traceId/spans/a776ff0aecd90002","parentSpanId":"a776ff0aecd90000","spanId":"a776ff0aecd90002","startTime":"2019-12-25T00:00:00Z"},` +
		`{"displayName":{"truncatedByteCount":0,"value":"/cnb/buildpacks/foo/bin/detect"},"endTime":"2019-12-25T23:59:59Z","name":"projects/projectId/traces/traceId/spans/a776ff0aecd90000","spanId":"a776ff0aecd90000","startTime":"2019-12-25T00:00:00Z"}]}`
	if diff := cmp.Diff(expected, string(content)); diff != "" {
		t.Errorf("writeTrace() mismatch (-want +got):\n%s", diff)
	}
}
