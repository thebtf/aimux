package types_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/thebtf/aimux/pkg/types"
)

// TestSessionBindingModeConstantsAreDistinct freezes the provider-neutral
// vocabulary and ensures it cannot collapse into the legacy SessionMode.
func TestSessionBindingModeConstantsAreDistinct(t *testing.T) {
	modes := map[types.SessionBindingMode]string{
		types.SessionBindingModeStateless:   "stateless",
		types.SessionBindingModeNew:         "new",
		types.SessionBindingModeExactResume: "exact_resume",
		types.SessionBindingModeFork:        "fork",
	}
	if len(modes) != 4 {
		t.Fatalf("expected four distinct SessionBindingMode values, got %d", len(modes))
	}
	for mode, want := range modes {
		if string(mode) != want {
			t.Errorf("SessionBindingMode %v: got %q, want %q", mode, mode, want)
		}
	}

	legacyModes := []types.SessionBindingMode{
		types.SessionBindingMode(types.SessionModeLive),
		types.SessionBindingMode(types.SessionModeOnceStateful),
		types.SessionBindingMode(types.SessionModeOnceStateless),
	}
	for _, legacyMode := range legacyModes {
		if _, frozen := modes[legacyMode]; frozen {
			t.Errorf("legacy SessionMode value %q collides with a SessionBindingMode", legacyMode)
		}
	}
}

func completeBindingIdentity() types.SessionBindingIdentity {
	return types.SessionBindingIdentity{
		HandleID:           "handle-17",
		HandleGeneration:   2,
		RegistryGeneration: 3,
		ProviderSession: types.SessionIdentity{
			Provider:   "neutral",
			ID:         "session-17",
			Generation: 1,
		},
	}
}

// TestSessionBindingIdentityValidate rejects every component needed to fence
// a live binding, including incomplete provider identities.
func TestSessionBindingIdentityValidate(t *testing.T) {
	tests := []struct {
		name     string
		identity types.SessionBindingIdentity
		wantErr  bool
	}{
		{name: "complete", identity: completeBindingIdentity()},
		{name: "blank handle ID", identity: func() types.SessionBindingIdentity {
			identity := completeBindingIdentity()
			identity.HandleID = ""
			return identity
		}(), wantErr: true},
		{name: "zero handle generation", identity: func() types.SessionBindingIdentity {
			identity := completeBindingIdentity()
			identity.HandleGeneration = 0
			return identity
		}(), wantErr: true},
		{name: "zero registry generation", identity: func() types.SessionBindingIdentity {
			identity := completeBindingIdentity()
			identity.RegistryGeneration = 0
			return identity
		}(), wantErr: true},
		{name: "blank provider", identity: func() types.SessionBindingIdentity {
			identity := completeBindingIdentity()
			identity.ProviderSession.Provider = ""
			return identity
		}(), wantErr: true},
		{name: "blank provider ID", identity: func() types.SessionBindingIdentity {
			identity := completeBindingIdentity()
			identity.ProviderSession.ID = ""
			return identity
		}(), wantErr: true},
		{name: "zero provider generation", identity: func() types.SessionBindingIdentity {
			identity := completeBindingIdentity()
			identity.ProviderSession.Generation = 0
			return identity
		}(), wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.identity.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, want error: %v", err, test.wantErr)
			}
		})
	}
}

// TestSessionBindingRequestValidateEnforcesModeIdentityRules covers every
// request mode and forbids identities that do not belong to that mode.
func TestSessionBindingRequestValidateEnforcesModeIdentityRules(t *testing.T) {
	expected := completeBindingIdentity()
	parent := completeBindingIdentity()
	parent.HandleID = "parent-handle"
	parent.ProviderSession.ID = "parent-session"

	tests := []struct {
		name    string
		request types.SessionBindingRequest
		wantErr bool
	}{
		{name: "stateless", request: types.SessionBindingRequest{Mode: types.SessionBindingModeStateless}},
		{name: "stateless with expected", request: types.SessionBindingRequest{Mode: types.SessionBindingModeStateless, Expected: &expected}, wantErr: true},
		{name: "stateless with parent", request: types.SessionBindingRequest{Mode: types.SessionBindingModeStateless, Parent: &parent}, wantErr: true},
		{name: "new", request: types.SessionBindingRequest{Mode: types.SessionBindingModeNew}},
		{name: "new with expected", request: types.SessionBindingRequest{Mode: types.SessionBindingModeNew, Expected: &expected}, wantErr: true},
		{name: "new with parent", request: types.SessionBindingRequest{Mode: types.SessionBindingModeNew, Parent: &parent}, wantErr: true},
		{name: "exact resume", request: types.SessionBindingRequest{Mode: types.SessionBindingModeExactResume, Expected: &expected}},
		{name: "exact resume without expected", request: types.SessionBindingRequest{Mode: types.SessionBindingModeExactResume}, wantErr: true},
		{name: "exact resume with invalid expected", request: types.SessionBindingRequest{Mode: types.SessionBindingModeExactResume, Expected: &types.SessionBindingIdentity{}}, wantErr: true},
		{name: "exact resume with parent", request: types.SessionBindingRequest{Mode: types.SessionBindingModeExactResume, Expected: &expected, Parent: &parent}, wantErr: true},
		{name: "fork", request: types.SessionBindingRequest{Mode: types.SessionBindingModeFork, Parent: &parent}},
		{name: "fork without parent", request: types.SessionBindingRequest{Mode: types.SessionBindingModeFork}, wantErr: true},
		{name: "fork with invalid parent", request: types.SessionBindingRequest{Mode: types.SessionBindingModeFork, Parent: &types.SessionBindingIdentity{}}, wantErr: true},
		{name: "fork with expected", request: types.SessionBindingRequest{Mode: types.SessionBindingModeFork, Parent: &parent, Expected: &expected}, wantErr: true},
		{name: "unknown", request: types.SessionBindingRequest{Mode: types.SessionBindingMode("unknown")}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, want error: %v", err, test.wantErr)
			}
		})
	}
}

// TestSessionBindingRequestHasNoCapabilityFields prevents provider-specific
// capability booleans from entering the request contract.
func TestSessionBindingRequestHasNoCapabilityFields(t *testing.T) {
	requestType := reflect.TypeOf(types.SessionBindingRequest{})
	for _, fieldName := range []string{"PersistentSessions", "ForkCapability"} {
		if _, found := requestType.FieldByName(fieldName); found {
			t.Errorf("SessionBindingRequest must not declare %s", fieldName)
		}
	}
}

type testSessionForker struct{}

func (testSessionForker) ForkSession(context.Context, types.SessionIdentity, types.SpawnArgs) (types.Session, error) {
	var session types.Session
	return session, nil
}

// This assertion freezes SessionForker as the optional executor seam, without
// adding a request-level capability flag.
var _ types.SessionForker = testSessionForker{}
