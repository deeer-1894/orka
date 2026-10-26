package modelsettings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/orka-oss/orka_core/netguard"
)

// Fake providers in these tests listen on loopback, which is exactly what a
// deployment refuses by default; allow it here as an administrator would.
func TestMain(m *testing.M) {
	loopback, _ := netguard.ParseNetworks("127.0.0.0/8,::1")
	Networks.Allowed = loopback
	os.Exit(m.Run())
}

func TestModelEndpointsOnTheInternalNetworkAreRefusedByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer srv.Close()
	saved := Networks.Allowed
	Networks.Allowed = nil
	defer func() { Networks.Allowed = saved }()

	_, err := New(t.TempDir()).DiscoverWithMetadata(context.Background(), "a@x", srv.URL+"/v1", "k")
	if err == nil {
		t.Fatal("a loopback model endpoint must be refused unless the administrator allows it")
	}
	if !errors.Is(err, ErrInternalAddress) {
		t.Fatalf("unexpected error: %v", err)
	}
}
