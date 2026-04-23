func (s *Server) handleUpgrade(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	action, err := request.RequireString("action")
	if err != nil {
		return mcp.NewToolResultError("action is required (check or apply)"), nil
	}

	switch action {
	case "check":
		release, checkErr := updater.CheckUpdate(ctx, Version)
		if checkErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("update check failed: %v", checkErr)), nil
		}
		if release == nil {
			return marshalToolResult(map[string]any{
				"status":          "up_to_date",
				"current_version": Version,
			})
		}
		includeContent := request.GetBool("include_content", false)
		releaseNotesLen := len(release.ReleaseNotes)
		payload := map[string]any{
			"status":               "update_available",
			"current_version":      Version,
			"latest_version":       release.Version,
			"asset_name":           release.AssetName,
			"published_at":         release.PublishedAt,
			"release_notes_length": releaseNotesLen,
		}
		if includeContent {
			payload["release_notes"] = release.ReleaseNotes
		} else if releaseNotesLen > 0 {
			payload["truncated"] = true
			payload["hint"] = "release_notes omitted (" + fmt.Sprintf("%d", releaseNotesLen) + " bytes). Use upgrade(action=check, include_content=true) for full body."
		}
		return marshalToolResult(payload)

	case "apply":
		mode := upgrade.Mode(request.GetString("mode", string(upgrade.ModeAuto)))
		if mode != upgrade.ModeAuto && mode != upgrade.ModeHotSwap && mode != upgrade.ModeDeferred {
			return mcp.NewToolResultError(fmt.Sprintf("invalid upgrade mode %q (use auto, hot_swap, or deferred)", mode)), nil
		}

		binaryPath, exeErr := os.Executable()
		if exeErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("locate executable: %v", exeErr)), nil
		}

		h, engineMode := s.sessionHandler.(*aimuxHandler)
		var sh upgrade.SessionHandler
		if engineMode {
			sh = h
		}

		coord := &upgrade.Coordinator{
			Version:         Version,
			BinaryPath:      binaryPath,
			SessionHandler:  sh,
			EngineMode:      engineMode,
			GracefulRestart: upgrade.NewControlSocketGracefulRestartFunc(s.daemonControlSocketPath),
			Logger:          s.log,
		}

		result, applyErr := coord.Apply(ctx, mode)
		if applyErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("update failed: %v", applyErr)), nil
		}

		switch result.Method {
		case "up_to_date":
			return marshalToolResult(map[string]any{
				"status":          "up_to_date",
				"current_version": Version,
			})
		case "hot_swap":
			return marshalToolResult(map[string]any{
				"status":                  "updated_hot_swap",
				"previous_version":        result.PreviousVersion,
				"new_version":             result.NewVersion,
				"handoff_transferred_ids": result.HandoffTransferred,
				"handoff_duration_ms":     result.HandoffDurationMs,
				"message":                 result.Message,
			})
		case "deferred":
			payload := map[string]any{
				"status":           "updated",
				"previous_version": result.PreviousVersion,
				"new_version":      result.NewVersion,
				"message":          result.Message,
			}
			if result.HandoffError != "" {
				payload["handoff_error"] = result.HandoffError
			}
			return marshalToolResult(payload)
		default:
			return marshalToolResult(map[string]any{
				"status":           "updated",
				"previous_version": result.PreviousVersion,
				"new_version":      result.NewVersion,
				"message":          result.Message,
			})
		}

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown upgrade action %q (use check or apply)", action)), nil
	}
}
