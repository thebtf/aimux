package deepresearch

import (
	"context"
	"fmt"
	"path/filepath"

	"google.golang.org/genai"
)

// UploadFiles uploads local files to Google Files API for use in research prompts.
// Returns uploaded file objects for embedding in prompts.
func UploadFiles(ctx context.Context, client *genai.Client, paths []string, cwd string) ([]*genai.File, error) {
	var uploaded []*genai.File

	for _, path := range paths {
		// Resolve relative paths
		if !filepath.IsAbs(path) && cwd != "" {
			path = filepath.Join(cwd, path)
		}

		// Detect MIME type from extension
		mimeType := detectMIMEType(path)

		// Upload via Files API (UploadFromPath handles file reading)
		file, err := client.Files.UploadFromPath(ctx, path, &genai.UploadFileConfig{
			DisplayName: filepath.Base(path),
			MIMEType:    mimeType,
		})
		if err != nil {
			return uploaded, fmt.Errorf("upload file %s: %w", path, err)
		}

		uploaded = append(uploaded, file)
	}

	return uploaded, nil
}

func detectMIMEType(path string) string {
	ext := filepath.Ext(path)
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".txt", ".md":
		return "text/plain"
	case ".go", ".ts", ".js", ".py", ".rs":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/x-yaml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return "application/octet-stream"
	}
}
