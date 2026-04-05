package session_test

import (
	"testing"

	"github.com/thebtf/aimux/pkg/session"
	"github.com/thebtf/aimux/pkg/types"
)

func TestRegistry_CreateAndGet(t *testing.T) {
	reg := session.NewRegistry()

	sess := reg.Create("codex", types.SessionModeLive, "/tmp")
	if sess.ID == "" {
		t.Error("expected non-empty session ID")
	}
	if sess.CLI != "codex" {
		t.Errorf("CLI = %q, want codex", sess.CLI)
	}
	if sess.Mode != types.SessionModeLive {
		t.Errorf("Mode = %q, want live", sess.Mode)
	}
	if sess.Status != types.SessionStatusCreated {
		t.Errorf("Status = %q, want created", sess.Status)
	}

	got := reg.Get(sess.ID)
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.ID != sess.ID {
		t.Errorf("Get ID = %q, want %q", got.ID, sess.ID)
	}
}

func TestRegistry_Update(t *testing.T) {
	reg := session.NewRegistry()
	sess := reg.Create("codex", types.SessionModeOnceStateful, "/tmp")

	ok := reg.Update(sess.ID, func(s *session.Session) {
		s.Status = types.SessionStatusRunning
		s.PID = 12345
	})
	if !ok {
		t.Error("Update returned false")
	}

	got := reg.Get(sess.ID)
	if got.Status != types.SessionStatusRunning {
		t.Errorf("Status = %q, want running", got.Status)
	}
	if got.PID != 12345 {
		t.Errorf("PID = %d, want 12345", got.PID)
	}
}

func TestRegistry_List(t *testing.T) {
	reg := session.NewRegistry()
	reg.Create("codex", types.SessionModeLive, "/tmp")
	s2 := reg.Create("gemini", types.SessionModeOnceStateful, "/tmp")

	reg.Update(s2.ID, func(s *session.Session) {
		s.Status = types.SessionStatusCompleted
	})

	all := reg.List("")
	if len(all) != 2 {
		t.Errorf("List() = %d, want 2", len(all))
	}

	completed := reg.List(types.SessionStatusCompleted)
	if len(completed) != 1 {
		t.Errorf("List(completed) = %d, want 1", len(completed))
	}
}

func TestRegistry_Delete(t *testing.T) {
	reg := session.NewRegistry()
	sess := reg.Create("codex", types.SessionModeLive, "/tmp")

	if !reg.Delete(sess.ID) {
		t.Error("Delete returned false")
	}
	if reg.Get(sess.ID) != nil {
		t.Error("session still exists after delete")
	}
	if reg.Delete(sess.ID) {
		t.Error("double delete should return false")
	}
}

func TestJobManager_Lifecycle(t *testing.T) {
	jm := session.NewJobManager()
	job := jm.Create("session-1", "codex")

	if job.Status != types.JobStatusCreated {
		t.Errorf("initial status = %q, want created", job.Status)
	}

	// Start
	if !jm.StartJob(job.ID, 9999) {
		t.Error("StartJob failed")
	}
	j := jm.Get(job.ID)
	if j.Status != types.JobStatusRunning {
		t.Errorf("after start: status = %q, want running", j.Status)
	}
	if j.PID != 9999 {
		t.Errorf("PID = %d, want 9999", j.PID)
	}

	// Progress
	jm.UpdateProgress(job.ID, "50% complete")
	j = jm.Get(job.ID)
	if j.Progress != "50% complete" {
		t.Errorf("Progress = %q, want '50%% complete'", j.Progress)
	}

	// Complete
	if !jm.CompleteJob(job.ID, "result content", 0) {
		t.Error("CompleteJob failed")
	}
	j = jm.Get(job.ID)
	if j.Status != types.JobStatusCompleted {
		t.Errorf("after complete: status = %q, want completed", j.Status)
	}
	if j.Content != "result content" {
		t.Errorf("Content = %q, want 'result content'", j.Content)
	}
	if j.PID != 0 {
		t.Errorf("PID after complete = %d, want 0", j.PID)
	}
}

func TestJobManager_FailJob(t *testing.T) {
	jm := session.NewJobManager()
	job := jm.Create("session-1", "codex")
	jm.StartJob(job.ID, 100)

	err := types.NewTimeoutError("timed out", "partial")
	if !jm.FailJob(job.ID, err) {
		t.Error("FailJob failed")
	}

	j := jm.Get(job.ID)
	if j.Status != types.JobStatusFailed {
		t.Errorf("status = %q, want failed", j.Status)
	}
	if j.Error == nil {
		t.Error("expected error to be set")
	}
}

func TestJobManager_PollCount(t *testing.T) {
	jm := session.NewJobManager()
	job := jm.Create("session-1", "codex")

	for i := 1; i <= 5; i++ {
		count := jm.IncrementPoll(job.ID)
		if count != i {
			t.Errorf("poll %d: count = %d, want %d", i, count, i)
		}
	}
}

func TestJobManager_Pheromones(t *testing.T) {
	jm := session.NewJobManager()
	job := jm.Create("session-1", "codex")

	jm.SetPheromone(job.ID, "discovery", "found useful pattern in auth.go")
	jm.SetPheromone(job.ID, "repellent", "tried approach X, failed")

	j := jm.Get(job.ID)
	if j.Pheromones["discovery"] != "found useful pattern in auth.go" {
		t.Error("discovery pheromone not set")
	}
	if j.Pheromones["repellent"] != "tried approach X, failed" {
		t.Error("repellent pheromone not set")
	}
}

func TestJobManager_ListBySession(t *testing.T) {
	jm := session.NewJobManager()
	jm.Create("session-1", "codex")
	jm.Create("session-1", "codex")
	jm.Create("session-2", "gemini")

	jobs := jm.ListBySession("session-1")
	if len(jobs) != 2 {
		t.Errorf("ListBySession = %d, want 2", len(jobs))
	}
}

func TestJobManager_InvalidTransitions(t *testing.T) {
	jm := session.NewJobManager()
	job := jm.Create("session-1", "codex")

	// Can't complete a job that hasn't started
	if jm.CompleteJob(job.ID, "content", 0) {
		t.Error("should not complete a created job")
	}

	// Can't start twice
	jm.StartJob(job.ID, 100)
	if jm.StartJob(job.ID, 200) {
		t.Error("should not start a running job")
	}
}
