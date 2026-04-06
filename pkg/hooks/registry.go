package hooks

import (
	"log"
	"sync"
	"time"
)

const defaultTimeout = 5 * time.Second
const maxPromptPreview = 500

// HookEntry holds common hook metadata.
type HookEntry struct {
	Name    string
	Timeout time.Duration
}

type beforeEntry struct {
	HookEntry
	Handler BeforeHookFn
}

type afterEntry struct {
	HookEntry
	Handler AfterHookFn
}

// Registry manages before and after execution hooks.
type Registry struct {
	mu          sync.RWMutex
	beforeHooks []beforeEntry
	afterHooks  []afterEntry
}

// NewRegistry creates a new hook registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// RegisterBefore adds a before-execution hook. Optional timeout overrides the default 5s.
func (r *Registry) RegisterBefore(name string, handler BeforeHookFn, timeout ...time.Duration) {
	t := defaultTimeout
	if len(timeout) > 0 && timeout[0] > 0 {
		t = timeout[0]
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.beforeHooks = append(r.beforeHooks, beforeEntry{
		HookEntry: HookEntry{Name: name, Timeout: t},
		Handler:   handler,
	})
}

// RegisterAfter adds an after-execution hook. Optional timeout overrides the default 5s.
func (r *Registry) RegisterAfter(name string, handler AfterHookFn, timeout ...time.Duration) {
	t := defaultTimeout
	if len(timeout) > 0 && timeout[0] > 0 {
		t = timeout[0]
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.afterHooks = append(r.afterHooks, afterEntry{
		HookEntry: HookEntry{Name: name, Timeout: t},
		Handler:   handler,
	})
}

// Remove removes all hooks (before and after) with the given name.
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	filtered := r.beforeHooks[:0]
	for _, h := range r.beforeHooks {
		if h.Name != name {
			filtered = append(filtered, h)
		}
	}
	r.beforeHooks = filtered

	filteredAfter := r.afterHooks[:0]
	for _, h := range r.afterHooks {
		if h.Name != name {
			filteredAfter = append(filteredAfter, h)
		}
	}
	r.afterHooks = filteredAfter
}

// RunBefore executes all before hooks sequentially with timeout and panic protection.
func (r *Registry) RunBefore(ctx BeforeHookContext) BeforeHookResult {
	r.mu.RLock()
	hooks := make([]beforeEntry, len(r.beforeHooks))
	copy(hooks, r.beforeHooks)
	r.mu.RUnlock()

	// Truncate prompt preview
	if len(ctx.PromptPreview) > maxPromptPreview {
		ctx.PromptPreview = ctx.PromptPreview[:maxPromptPreview]
	}

	final := BeforeHookResult{Action: "proceed"}

	for _, h := range hooks {
		// Defensive copy of context metadata
		cloned := ctx
		if ctx.Metadata != nil {
			cloned.Metadata = make(map[string]string, len(ctx.Metadata))
			for k, v := range ctx.Metadata {
				cloned.Metadata[k] = v
			}
		}

		result, ok := runBeforeWithTimeout(h.Handler, cloned, h.Timeout)
		if !ok {
			log.Printf("[aimux:hooks] before hook %q timed out or panicked, skipping", h.Name)
			continue
		}

		switch result.Action {
		case "block":
			return result
		case "skip":
			return result
		case "proceed":
			if result.ModifiedPrompt != "" {
				final.ModifiedPrompt = result.ModifiedPrompt
			}
			if result.Metadata != nil {
				if final.Metadata == nil {
					final.Metadata = make(map[string]string)
				}
				for k, v := range result.Metadata {
					final.Metadata[k] = v
				}
			}
		}
	}

	return final
}

// RunAfter executes all after hooks sequentially with timeout and panic protection.
func (r *Registry) RunAfter(ctx AfterHookContext) AfterHookResult {
	r.mu.RLock()
	hooks := make([]afterEntry, len(r.afterHooks))
	copy(hooks, r.afterHooks)
	r.mu.RUnlock()

	final := AfterHookResult{Action: "accept"}

	for _, h := range hooks {
		result, ok := runAfterWithTimeout(h.Handler, ctx, h.Timeout)
		if !ok {
			log.Printf("[aimux:hooks] after hook %q timed out or panicked, skipping", h.Name)
			continue
		}

		switch result.Action {
		case "reject":
			return result
		case "annotate":
			if result.Annotations != nil {
				if final.Annotations == nil {
					final.Annotations = make(map[string]string)
				}
				for k, v := range result.Annotations {
					final.Annotations[k] = v
				}
			}
		}
	}

	return final
}

func runBeforeWithTimeout(fn BeforeHookFn, ctx BeforeHookContext, timeout time.Duration) (result BeforeHookResult, ok bool) {
	type outcome struct {
		result BeforeHookResult
		ok     bool
	}
	ch := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[aimux:hooks] before hook panicked: %v", r)
				ch <- outcome{ok: false}
			}
		}()
		ch <- outcome{result: fn(ctx), ok: true}
	}()

	select {
	case o := <-ch:
		return o.result, o.ok
	case <-time.After(timeout):
		return BeforeHookResult{}, false
	}
}

func runAfterWithTimeout(fn AfterHookFn, ctx AfterHookContext, timeout time.Duration) (result AfterHookResult, ok bool) {
	type outcome struct {
		result AfterHookResult
		ok     bool
	}
	ch := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[aimux:hooks] after hook panicked: %v", r)
				ch <- outcome{ok: false}
			}
		}()
		ch <- outcome{result: fn(ctx), ok: true}
	}()

	select {
	case o := <-ch:
		return o.result, o.ok
	case <-time.After(timeout):
		return AfterHookResult{}, false
	}
}
