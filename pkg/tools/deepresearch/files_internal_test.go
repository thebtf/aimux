package deepresearch

import "testing"

// detectMIMEType is unexported — test from within the package.

func TestDetectMIMEType(t *testing.T) {
	tests := []struct {
		path     string
		wantMIME string
	}{
		{"doc.pdf", "application/pdf"},
		{"notes.txt", "text/plain"},
		{"readme.md", "text/plain"},
		{"main.go", "text/plain"},
		{"app.ts", "text/plain"},
		{"script.js", "text/plain"},
		{"model.py", "text/plain"},
		{"lib.rs", "text/plain"},
		{"config.json", "application/json"},
		{"config.yaml", "application/x-yaml"},
		{"config.yml", "application/x-yaml"},
		{"image.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"binary.bin", "application/octet-stream"},
		{"noext", "application/octet-stream"},
		{"archive.zip", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := detectMIMEType(tt.path)
			if got != tt.wantMIME {
				t.Errorf("detectMIMEType(%q) = %q, want %q", tt.path, got, tt.wantMIME)
			}
		})
	}
}
