package server

import "testing"

func TestFilterSensitive_DropsAPIKey(t *testing.T) {
	env := map[string]string{
		"OPENAI_API_KEY":    "sk-secret",
		"ANTHROPIC_API_KEY": "sk-ant-secret",
		"PATH":              "/usr/bin:/bin",
	}
	got := FilterSensitive(env)
	if _, ok := got["OPENAI_API_KEY"]; ok {
		t.Error("OPENAI_API_KEY should have been filtered")
	}
	if _, ok := got["ANTHROPIC_API_KEY"]; ok {
		t.Error("ANTHROPIC_API_KEY should have been filtered")
	}
	if got["PATH"] != "/usr/bin:/bin" {
		t.Error("PATH should not be filtered")
	}
}

func TestFilterSensitive_DropsToken(t *testing.T) {
	env := map[string]string{
		"GITHUB_TOKEN": "ghp_secret",
		"USER":         "alice",
	}
	got := FilterSensitive(env)
	if _, ok := got["GITHUB_TOKEN"]; ok {
		t.Error("GITHUB_TOKEN should have been filtered")
	}
	if got["USER"] != "alice" {
		t.Error("USER should not be filtered")
	}
}

func TestFilterSensitive_KeepsPATH(t *testing.T) {
	env := map[string]string{
		"PATH": "/usr/local/bin:/usr/bin",
		"HOME": "/home/user",
	}
	got := FilterSensitive(env)
	if got["PATH"] != "/usr/local/bin:/usr/bin" {
		t.Errorf("PATH should be kept, got %v", got)
	}
	if got["HOME"] != "/home/user" {
		t.Error("HOME should be kept")
	}
}

func TestFilterSensitive_KeepsSSH_AUTH_SOCK(t *testing.T) {
	env := map[string]string{
		"SSH_AUTH_SOCK": "/tmp/ssh-agent.sock",
	}
	got := FilterSensitive(env)
	// SSH_AUTH_SOCK does NOT end in _KEY/_TOKEN etc. — it should pass through.
	if got["SSH_AUTH_SOCK"] != "/tmp/ssh-agent.sock" {
		t.Error("SSH_AUTH_SOCK should not be filtered")
	}
}
