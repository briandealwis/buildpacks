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
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DxOption is a function that provides download or extract options.
type DxOption func(o *dxParams)

// StripComponents causes the first N components to be dropped when extracting an archive.
// For example, extracting an archive with a single file `gradle-5.2.3/bin/gradle` with
// `StripComponents(1)` would cause the `gradle-5.2.3` to be dropped and result in a file `bin/gradle`.
func StripComponents(toStrip int) DxOption {
	return func(o *dxParams) {
		o.stripComponents = toStrip
	}
}

// KeepDirectorySymlink preserves existing symlinks to directories during extraction.
func KeepDirectorySymlink() DxOption {
	return func(o *dxParams) {
		o.keepDirectorySymlink = true
	}
}

// Wildcards only extracts files matching the given wildcard.  The filenames are matched
// prior to applying StripComponents.
func Wildcards(wildcards string) DxOption {
	return func(o *dxParams) {
		o.wildcards = wildcards
	}
}

// Download causes the given url to be downloaded and stored at the provided path, exiting on any error.
// Download will cache the download if configured.
func (ctx *Context) Download(description, downloadUrl, dest string, options ...DxOption) {
	// skip checking parent as curl will fail if the parent directory does not exist
	if fi, err := os.Stat(dest); err != nil && !os.IsNotExist(err) {
		ctx.Exit(1, InternalErrorf("could not access %q: %v", dest, err))
	} else if err == nil && !fi.Mode().IsRegular() {
		ctx.Exit(1, InternalErrorf("%q is not a file", dest))
	}

	if cacheLocation, shouldCache := ctx.ShouldCache(); shouldCache {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(downloadUrl)))
		downloadsPath := filepath.Join(cacheLocation, "downloads", ctx.BuildpackID())
		archivePath := filepath.Join(downloadsPath, hash)

		if ctx.FileExists(archivePath) {
			ctx.CacheHit(downloadUrl)
		} else {
			ctx.CacheMiss(downloadUrl)
			ctx.MkdirAll(downloadsPath, 0755)
			download(ctx, description, downloadUrl, archivePath)
		}
		ctx.CopyFile(archivePath, dest)
	} else {
		download(ctx, description, downloadUrl, dest)
	}
}

// DownloadAndExtract causes the given url to be downloaded and extracted to the given directory, exiting on any error.
// DownloadAndExtract will cache the download if configured.
func (ctx *Context) DownloadAndExtract(description, downloadUrl, destdir string, options ...DxOption) {
	if fi, err := os.Stat(destdir); err != nil {
		ctx.Exit(1, InternalErrorf("cannot access %q: %v", destdir, err))
	} else if !fi.IsDir() {
		ctx.Exit(1, InternalErrorf("location %q not a directory", destdir))
	}

	dxParams := dxParams{}
	for _, o := range options {
		o(&dxParams)
	}

	extractor, err := determineFileType(downloadUrl)
	if err != nil {
		ctx.Exit(1, err)
	}

	if cacheLocation, shouldCache := ctx.ShouldCache(); shouldCache {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(downloadUrl)))
		downloadsPath := filepath.Join(cacheLocation, "downloads", ctx.BuildpackID())
		archivePath := filepath.Join(downloadsPath, hash)

		if ctx.FileExists(archivePath) {
			ctx.CacheHit(downloadUrl)
		} else {
			ctx.CacheMiss(downloadUrl)
			ctx.MkdirAll(downloadsPath, 0755)
			download(ctx, description, downloadUrl, archivePath)
		}
		if extractor.commandLine != nil {
			extract := extractor.commandLine(ctx, archivePath, destdir, dxParams)
			ctx.Exec(extract, WithUserAttribution)
		} else {
			extractor.run(ctx, archivePath, destdir, dxParams)
		}
	} else if extractor.supportsStdin && extractor.commandLine != nil {
		download := []string{"curl", "--fail", "--show-error", "--silent", "--location", "--retry", "3", downloadUrl}
		extract := extractor.commandLine(ctx, "", destdir, dxParams)
		command := []string{"bash", "-c", shJoin(download) + " | " + shJoin(extract)}
		runCurl(ctx, description, command, WithUserAttribution)
	} else {
		hash := fmt.Sprintf("%x%s", sha256.Sum256([]byte(downloadUrl)), extractor.extension)
		archivePath := filepath.Join(destdir, hash)
		download(ctx, description, downloadUrl, archivePath)
		defer ctx.RemoveAll(archivePath)
		if extractor.commandLine != nil {
			extract := extractor.commandLine(ctx, archivePath, destdir, dxParams)
			ctx.Exec(extract, WithUserAttribution)
		} else {
			extractor.run(ctx, archivePath, destdir, dxParams)
		}
	}
}

// download is a simple wrapper around curl for downloading to a file.
func download(ctx *Context, description, url, dest string) {
	command := []string{"curl", "--silent", "--fail", "--show-error", "--location", "--retry", "3", "--output", dest, url}
	runCurl(ctx, description, command, WithUserAttribution, OnError(func(_ *Error) { os.Remove(dest) }))
}

