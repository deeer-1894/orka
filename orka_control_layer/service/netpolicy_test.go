package service

import (
	"github.com/orka-oss/orka_control_layer/modelsettings"
	"github.com/orka-oss/orka_core/netguard"
)

// Fake model providers in these tests listen on loopback; allow it as an
// administrator would with ORKA_MODEL_ALLOWED_NETWORKS.
func init() {
	loopback, _ := netguard.ParseNetworks("127.0.0.0/8,::1")
	modelsettings.Networks.Allowed = loopback
}
