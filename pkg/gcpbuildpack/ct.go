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
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func writeStats(stats *stats) {
	project, found := os.LookupEnv("GOOGLE_TRACE_PROJECT")
	if !found || project == "" {
		return
	}
	traceDir, found := os.LookupEnv("GOOGLE_TRACE_DIR")
	if !found || traceDir == "" {
		return
	}
	// Maybe we should create a traceId if not found
	traceId, parentId, found := parseTraceContext()
	if !found {
		return
	}
	writeTrace(traceDir, os.Args[0], project, traceId, parentId, stats.spans)
}

func parseTraceContext() (string, string, bool) {
	// Support W3C-like convention of passing trace context
	traceParent, found := os.LookupEnv("TRACEPARENT")
	if found {
		re := regexp.MustCompile(`00-([0-9a-fA-F]{32})-([0-9a-fA-F]{16})-0[01]`)
		if elements := re.FindStringSubmatch(traceParent); elements != nil {
			return elements[1], elements[2], true
		}
	}
	return "", "", false
}

// writeTrace writes out the spans in a form suitable to be sent to Cloud Trace's batchWrite.
func writeTrace(dir, description, projectId, traceId, rootSpanId string, spans []*spanInfo) {
	// Cloud Trace expects the Span IDs to be 8 bytes (encoded as 16 hex digits).
	// We hash our span description and use the fix 6 bytes, leaving space for 65536 spans.
	spanIdPrefix := generateSpanPrefix(description)
	parentSpanId := fmt.Sprintf("%x%04x", spanIdPrefix, 0)

	parentSpan := spanInfo{name: description} // different from Cloud Trace span name
	if len(spans) == 0 {
		parentSpan.start = time.Now()
		parentSpan.end = time.Now()
	} else {
		parentSpan.start = spans[0].start
		parentSpan.end = spans[0].end
	}

	var s []interface{} // the Cloud Trace transformed spans to be marshalled

	for i, span := range spans {
		if parentSpan.start.After(span.start) {
			parentSpan.start = span.start
		}
		if parentSpan.end.Before(span.end) {
			parentSpan.end = span.end
		}
		spanId := fmt.Sprintf("%x%04x", spanIdPrefix, i+1)
		spanName := fmt.Sprintf("projects/%s/traces/%s/spans/%s", projectId, traceId, spanId)
		s = append(s, marshalSpan(spanName, spanId, parentSpanId, span))
	}

	// Create parent span to contain the provided spans.
	parentSpanName := fmt.Sprintf("projects/%s/traces/%s/spans/%s", projectId, traceId, parentSpanId)
	s = append(s, marshalSpan(parentSpanName, parentSpanId, rootSpanId, &parentSpan))

	t := map[string]interface{}{"spans": s}
	b, err := json.Marshal(t)
	if err != nil {
		return
	}
	file := filepath.Join(dir, strings.ReplaceAll(description, string(os.PathSeparator), "_"))
	ioutil.WriteFile(file, b, 0644)
}

func generateSpanPrefix(description string) []byte {
	// We hash the trace name to provide a unique prefix using the first 6 bytes of the hash.
	h := sha1.New()
	h.Write([]byte(description))
	bs := h.Sum(nil)
	return bs[0:6]
}

// parentSpanId and spanId are a 16-character hexadecimal encoding of an 8-byte array.
func marshalSpan(spanName, spanId, parentSpanId string, span *spanInfo) interface{} {
	truncated, remaining := span.name, 0
	if len(truncated) > 128 {
		truncated = span.name[0:128]
		remaining = len(span.name) - 128
	}

	m := map[string]interface{}{
		"name":        spanName,
		"spanId":      spanId,
		"displayName": map[string]interface{}{"value": truncated, "truncatedByteCount": remaining},
		"startTime":   span.start.Format(time.RFC3339Nano),
		"endTime":     span.end.Format(time.RFC3339Nano),
	}
	if parentSpanId != "" {
		m["parentSpanId"] = parentSpanId
	}
	if len(span.attributes) > 0 {
		m["attributes"] = marshalAttributes(span.attributes)
	}

	return m
}

func marshalAttributes(attributes map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	m := make(map[string]interface{})
	result["attributeMap"] = m
	for k, v := range attributes {
		switch value := v.(type) {
		case string:
			m[k] = map[string]interface{}{
				"stringValue": map[string]string{"value": value},
			}
		case int:
			m[k] = map[string]interface{}{
				"intValue": value,
			}
		case bool:
			m[k] = map[string]interface{}{
				"boolValue": value,
			}
		}
	}
	return result
}
