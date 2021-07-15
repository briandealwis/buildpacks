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
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
)

// TempDir creates a directory with the provided name in the buildpack temp layer and returns its path. Exits on any error.
func (ctx *Context) TempDir(name string) string {
	tmpLayer := ctx.Layer("gcpbuildpack-tmp")
	directory := filepath.Join(tmpLayer.Path, name)
	ctx.MkdirAll(directory, 0755)
	return directory
}

// WriteFile invokes ioutil.WriteFile, exiting on any error.
func (ctx *Context) WriteFile(filename string, data []byte, perm os.FileMode) {
	if err := ioutil.WriteFile(filename, data, perm); err != nil {
		ctx.Exit(1, Errorf(StatusInternal, "writing file %q: %v", filename, err))
	}
}

// ReadFile invokes ioutil.ReadFile, exiting on any error.
func (ctx *Context) ReadFile(filename string) []byte {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		ctx.Exit(1, Errorf(StatusInternal, "reading file %q: %v", filename, err))
	}
	return data
}

// ReadDir invokes ioutil.ReadDir, exiting on any error.
func (ctx *Context) ReadDir(elem ...string) []os.FileInfo {
	n := filepath.Join(elem...)
	files, err := ioutil.ReadDir(n)
	if err != nil {
		ctx.Exit(1, Errorf(StatusInternal, "reading directory %q: %v", n, err))
	}
	return files
}

// CopyFile copies from one file to another, exiting on any error.
// dest should be the destination file name, not a directory to hold the copy.
func (ctx *Context) CopyFile(src, dest string) {
	// ensure src exists
	if fi, err := os.Stat(src); err != nil {
		ctx.Exit(1, Errorf(StatusInternal, "could not copy %q: %v", src, err))
	} else if !fi.Mode().IsRegular() {
		ctx.Exit(1, Errorf(StatusInternal, "could not copy %q: is not a file", src))
	}

	// ensure dest either does not exist or is not a directory
	if fi, err := os.Stat(dest); err != nil {
		if !os.IsNotExist(err) {
			ctx.Exit(1, Errorf(StatusInternal, "could not copy to %q: %v", dest, err))
		}
	} else if fi.Mode().IsDir() {
		ctx.Exit(1, Errorf(StatusInternal, "could not copy to %q: is a directory", dest))
	}

	from, err := os.Open(src)
	if err != nil {
		ctx.Exit(1, Errorf(StatusInternal, "could not open %q: %v", dest, err))
	}
	defer from.Close()
	to, err := os.Create(dest)
	if err != nil {
		ctx.Exit(1, Errorf(StatusInternal, "could not create %q: %v", dest, err))
	}
	defer to.Close()
	if _, err := io.Copy(to, from); err != nil {
		ctx.Exit(1, Errorf(StatusInternal, "could not copy into %q: %v", dest, err))
	}
}
