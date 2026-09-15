package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/orka-oss/orka_core/delivery"
	"github.com/orka-oss/orka_core/pathsafe"
)

func TestRunDeliveryPublishesOnlyVerifiedSnapshotDependencies(t *testing.T) {
	base := t.TempDir()
	root, err := pathsafe.EnsureSession(base, "owner", "one")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data.csv"), []byte("value\n0.1\n0.2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sum.acceptance.json"), []byte(`{"kind":"orka.acceptance/v1","requirements":[{"id":"sum","method":"csv","file":"data.csv","column":"value","operation":"sum","expected":"0.3"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := newDeliveryTracker(root)
	d.declare([]string{"sum.acceptance.json"})
	ctx := withDelivery(withAcceptance(context.Background(), base, "owner", "one", "run-one"), d)
	svc := &ChatService{}
	if err := svc.publishDelivery(ctx); err == nil {
		t.Fatal("snapshot missing referenced data was accepted")
	}
	if all, err := delivery.List(base, "owner", "one"); err != nil || len(all) != 0 {
		t.Fatal("failed validation published files")
	}
	d.declare([]string{"data.csv"})
	if err := svc.publishDelivery(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data.csv"), []byte("value\n999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := svc.publishDelivery(ctx); err != nil {
		t.Fatal("committed finalization not idempotent", err)
	}
	data, err := delivery.Read(base, "owner", "one", "run-one", "data.csv")
	if err != nil || string(data) != "value\n0.1\n0.2\n" {
		t.Fatal("fixed result changed", err)
	}
}
