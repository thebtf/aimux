package picker

import (
	"context"
	"fmt"

	"github.com/thebtf/aimux/pkg/executor/types"
)

// PickPair selects a cross-family driver and navigator for pair execution.
func (p *Picker) PickPair(ctx context.Context, taskClass string) (driver, navigator types.CLIName, err error) {
	driverStr, err := p.Pick(ctx, TaskSpec{TaskClass: taskClass})
	if err != nil {
		return "", "", types.NewCapabilityMismatch("no healthy CLI available", err)
	}
	driver = types.CLIName(driverStr)

	healthy, reasons := p.filterHealthy()
	if len(healthy) == 0 {
		return "", "", types.NewCapabilityMismatch("no healthy CLI available", &ErrNoHealthyCLI{Reasons: reasons})
	}
	if healthyFamilyCount(healthy) < 2 {
		return "", "", types.NewCapabilityMismatch("cross-family pairing required, only one family available", nil)
	}

	if override := p.pairNavigatorOverride(driver); override != "" {
		nav := types.CLIName(override)
		if sameFamily(driver, nav) {
			return "", "", types.NewCapabilityMismatch(
				fmt.Sprintf("cross-family pairing required, override %s->%s points to same family", driver, nav),
				nil,
			)
		}
		if p.isHealthyActiveNavigator(nav, driver) {
			return driver, nav, nil
		}
	}

	if nav := defaultPairNavigator[driver]; p.isHealthyActiveNavigator(nav, driver) {
		return driver, nav, nil
	}

	for _, candidate := range healthy {
		nav := types.CLIName(candidate)
		if nav != driver && !sameFamily(driver, nav) {
			return driver, nav, nil
		}
	}

	return "", "", types.NewCapabilityMismatch("cross-family pairing required, only one family available", nil)
}

func (p *Picker) pairNavigatorOverride(driver types.CLIName) string {
	if p.cfg.PairNavigator == nil {
		return ""
	}
	return p.cfg.PairNavigator[string(driver)]
}

func (p *Picker) isHealthyActiveNavigator(nav, driver types.CLIName) bool {
	if nav == "" || nav == driver || sameFamily(driver, nav) {
		return false
	}
	if !contains(p.activeCLIs, string(nav)) || p.cfg.isDisabled(string(nav)) {
		return false
	}
	return p.health.IsHealthy(string(nav))
}

func healthyFamilyCount(healthy []string) int {
	families := make(map[string]struct{})
	for _, cli := range healthy {
		if family, ok := FamilyOf(types.CLIName(cli)); ok {
			families[family] = struct{}{}
		}
	}
	return len(families)
}
