package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	_ "embed"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	guideResourceMIMEType = "application/json"
	callerGuideMIMEType   = "text/markdown; charset=utf-8"
)

//go:embed caller_guide.md
var callerGuideMarkdown string

func (s *Server) registerGuideResources() {
	s.mcp.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"aimux://guides",
			"Guide Catalog",
			mcp.WithTemplateDescription("Compiled caller guide catalog"),
			mcp.WithTemplateMIMEType(guideResourceMIMEType),
		),
		s.handleGuideCatalogResource,
	)
	s.mcp.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"aimux://guides/caller",
			"Caller Guide",
			mcp.WithTemplateDescription("Supported task, think, recipe, replay, viewer, and safety guide"),
			mcp.WithTemplateMIMEType(callerGuideMIMEType),
		),
		s.handleCallerGuideResource,
	)
}

func (s *Server) handleGuideCatalogResource(_ context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	if err := validateGuideCatalogURI(request.Params.URI); err != nil {
		return guideResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_uri",
			"error":  err.Error(),
		})
	}
	return guideResourceJSON(request.Params.URI, map[string]any{
		"status":       "ok",
		"resource_uri": request.Params.URI,
		"guides": []map[string]any{
			{
				"id":          "caller",
				"title":       "aimux Caller Guide",
				"uri":         "aimux://guides/caller",
				"mime_type":   callerGuideMIMEType,
				"description": "Supported task, think, recipe, replay, viewer, and safety surface.",
			},
		},
	})
}

func (s *Server) handleCallerGuideResource(_ context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	if err := validateCallerGuideURI(request.Params.URI); err != nil {
		return guideResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_uri",
			"error":  err.Error(),
		})
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      request.Params.URI,
			MIMEType: callerGuideMIMEType,
			Text:     callerGuideMarkdown,
		},
	}, nil
}

func guideResourceJSON(uri string, payload map[string]any) ([]mcp.ResourceContents, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      uri,
			MIMEType: guideResourceMIMEType,
			Text:     string(data),
		},
	}, nil
}

func validateGuideCatalogURI(rawURI string) error {
	return validateExactGuideURI(rawURI, "")
}

func validateCallerGuideURI(rawURI string) error {
	return validateExactGuideURI(rawURI, "caller")
}

func validateExactGuideURI(rawURI, path string) error {
	u, err := url.Parse(rawURI)
	if err != nil {
		return fmt.Errorf("invalid guide resource URI")
	}
	if u.Scheme != "aimux" || u.Host != "guides" || strings.Trim(u.EscapedPath(), "/") != path {
		return fmt.Errorf("invalid guide resource URI")
	}
	return nil
}
