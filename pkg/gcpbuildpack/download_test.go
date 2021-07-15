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
	"archive/zip"
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/buildpacks/libcnb"
)

func TestExtractTypes(t *testing.T) {
	t.Run("extractType extensions should be unique", func(t *testing.T) {
		seen := map[string]string{}
		for _, x := range extractTypes {
			if _, found := seen[x.extension]; found {
				t.Errorf("extension %q defined multiple times", x.extension)
			}
			seen[x.extension] = x.extension
		}
	})

	for _, x := range extractTypes {
		t.Run("validity "+x.extension, func(t *testing.T) {
			if x.commandLine == nil && x.run == nil {
				t.Errorf("one of .commandLine or .run must be non-nil")
			}
		})
	}
}

func TestDxOptions(t *testing.T) {
	tests := []struct {
		description string
		options     []DxOption
		result      dxParams
	}{
		{"defaults", nil, dxParams{}},
		{"keep directory symbolic links", []DxOption{KeepDirectorySymlink()}, dxParams{keepDirectorySymlink: true}},
		{"strip 2 components", []DxOption{StripComponents(2)}, dxParams{stripComponents: 2}},
		{"wildcards */foo", []DxOption{Wildcards("*/foo")}, dxParams{wildcards: "*/foo"}},
		{"keep directory symbolic links + strip 1 components + wildcards */foo", []DxOption{KeepDirectorySymlink(), StripComponents(1), Wildcards("*/foo")}, dxParams{keepDirectorySymlink: true, stripComponents: 1, wildcards: "*/foo"}},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			dxParams := dxParams{}
			for _, o := range test.options {
				o(&dxParams)
			}
			if !reflect.DeepEqual(test.result, dxParams) {
				t.Errorf("got=%v want=%v", dxParams, test.result)
			}
		})
	}
}

func TestDetermineFileType(t *testing.T) {
	tests := []struct {
		url       string
		shouldErr bool
		extension string
	}{
		{"", true, ""},
		{"ht|tp:/invalid/url/foo.tar.gz", true, ""},
		{"https://web.site/foo.tar", false, ".tar"},
		{"https://web.site/foo.tar.gz", false, ".tar.gz"},
		{"https://web.site/foo.tar.Z", false, ".tar.Z"},
		{"https://web.site/foo.tar.bz2", false, ".tar.bz2"},
		{"https://web.site/foo.tar.xz", false, ".tar.xz"},
		{"https://web.site/foo.zip", false, ".zip"},
		{"foo.tar.gz", false, ".tar.gz"},
		{"https://web.site/foo", true, ""},
		{"https://web.site/foo.foo", true, ""},
	}

	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			x, err := determineFileType(test.url)
			if test.shouldErr && err == nil {
				t.Error("should have errored")
			} else if !test.shouldErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			} else if !test.shouldErr && x.extension != test.extension {
				t.Errorf("got=%v want=%v", x.extension, test.extension)
			}
		})
	}
}

func TestExtract_CommandLine(t *testing.T) {
	tests := []struct {
		extension string
		source    string
		dest      string
		params    dxParams
		result    []string
	}{
		{".tar", "", "/tmp", dxParams{}, []string{"tar", "x", "--directory", "/tmp"}},
		{".tar.Z", "", "/tmp", dxParams{}, []string{"tar", "xZ", "--directory", "/tmp"}},
		{".tar.gz", "", "/tmp", dxParams{}, []string{"tar", "xz", "--directory", "/tmp"}},
		{".tar.bz2", "", "/tmp", dxParams{}, []string{"tar", "xj", "--directory", "/tmp"}},
		{".tar.xz", "", "/tmp", dxParams{}, []string{"tar", "xJ", "--directory", "/tmp"}},
		{".tar.xz", "file", "/tmp", dxParams{}, []string{"tar", "xJf", "file", "--directory", "/tmp"}},
		{".tar.xz", "file", "/tmp", dxParams{keepDirectorySymlink: true}, []string{"tar", "xJf", "file", "--directory", "/tmp", "--keep-directory-symlink"}},
		{".tar.xz", "", "/tmp", dxParams{keepDirectorySymlink: true, stripComponents: 20}, []string{"tar", "xJ", "--directory", "/tmp", "--strip-components=20", "--keep-directory-symlink"}},
		{".tar.xz", "", "/tmp", dxParams{wildcards: "*/foo"}, []string{"tar", "xJ", "--directory", "/tmp", "--wildcards", "*/foo"}},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("from %q to %q with %v", test.source, test.dest, test.params), func(t *testing.T) {
			x, err := determineFileType(test.extension)
			if err != nil {
				t.Fatalf("unable to determine extractor for %q", test.extension)
			}
			if x.commandLine == nil {
				t.Fatalf("extractor for %q does not have commandLine() function", test.extension)
			}
			result := x.commandLine(NewContext(), test.source, test.dest, test.params)
			if !reflect.DeepEqual(result, test.result) {
				t.Errorf("got=%v want=%v", result, test.result)
			}
		})
	}
}

