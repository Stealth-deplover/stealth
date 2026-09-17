package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

// projectHostPreflight copies only display-safe host check results into the
// encrypted setup state. The setup API can project these checks to the browser
// without running Docker or resource probes inside its temporary container.
func projectHostPreflight(checks []SystemCheck) []setupstate.HostPreflightCheck {
	projected := make([]setupstate.HostPreflightCheck, 0, len(checks))
	for _, check := range checks {
		name := strings.TrimSpace(check.Name)
		if name == "" {
			continue
		}
		detail := strings.TrimSpace(check.Detail)
		if len(detail) > 240 {
			detail = detail[:240]
		}
		projected = append(projected, setupstate.HostPreflightCheck{Name: name, Detail: detail, OK: check.OK, Required: check.Required})
	}
	return projected
}

func (a *App) persistHostPreflight(values map[string]string, checks []SystemCheck) error {
	store, err := a.setupStateStore(values)
	if err != nil {
		return err
	}
	projected := projectHostPreflight(checks)
	if len(projected) == 0 {
		return fmt.Errorf("host preflight produced no checks")
	}
	_, err = store.Update(context.Background(), func(state *setupstate.State) error {
		if setupstate.InstallationLocked(*state) {
			return nil
		}
		state.HostPreflight = projected
		return nil
	})
	return err
}
