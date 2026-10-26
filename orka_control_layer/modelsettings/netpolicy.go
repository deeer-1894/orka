package modelsettings

import (
	"errors"
	"log"
	"net/http"

	"github.com/orka-oss/orka_core/netguard"
)

// Networks is where user-configured model endpoints may connect: the public
// internet, plus what the administrator lists in ORKA_MODEL_ALLOWED_NETWORKS
// (for example a model server on the internal network). Without it, a model
// address is not a way to reach the deployment's own services.
var Networks = loadNetworks()

func loadNetworks() *netguard.Policy {
	p, err := netguard.FromEnv("ORKA_MODEL_ALLOWED_NETWORKS")
	if err != nil {
		log.Printf("[modelsettings] %v; allowing public addresses only", err)
	}
	return &p
}

// ErrInternalAddress explains a refused endpoint to whoever configures it.
var ErrInternalAddress = errors.New("model address is on an internal network; an administrator can allow it with ORKA_MODEL_ALLOWED_NETWORKS")

// Guard confines a transport used for user-configured endpoints to Networks.
func Guard(t http.RoundTripper) http.RoundTripper {
	if ht, ok := t.(*http.Transport); ok {
		return Networks.Guard(ht)
	}
	return t
}
