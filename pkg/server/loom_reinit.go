package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/thebtf/aimux/loom"
	loomworkers "github.com/thebtf/aimux/pkg/aimuxworkers"
	codexexec "github.com/thebtf/aimux/pkg/executor/codex"
)

var errLoomStoreUnavailable = errors.New("SQLite session store unavailable")

func isLoomStoreUnavailable(err error) bool {
	return errors.Is(err, errLoomStoreUnavailable)
}

func (s *Server) ensureLoom(ctx context.Context) (*loom.LoomEngine, error) {
	if s == nil {
		return nil, errors.New("server unavailable")
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}

	s.loomMu.Lock()
	defer s.loomMu.Unlock()

	created := false
	if s.loom == nil {
		s.loomRuntimeWired = false
		if err := s.initLoomEngineLocked(); err != nil {
			return nil, err
		}
		created = true
	}
	if created {
		s.wireLoomRuntimeLocked()
	} else if !s.loomRuntimeWired && s.store != nil {
		s.wireLoomRuntimeLocked()
	}
	return s.loom, nil
}

func (s *Server) initLoomEngine() error {
	if s == nil {
		return errors.New("server unavailable")
	}
	s.loomMu.Lock()
	defer s.loomMu.Unlock()
	return s.initLoomEngineLocked()
}

func (s *Server) initLoomEngineLocked() error {
	if s.store == nil || s.store.DB() == nil {
		return errLoomStoreUnavailable
	}
	if s.engineName == "" {
		s.engineName = ResolveEngineName()
	}
	taskStore, err := loom.NewTaskStore(s.store.DB(), s.engineName)
	if err != nil {
		return err
	}
	s.loom = loom.New(taskStore)
	if s.log != nil {
		s.log.Info("LoomEngine initialized (shared SQLite)")
		s.log.Info("loom task scoping: engine_name=%s", s.engineName)
	}
	return nil
}

func (s *Server) wireLoomRuntime() {
	if s == nil {
		return
	}
	s.loomMu.Lock()
	defer s.loomMu.Unlock()
	s.wireLoomRuntimeLocked()
}

func (s *Server) wireLoomRuntimeLocked() {
	if s.loom == nil || s.loomRuntimeWired {
		return
	}
	s.loom.RegisterWorker(loom.WorkerTypeThinker, loomworkers.NewThinkerWorker())
	s.registerTaskWorkers()
	s.registerCodexWorkerLocked()

	if n, err := s.loom.RecoverCrashed(); err != nil {
		if s.log != nil {
			s.log.Warn("loom crash recovery: %v", err)
		}
	} else if n > 0 && s.log != nil {
		s.log.Info("loom: recovered %d crashed tasks", n)
	}
	s.startLoomGCLocked()
	s.loomRuntimeWired = true
}

func (s *Server) registerCodexWorkerLocked() {
	if s.loom == nil {
		return
	}
	if s.codexPool == nil {
		codexPath, pathErr := lookupCodexBinary()
		if pathErr != nil {
			if s.log != nil {
				s.log.Info("codex: binary not found on PATH - generic workers will report unavailable (%v)", pathErr)
			}
			return
		}
		pool, poolErr := codexexec.NewCodexPool(codexPath, codexexec.DefaultPoolConfig())
		if poolErr != nil {
			if s.log != nil {
				s.log.Warn("codex: pool init failed: %v", poolErr)
			}
			return
		}
		s.codexPool = pool
		if s.log != nil {
			s.log.Info("codex: pool initialized (binary: %s)", codexPath)
		}
	}
	worker, workerErr := codexexec.NewCodexWorker(codexexec.CodexWorkerConfig{
		Pool:    s.codexPool,
		Loom:    s.loom,
		LoomGet: s.loom,
	})
	if workerErr != nil {
		if s.log != nil {
			s.log.Warn("codex: worker init failed: %v", workerErr)
		}
		return
	}
	s.loom.RegisterWorker(codexexec.WorkerTypeCodex, worker)
	if s.log != nil {
		s.log.Info("codex: worker initialized")
	}
}

func (s *Server) startLoomGCLocked() {
	if s.gcCtx == nil || s.loomGCInterval <= 0 {
		return
	}
	s.loomGCOnce.Do(func() {
		go s.runLoomGC(s.gcCtx, s.loomGCInterval)
	})
}

func formatLoomUnavailableError(err error) string {
	if err == nil || isLoomStoreUnavailable(err) {
		return taskRouterLoomUnavailableMessage
	}
	return fmt.Sprintf("%s Last reinitialization error: %v", taskRouterLoomUnavailableMessage, err)
}
