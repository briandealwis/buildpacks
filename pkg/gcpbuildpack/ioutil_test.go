// Copyright 2021 Google LLC
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
	"bytes"
	"path/filepath"
	"testing"
)

func TestIOHelpers(t *testing.T) {
	content := []byte("Hello, World")
	ctx := NewContext()
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	ctx.WriteFile(file, content, 0666)

	if dc := ctx.ReadDir(dir); len(dc) != 1 || dc[0].Name() != "file" || dc[0].Size() != int64(len(content)) || !dc[0].Mode().IsRegular() {
		t.Fatal("file creation not as planned")
	}
	if c := ctx.ReadFile(file); bytes.Compare(c, content) != 0 {
		t.Fatalf("copied file content: wanted=%q got=%q", string(content), string(c))
	}

	newfile := filepath.Join(dir, "newfile")
	ctx.CopyFile(file, newfile)

	if dc := ctx.ReadDir(dir); len(dc) != 2 {
		t.Fatal("file creation not as planned")
	}
	if c := ctx.ReadFile(newfile); bytes.Compare(c, content) != 0 {
		t.Fatalf("copied file content: wanted=%q got=%q", string(content), string(c))
	}
}