func TestExtractZip(t *testing.T) {
	files := map[string]string{
		"gradle/bin/gradle":     "gradle shell script",
		"gradle/lib/gradle.jar": "gradle jar",
	}

	zipfile := filepath.Join(t.TempDir(), "file.zip")
	zf, err := os.Create(zipfile)
	if err != nil {
		t.Fatal("error creating temp file", err)
	}
	w := zip.NewWriter(zf)
	for name, content := range files {
		if f, err := w.Create(name); err != nil {
			t.Fatalf("could not create zip with file %q: %v", name, err)
		} else if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("could not write content for file %q: %v", name, err)
		}
	}
	w.Close()

	tests := []struct {
		description string
		params      dxParams
		expected    []string
	}{
		{"no stripComponents", dxParams{}, []string{"gradle/bin/gradle", "gradle/lib/gradle.jar"}},
		{"stripComponents=1", dxParams{stripComponents: 1}, []string{"bin/gradle", "lib/gradle.jar"}},
		{"stripComponents=2", dxParams{stripComponents: 2}, []string{"gradle", "gradle.jar"}},
		{"stripComponents=3", dxParams{stripComponents: 3}, []string{}},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			destdir := t.TempDir()
			extractZip(NewContext(), zipfile, destdir, test.params)
			for _, f := range test.expected {
				if fi, err := os.Stat(filepath.Join(destdir, filepath.FromSlash(f))); err != nil {
					t.Errorf("expected to find file %q", f)
				} else if !fi.Mode().IsRegular() {
					t.Errorf("should have been a file %q: %v", f, fi)
				}
			}
		})
	}
}

