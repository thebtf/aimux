package server

import "github.com/thebtf/aimux/loom"

const specWorkerType = loom.WorkerType("spec")

func registerSpecWorker(s *Server) {
	if s == nil || s.loom == nil {
		return
	}
	s.loom.RegisterWorker(specWorkerType, profileTaskWorker{
		server:        s,
		workerType:    specWorkerType,
		taskClass:     "spec",
		defaultCLI:    "codex",
		forcedSandbox: "read-only",
	})
}
