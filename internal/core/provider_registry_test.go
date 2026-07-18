package core

import (
	"testing"

	"github.com/Podiom/Podiom/internal/config"
)

// TestRegisteredInstructionDeliveriesAreKnown guards the string coupling
// between config.ProviderInfo.InstructionDelivery and core's DeliveryMode
// enum: a registry entry naming an unknown delivery mode would otherwise fail
// only at compose time.
func TestRegisteredInstructionDeliveriesAreKnown(t *testing.T) {
	known := map[DeliveryMode]bool{
		DeliveryClaudeImport: true,
		DeliveryCodexBundle:  true,
	}
	for _, info := range config.Providers() {
		if !known[DeliveryMode(info.InstructionDelivery)] {
			t.Errorf("provider %q: InstructionDelivery %q is not a known core.DeliveryMode", info.ID, info.InstructionDelivery)
		}
	}
}
