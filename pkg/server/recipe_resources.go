package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thebtf/aimux/pkg/recipes"
)

const recipeResourceMIMEType = "application/json"

func (s *Server) registerRecipeResources() {
	s.mcp.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"aimux://recipes",
			"Recipe Catalog",
			mcp.WithTemplateDescription("Compiled read-only recipe catalog with phases, policy needs, and output resources"),
			mcp.WithTemplateMIMEType(recipeResourceMIMEType),
		),
		s.handleRecipeListResource,
	)
	s.mcp.AddResourceTemplate(
		mcp.NewResourceTemplate(
			"aimux://recipes/{recipe_id}",
			"Recipe Detail",
			mcp.WithTemplateDescription("Compiled recipe detail for one supported recipe ID"),
			mcp.WithTemplateMIMEType(recipeResourceMIMEType),
		),
		s.handleRecipeDetailResource,
	)
}

func (s *Server) handleRecipeListResource(_ context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	if err := validateRecipeListURI(request.Params.URI); err != nil {
		return recipeResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_uri",
			"error":  err.Error(),
		})
	}
	list := recipes.List()
	return recipeResourceJSON(request.Params.URI, map[string]any{
		"recipes": list,
		"count":   len(list),
	})
}

func (s *Server) handleRecipeDetailResource(_ context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	recipeID, err := parseRecipeDetailURI(request.Params.URI)
	if err != nil {
		return recipeResourceJSON(request.Params.URI, map[string]any{
			"status": "invalid_uri",
			"error":  err.Error(),
		})
	}
	recipe, ok := recipes.Resolve(recipeID)
	if !ok {
		return recipeResourceJSON(request.Params.URI, recipeResourceNotFoundPayload(recipeID))
	}
	return recipeResourceJSON(request.Params.URI, recipePayload(recipe))
}

func recipeResourceNotFoundPayload(recipeID string) map[string]any {
	return map[string]any{
		"status":            "not_found",
		"error":             "recipe not found",
		"recipe_id":         recipeID,
		"available_recipes": recipes.AvailableIDs(),
	}
}

func recipePayload(recipe recipes.Recipe) map[string]any {
	payload := map[string]any{
		"id":               recipe.ID,
		"title":            recipe.Title,
		"description":      recipe.Description,
		"task_class":       recipe.TaskClass,
		"read_only":        recipe.ReadOnly,
		"phases":           recipe.Phases,
		"policy_needs":     recipe.PolicyNeeds,
		"output_resources": recipe.OutputResources,
		"required_args":    recipe.RequiredArgs,
		"gate_default":     recipe.GateDefault,
	}
	if recipe.WorkflowID != "" {
		payload["recipe_workflow_id"] = recipe.WorkflowID
		payload["recipe_workflow_source"] = recipe.WorkflowSource
		payload["recipe_workflow_steps"] = recipe.WorkflowSteps
	}
	return payload
}

func recipeResourceJSON(uri string, payload map[string]any) ([]mcp.ResourceContents, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      uri,
			MIMEType: recipeResourceMIMEType,
			Text:     string(data),
		},
	}, nil
}

func validateRecipeListURI(rawURI string) error {
	u, err := url.Parse(rawURI)
	if err != nil {
		return fmt.Errorf("invalid recipe resource URI")
	}
	if u.Scheme != "aimux" || u.Host != "recipes" || strings.Trim(u.EscapedPath(), "/") != "" {
		return fmt.Errorf("invalid recipe resource URI")
	}
	return nil
}

func parseRecipeDetailURI(rawURI string) (string, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return "", fmt.Errorf("invalid recipe resource URI")
	}
	if u.Scheme != "aimux" || u.Host != "recipes" {
		return "", fmt.Errorf("invalid recipe resource URI")
	}
	segments := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(segments) != 1 || segments[0] == "" {
		return "", fmt.Errorf("invalid recipe resource URI")
	}
	recipeID, err := url.PathUnescape(segments[0])
	if err != nil || strings.TrimSpace(recipeID) == "" {
		return "", fmt.Errorf("invalid recipe resource URI")
	}
	return recipeID, nil
}
