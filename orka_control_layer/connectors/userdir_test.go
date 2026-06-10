package connectors

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/orka-oss/orka_core/state"
)

func TestCachedDirectory_CacheFirstBatchBackfill(t *testing.T) {
	ctx := context.Background()
	stub := &StubDirectory{}
	dir := NewCachedDirectory(stub, state.NewMemoryStore(), time.Hour)

	emails := make([]string, 100)
	for i := range emails {
		emails[i] = fmt.Sprintf("user%d@x.com", i)
	}

	// first call: all misses -> exactly 100 external resolutions
	got, err := dir.Lookup(ctx, emails)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 || stub.Calls != 100 {
		t.Fatalf("first lookup: got=%d calls=%d", len(got), stub.Calls)
	}
	if got["user7@x.com"].Name != "user7" {
		t.Fatalf("name derivation wrong: %+v", got["user7@x.com"])
	}

	// second call (same 100): all cache hits -> ZERO additional external calls
	if _, err := dir.Lookup(ctx, emails); err != nil {
		t.Fatal(err)
	}
	if stub.Calls != 100 {
		t.Fatalf("second lookup should hit cache; external calls = %d, want 100", stub.Calls)
	}

	// mixed: 100 cached + 10 new -> only the 10 new go to the source
	more := append(append([]string{}, emails...), "new1@x.com", "new2@x.com")
	if _, err := dir.Lookup(ctx, more); err != nil {
		t.Fatal(err)
	}
	if stub.Calls != 102 {
		t.Fatalf("mixed lookup: external calls = %d, want 102", stub.Calls)
	}
}
