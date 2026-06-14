package server

import (
	"fmt"
	"strings"

	"github.com/thebtf/aimux/pkg/config"
	extypes "github.com/thebtf/aimux/pkg/executor/types"
	"github.com/thebtf/aimux/pkg/recipes"
	"github.com/thebtf/aimux/pkg/server/classifier"
)

const (
	recipePolicyEnforcedMetadataKey = "recipe_policy_enforced"
	recipePolicySelectedCLIMetadata = "recipe_policy_selected_cli"
	recipePolicyRequestedMetadata   = "recipe_policy_requested"
	recipePolicySupportedMetadata   = "recipe_policy_supported"
)

func (s *Server) validateRecipeProviderPolicy(req TaskRequest) error {
	recipeID, ok := metadataString(req.Metadata, "recipe_id")
	if !ok || strings.TrimSpace(recipeID) == "" {
		return nil
	}
	recipe, ok := recipes.Resolve(recipeID)
	if !ok {
		return newUnsupportedRecipeIDError(recipeID)
	}
	selectedCLI := selectedRecipePolicyCLI(req)
	profile, err := s.recipePolicyProfile(selectedCLI)
	if err != nil {
		result := recipes.PolicyValidationResult{
			OK:                    false,
			Retryable:             false,
			RecipeID:              recipe.ID,
			SelectedCLI:           selectedCLI,
			RequestedPolicy:       recipe.PolicyNeeds,
			MissingCapabilities:   []string{"profile." + selectedCLI},
			SupportedCapabilities: []string{},
		}
		return newTaskRecipePolicyError(result, err)
	}
	result := recipes.ValidatePolicy(recipe, providerCapabilitiesFromProfile(selectedCLI, req.TaskClass, profile))
	if !result.OK {
		return newTaskRecipePolicyError(result, nil)
	}
	req.Metadata[recipePolicyEnforcedMetadataKey] = true
	req.Metadata[recipePolicySelectedCLIMetadata] = result.SelectedCLI
	req.Metadata[recipePolicyRequestedMetadata] = result.RequestedPolicy
	req.Metadata[recipePolicySupportedMetadata] = result.SupportedCapabilities
	return nil
}

func (s *Server) recipePolicyProfile(selectedCLI string) (*config.CLIProfile, error) {
	if s == nil || s.registry == nil {
		return nil, fmt.Errorf("server registry unavailable")
	}
	profile, err := s.registry.Get(selectedCLI)
	if err != nil || profile == nil {
		if err == nil {
			err = fmt.Errorf("profile is nil")
		}
		return nil, err
	}
	return profile, nil
}

func selectedRecipePolicyCLI(req TaskRequest) string {
	switch req.TaskClass {
	case classifier.TaskClassReview:
		return "codex"
	case classifier.TaskClassCode:
		if strings.TrimSpace(req.CLI) != "" {
			return strings.TrimSpace(req.CLI)
		}
		return "codex"
	default:
		if strings.TrimSpace(req.CLI) != "" {
			return strings.TrimSpace(req.CLI)
		}
		return "codex"
	}
}

func providerCapabilitiesFromProfile(selectedCLI string, taskClass string, profile *config.CLIProfile) recipes.ProviderCapabilities {
	if profile == nil {
		return recipes.ProviderCapabilities{SelectedCLI: selectedCLI, TaskClass: taskClass}
	}
	readOnly := profile.Features.ReadOnly || len(profile.ReadOnlyFlags) > 0
	sandbox := make([]string, 0, 2)
	if readOnly {
		sandbox = append(sandbox, "read-only")
	}
	if len(profile.WriteSandboxFlags) > 0 {
		sandbox = append(sandbox, "workspace-write")
	}
	return recipes.ProviderCapabilities{
		SelectedCLI:      selectedCLI,
		TaskClass:        taskClass,
		OutputFormat:     profile.OutputFormat,
		ReadOnly:         readOnly,
		SupportedSandbox: sandbox,
	}
}

type taskRecipePolicyError struct {
	err    *extypes.CLIError
	result recipes.PolicyValidationResult
}

func newTaskRecipePolicyError(result recipes.PolicyValidationResult, cause error) error {
	err := extypes.NewCapabilityMismatch("task: recipe policy cannot be enforced by selected provider", cause)
	err.Retryable = false
	return &taskRecipePolicyError{err: err, result: result}
}

func (e *taskRecipePolicyError) Error() string {
	if e == nil || e.err == nil {
		return "task: recipe policy error"
	}
	return e.err.Error()
}

func (e *taskRecipePolicyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *taskRecipePolicyError) Result() recipes.PolicyValidationResult {
	if e == nil {
		return recipes.PolicyValidationResult{}
	}
	result := e.result
	result.RequestedPolicy = cloneRecipeStrings(result.RequestedPolicy)
	result.MissingCapabilities = cloneRecipeStrings(result.MissingCapabilities)
	result.SupportedCapabilities = cloneRecipeStrings(result.SupportedCapabilities)
	return result
}
