package builtin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const readFileTinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

func TestReadFileImageReturnsValidatedBoundedDataURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pixel.png")
	raw, err := base64.StdEncoding.DecodeString(readFileTinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": path})
	text, images, err := (readFile{}).ExecuteWithImages(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || !strings.HasPrefix(images[0], "data:image/png;base64,") || !strings.Contains(text, "image/png") {
		t.Fatalf("text=%q images=%v", text, images)
	}
}

func TestReadFileImageRejectsExtensionOnlyPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-image.png")
	if err := os.WriteFile(path, []byte("not really an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": path})
	if _, _, err := (readFile{}).ExecuteWithImages(context.Background(), args); err == nil {
		t.Fatal("extension-only image payload was accepted")
	}
}

func TestReadFileImageRejectsMismatchedMIME(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pixel.jpg")
	raw, err := base64.StdEncoding.DecodeString(readFileTinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": path})
	if _, _, err := (readFile{}).ExecuteWithImages(context.Background(), args); err == nil {
		t.Fatal("mismatched image MIME was accepted")
	}
}

func TestReadFileImageRejectsTruncatedPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "truncated.png")
	raw, err := base64.StdEncoding.DecodeString(readFileTinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw[:len(raw)-4], 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": path})
	if _, _, err := (readFile{}).ExecuteWithImages(context.Background(), args); err == nil {
		t.Fatal("truncated image payload was accepted")
	}
}

func TestReadFileImageRejectsFilesOverTenMB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(10*1024*1024 + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": path})
	if _, _, err := (readFile{}).ExecuteWithImages(context.Background(), args); err == nil {
		t.Fatal("oversized image was accepted")
	}
}
