package main

import "testing"

func TestResolveDownloadFilename(t *testing.T) {
	if got, err := resolveDownloadFilename("", "image.jpg"); err != nil || got != "image.jpg" {
		t.Fatalf("resolveDownloadFilename(empty) = (%q, %v), want (image.jpg, nil)", got, err)
	}
	if got, err := resolveDownloadFilename("/tmp/dir/photo.png", "image.jpg"); err != nil || got != "photo.png" {
		t.Fatalf("resolveDownloadFilename(path) = (%q, %v), want (photo.png, nil)", got, err)
	}
	for _, bad := range []string{".", "..", "/", "dir/.."} {
		if got, err := resolveDownloadFilename(bad, "image.jpg"); err == nil {
			t.Fatalf("resolveDownloadFilename(%q) = (%q, nil), want error", bad, got)
		}
	}
}
