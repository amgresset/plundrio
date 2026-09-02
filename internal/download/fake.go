package download

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/elsbrock/go-putio"
)

// Extensions that make a transfer worth downloading at all.
var mediaExtensions = map[string]bool{
	// video
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".wmv": true,
	".ts": true, ".m2ts": true, ".mts": true, ".mpg": true, ".mpeg": true, ".webm": true,
	".flv": true, ".vob": true, ".divx": true, ".iso": true, ".img": true,
	// audio
	".flac": true, ".mp3": true, ".m4a": true, ".aac": true, ".ogg": true, ".opus": true, ".wav": true,
	// books
	".epub": true, ".mobi": true, ".azw3": true, ".pdf": true, ".cbz": true, ".cbr": true,
}

// Extensions that mark the classic public-tracker fake: a folder named like a
// real release that only contains a Windows executable.
var executableExtensions = map[string]bool{
	".exe": true, ".scr": true, ".bat": true, ".cmd": true, ".com": true, ".msi": true,
	".pif": true, ".vbs": true, ".js": true, ".jar": true, ".lnk": true, ".ps1": true,
}

// looksLikeFakeRelease reports whether a transfer contains nothing an *arr
// app could import: no media file at all. Public-tracker fakes come in two
// flavours, a lone Windows executable named like the release, or a handful of
// images and text files. Either way there is nothing worth pulling. It
// returns a short description of what the transfer did contain.
func looksLikeFakeRelease(files []*putio.File) (bool, string) {
	if len(files) == 0 {
		return false, ""
	}
	var exe string
	seen := map[string]bool{}
	var kinds []string
	for _, f := range files {
		ext := strings.ToLower(path.Ext(f.Name))
		if mediaExtensions[ext] {
			return false, ""
		}
		if exe == "" && executableExtensions[ext] {
			exe = f.Name
		}
		if ext == "" {
			ext = "(none)"
		}
		if !seen[ext] {
			seen[ext] = true
			kinds = append(kinds, ext)
		}
	}
	if exe != "" {
		return true, "only executable " + strconv.Quote(exe)
	}
	return true, "only " + strings.Join(kinds, ", ") + " files"
}

// FakeReleaseError is attached to a transfer context when the transfer was
// refused for containing no media files.
type FakeReleaseError struct {
	Detail string
}

func (e *FakeReleaseError) Error() string {
	return fmt.Sprintf("fake release: no media files, %s", e.Detail)
}