func TestWalkToDepth(t *testing.T) {
	root := t.TempDir()
	paths := []string{"5", "4", "3", "2", "1"}
	if err := os.MkdirAll(filepath.Join(root, filepath.Join(paths...)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Join(root, filepath.Join(paths...)), "file"), []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	for depth := 0; depth < len(paths); depth++ {
		t.Run(fmt.Sprintf("depth %d", depth), func(t *testing.T) {
			var files []string
			walkToDepth(NewContext(), root, depth, func(dir, base string) {
				files = append(files, base)
			})
			if len(files) != 1 {
				t.Errorf("expected single file result: %v", files)
			} else if files[0] != paths[depth] {
				t.Errorf("depth %d: got=%q want=%q", depth, files[0], paths[depth])
			}
		})
	}
}

func TestShJoin(t *testing.T) {
	tests := []struct {
		input    []string
		expected string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a", "b", "c"}, "a b c"},
		{[]string{"a b", "c d"}, `\"a b\" \"c d\"`},
		{[]string{"a", "*/b"}, `a \"*/b\"`},
		{[]string{"a", "b?"}, `a \"b?\"`},
		{[]string{"tar", "xJ", "--directory", "/tmp", "--wildcards", "*/foo"}, `tar xJ --directory /tmp --wildcards \"*/foo\"`},
	}
	for _, test := range tests {
		t.Run(test.expected, func(t *testing.T) {
			result := shJoin(test.input)
			if result != test.expected {
				t.Errorf("got=%s want=%s", result, test.expected)
			}
		})
	}
}

// TestDownloadAndExtract tests both Download and DownloadAndExtract.
func TestDownloadAndExtract(t *testing.T) {
	testFiles := map[string][]byte{
		"test.txt": []byte("Hello, World"),
	}
	content := new(bytes.Buffer)
	w := zip.NewWriter(content)
	for f, c := range testFiles {
		if f, err := w.Create(f); err != nil {
			t.Fatal("unable to create test zip", err)
		} else {
			f.Write(c)
		}
	}
	w.Close()

	tests := []struct {
		description  string
		cache        bool
		extract      bool
		failDownload bool
		shouldExit   bool
	}{
		{description: "download successful, no caching"},
		{description: "download successful, caching", cache: true},
		{description: "download fails, caching", cache: true, failDownload: true, shouldExit: true},
		{description: "download fails, no caching", failDownload: true, shouldExit: true},
		{description: "download successful, no caching, extracts", extract: true},
		{description: "download successful, caching, extracts", cache: true, extract: true},
		{description: "download fails, caching, extracts", cache: true, failDownload: true, shouldExit: true, extract: true},
		{description: "download fails, no caching, extracts", failDownload: true, shouldExit: true, extract: true},
	}
	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			serveCount := 0

			s := &http.Server{
				Addr: "localhost:9999",
				Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					serveCount++
					if test.failDownload {
						w.WriteHeader(http.StatusNotFound)
					} else {
						w.Header().Add("Content-Type", "application/zip")
						w.Header().Add("Content-Length", strconv.Itoa(content.Len()))
						w.Write(content.Bytes())
					}
				}),
			}
			defer s.Close()
			go s.ListenAndServe() // assume this works!

			if value, found := os.LookupEnv("GOOGLE_CACHE"); found {
				defer os.Setenv("GOOGLE_CACHE", value)
			} else {
				defer os.Unsetenv("GOOGLE_CACHE")
			}
			var cacheLocation string
			if test.cache {
				cacheLocation = t.TempDir()
				os.Setenv("GOOGLE_CACHE", cacheLocation)
			} else {
				os.Unsetenv("GOOGLE_CACHE")
			}

			ctx := NewContext(WithBuildpackInfo(libcnb.BuildpackInfo{ID: "test", Version: "0.0.0"}))
			cacheDest := filepath.Join(cacheLocation, "downloads", ctx.BuildpackID(), "f31d1d5d53982343f6750942d27ad9c5ec973685ead2e14b577438dace64d5ca")

			var exitResult panicExit
			ctx.exiter = &exitResult

			// try downloading twice: when caching, only one download should happen
			for i := 0; i < 2; i++ {
				dest := t.TempDir() // for Download(), dest points to the local file
				if !test.extract {
					dest = filepath.Join(dest, "content.zip")
				}

				// run the test within a function to catch the panic/recover
				func() {
					defer func() {
						if r := recover(); r != nil {
							switch r.(type) {
							case *panicExit:
								// cache file should not exit on ctx.Exit()
								if _, err := os.Stat(cacheDest); err == nil {
									t.Fatal("cache files should not exist on failure", cacheDest)
								}
								if !test.shouldExit {
									t.Fatal("should not have exited")
								}
							default:
								panic(r)
							}
						} else {
							// Exit should call panicExit.Exit() which panics with the panicExit instance
							if test.shouldExit {
								t.Fatal("should have exited")
							}
						}
					}()
					if test.extract {
						ctx.DownloadAndExtract(test.description, "http://localhost:9999/content.zip", dest)
					} else {
						ctx.Download(test.description, "http://localhost:9999/content.zip", dest)
					}
				}()

				if test.shouldExit {
					if !exitResult.called {
						t.Fatal("should have called ctx.Exit")
					}
					if test.failDownload {
						if _, err := os.Stat(cacheDest); err == nil || !os.IsNotExist(err) {
							t.Fatalf("cache file should not exist on failed downloadL %s: %v", cacheDest, err)
						}
					}
				} else {
					if exitResult.called {
						t.Fatal("should not have exited")
					}

					if test.cache {
						if fi, err := os.Stat(cacheDest); err != nil {
							t.Fatal("cache file does not exist", cacheDest, err)
						} else if fi.Size() != int64(content.Len()) {
							t.Fatalf("cached content has wrong size: wanted %d, got %d", content.Len(), fi.Size())
						}
						if b, err := ioutil.ReadFile(cacheDest); err != nil {
							t.Fatal("unable to read cached content", err)
						} else if bytes.Compare(content.Bytes(), b) != 0 {
							t.Fatalf("cached content is wrong: wanted %v, got %v", content.Bytes(), b)
						}
					}

					if test.extract {
						for f, c := range testFiles {
							testFile := filepath.Join(dest, f)
							if _, err := os.Stat(testFile); err != nil {
								t.Fatalf("extracted file %q does not exist: %v", f, err)
							}
							if b, err := ioutil.ReadFile(testFile); err != nil {
								t.Fatalf("unable to read extracted content for %q: %v", f, err)
							} else if bytes.Compare(c, b) != 0 {
								t.Fatalf("extracted content is wrong: wanted %v, got %v", string(c), string(b))
							}
						}
					} else {
						if fi, err := os.Stat(dest); err != nil {
							t.Fatal("dest file does not exist", err)
						} else if fi.Size() != int64(content.Len()) {
							t.Fatalf("downloaded content has wrong size: wanted %d, got %d", content.Len(), fi.Size())
						}
						if b, err := ioutil.ReadFile(dest); err != nil {
							t.Fatal("unable to read downloaded content", err)
						} else if bytes.Compare(content.Bytes(), b) != 0 {
							t.Fatalf("downloaded content is wrong: wanted %v, got %v", content.Bytes(), b)
						}
					}
				}
			}

			// verify fetch counts
			if !test.shouldExit {
				if test.cache && serveCount != 1 {
					t.Errorf("cache enabled but content was fetched %d times", serveCount)
				} else if !test.cache && serveCount != 2 {
					t.Errorf("cache disabled so content should be fetched 2 times but was fetched %d times", serveCount)
				}
			}
		})
	}
}

// panicExit is an exiter that uses panic()/recover().
type panicExit struct {
	called   bool
	exitCode int
	be       *Error
}

func (e *panicExit) Exit(exitCode int, be *Error) {
	e.called = true
	e.exitCode = exitCode
	e.be = be
	panic(e)
}