// runCurl is a simple wrapper around curl that does additional error processing.
// curl does not give very useful error messages (b/155874677)
// "Failure: curl: (22) The requested URL returned error: 404".
func runCurl(ctx *Context, description string, command []string, options ...ExecOption) {
	if result, err := ctx.ExecWithErr(command, options...); err != nil {
		exitCode := 1
		if result != nil {
			exitCode = result.ExitCode
			ctx.Exit(exitCode, InternalErrorf("fetching %s: %v\n%s", description, err, result.Stderr))
		}
		ctx.Exit(exitCode, InternalErrorf("fetching %s: %v", description, err))
	}
}

// shJoin turns a command array into a double-quote-bounded command-line for /bin/sh with quoting as required.
// For example,
func shJoin(commandLine []string) string {
	var s string
	for i, c := range commandLine {
		if strings.ContainsAny(c, " ?*") {
			c = `\"` + c + `\"`
		}
		if i == 0 {
			s = c
		} else {
			s += " " + c
		}
	}
	return s
}

// dxParams records the parameters required to extract a downloaded file to disk.
type dxParams struct {
	stripComponents      int
	keepDirectorySymlink bool
	wildcards            string
}

// extractionCommandLineGenerator is a signature for a function that generates a command-line for extracting
// the content of the given srcfile to the directory as destdir.  srcfile may be an empty string if this extrator
// supports taking input from stdin.
type extractionCommandLineGenerator func(ctx *Context, srcfile, destdir string, params dxParams) []string

// extractCommand is a function for a
type extractCommand func(ctx *Context, src, destdir string, params dxParams)

type extractor struct {
	// extension is the file extension supported.
	extension string
	// supportsStdin is true if this extractor can take the archive on stdin.
	supportsStdin bool
	// commandLine is a function to generate a command-line to extract input to destination (if supported)
	commandLine extractionCommandLineGenerator
	// run actually extracts the input archive to destination
	run extractCommand
}

// extractTypes maps file type to extractor commands.
// tar can be run as a single-element command.
// unzip requires more processing as it doesn't support stripComponents natively.
var extractTypes = []extractor{
	{extension: ".tar", supportsStdin: true, commandLine: tarGenerator("")},
	{extension: ".tar.gz", supportsStdin: true, commandLine: tarGenerator("z")},
	{extension: ".tar.bz2", supportsStdin: true, commandLine: tarGenerator("j")},
	{extension: ".tar.xz", supportsStdin: true, commandLine: tarGenerator("J")},
	{extension: ".tar.Z", supportsStdin: true, commandLine: tarGenerator("Z")},
	{extension: ".zip", run: extractZip},
}

// determineFileType tries to determine the file type of a download.
// We currently assume that the download is a tar archive of some kind.
func determineFileType(downloadUrl string) (extractor, *Error) {
	u, err := url.Parse(downloadUrl)
	if err != nil {
		return extractor{}, Errorf(StatusInternal, err.Error())
	}
	filename := path.Base(u.Path)
	if filename == "" {
		return extractor{}, Errorf(StatusInternal, "unable to determine local file name from %q", downloadUrl)
	}

	for _, x := range extractTypes {
		if strings.HasSuffix(filename, x.extension) {
			return x, nil
		}
	}
	return extractor{}, Errorf(StatusInternal, "unable to determine file archive type from %q", filename)
}

func tarGenerator(typeFlag string) extractionCommandLineGenerator {
	return func(ctx *Context, src, destdir string, params dxParams) []string {
		cmd := []string{"tar"}
		if src == "" {
			// input is stdin
			cmd = append(cmd, "x"+typeFlag)
		} else {
			cmd = append(cmd, "x"+typeFlag+"f", src)
		}
		cmd = append(cmd, "--directory", destdir)
		if params.stripComponents > 0 {
			cmd = append(cmd, fmt.Sprintf("--strip-components=%d", params.stripComponents))
		}
		if params.keepDirectorySymlink {
			cmd = append(cmd, "--keep-directory-symlink")
		}
		if params.wildcards != "" {
			cmd = append(cmd, "--wildcards", params.wildcards)
		}
		return cmd
	}
}

func extractZip(ctx *Context, src, destdir string, params dxParams) {
	// ignore dxParams.keepDirectorySymlink as zip doesn't support not extracting symlink
	if src != "" && params.stripComponents == 0 {
		command := []string{"unzip", "-q", "-d", destdir, src}
		if params.wildcards != "" {
			command = append(command, params.wildcards)
		}
		ctx.Exec(command, WithUserAttribution)
		return
	}
	tmpDir, err := ioutil.TempDir(destdir, "zip*")
	if err != nil {
		ctx.Exit(1, Errorf(StatusInternal, "unable to create archive extraction directory: %v", err))
	}
	defer ctx.RemoveAll(tmpDir)
	command := []string{"unzip", "-q", "-d", tmpDir, src}
	if params.wildcards != "" {
		command = append(command, params.wildcards)
	}
	ctx.Exec(command, WithUserAttribution)
	walkToDepth(ctx, tmpDir, params.stripComponents, func(dir, base string) {
		ctx.Rename(filepath.Join(dir, base), filepath.Join(destdir, base))
	})
}

func walkToDepth(ctx *Context, dir string, depth int, visitor func(string, string)) {
	for _, fi := range ctx.ReadDir(dir) {
		if depth == 0 {
			visitor(dir, fi.Name())
		} else if fi.IsDir() {
			walkToDepth(ctx, filepath.Join(dir, fi.Name()), depth-1, visitor)
		} else {
			ctx.Warnf("walk: unexpected file %q with remaining depth %d", filepath.Join(dir, fi.Name()), depth-1)
		}
	}
}
