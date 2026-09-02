package download

import (
	"testing"

	"github.com/elsbrock/go-putio"
)

func files(names ...string) []*putio.File {
	var fs []*putio.File
	for _, n := range names {
		fs = append(fs, &putio.File{Name: n})
	}
	return fs
}

func TestLooksLikeFakeRelease(t *testing.T) {
	cases := []struct {
		name  string
		files []*putio.File
		fake  bool
	}{
		{"exe only", files("Show S01E01 1080p WEB h264-GROUP.exe"), true},
		{"scr only, uppercase", files("Show.S01E01.1080p.WEB.H264-GROUP.SCR"), true},
		{"exe plus nfo and jpg", files("release.exe", "release.nfo", "cover.jpg"), true},
		{"real release with sample and nfo", files("Show.S01E01.mkv", "Show.S01E01.nfo"), false},
		{"real release that also ships an exe", files("Show.S01E01.mkv", "codec-installer.exe"), false},
		{"only images and text (UIndex-style junk)", files("cover.jpg", "info.txt", "read.nfo"), true},
		{"epub", files("book.epub"), false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		fake, detail := looksLikeFakeRelease(c.files)
		if fake != c.fake {
			t.Errorf("%s: fake=%v want %v (detail=%q)", c.name, fake, c.fake, detail)
		}
		if fake && detail == "" {
			t.Errorf("%s: expected a description of the contents", c.name)
		}
	}
}
