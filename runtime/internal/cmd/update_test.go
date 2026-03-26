package cmd

import (
	"testing"
)

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
		{"v0.0.1", "0.0.1"},
		{"dev", "dev"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeVersion(tt.input)
		if got != tt.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildArchiveName(t *testing.T) {
	tests := []struct {
		ver, goos, goarch string
		want              string
	}{
		{"1.2.3", "linux", "amd64", "amurg-runtime_1.2.3_linux_amd64.tar.gz"},
		{"1.2.3", "darwin", "arm64", "amurg-runtime_1.2.3_darwin_arm64.tar.gz"},
		{"0.5.0", "windows", "amd64", "amurg-runtime_0.5.0_windows_amd64.zip"},
		{"1.0.0", "linux", "arm64", "amurg-runtime_1.0.0_linux_arm64.tar.gz"},
		{"2.0.0", "darwin", "amd64", "amurg-runtime_2.0.0_darwin_amd64.tar.gz"},
		{"1.0.0", "windows", "arm64", "amurg-runtime_1.0.0_windows_arm64.zip"},
	}
	for _, tt := range tests {
		got := buildArchiveName(tt.ver, tt.goos, tt.goarch)
		if got != tt.want {
			t.Errorf("buildArchiveName(%q, %q, %q) = %q, want %q",
				tt.ver, tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestParseChecksum(t *testing.T) {
	checksums := `abc123def456  amurg-runtime_1.2.3_linux_amd64.tar.gz
deadbeef0000  amurg-runtime_1.2.3_darwin_arm64.tar.gz
1111222233334444  amurg-runtime_1.2.3_windows_amd64.zip
`

	tests := []struct {
		filename string
		wantHash string
		wantErr  bool
	}{
		{"amurg-runtime_1.2.3_linux_amd64.tar.gz", "abc123def456", false},
		{"amurg-runtime_1.2.3_darwin_arm64.tar.gz", "deadbeef0000", false},
		{"amurg-runtime_1.2.3_windows_amd64.zip", "1111222233334444", false},
		{"amurg-runtime_1.2.3_freebsd_amd64.tar.gz", "", true},
		{"nonexistent.tar.gz", "", true},
	}
	for _, tt := range tests {
		got, err := parseChecksum(checksums, tt.filename)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseChecksum(%q) error = %v, wantErr %v", tt.filename, err, tt.wantErr)
			continue
		}
		if got != tt.wantHash {
			t.Errorf("parseChecksum(%q) = %q, want %q", tt.filename, got, tt.wantHash)
		}
	}
}

func TestParseChecksumEdgeCases(t *testing.T) {
	// Empty checksums.
	if _, err := parseChecksum("", "foo.tar.gz"); err == nil {
		t.Error("expected error for empty checksums")
	}

	// Blank lines and extra whitespace.
	checksums := `
  aabbccdd  foo.tar.gz

  11223344  bar.tar.gz
`
	hash, err := parseChecksum(checksums, "foo.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "aabbccdd" {
		t.Errorf("got %q, want %q", hash, "aabbccdd")
	}
}
