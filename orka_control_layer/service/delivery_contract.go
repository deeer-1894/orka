package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/orka-oss/orka_core/artifacts"
)

// deliveryTracker owns additive file requirements independently of the mutable
// execution checklist. It never accepts a model-supplied verification result.
type deliveryTracker struct {
	mu      sync.Mutex
	root    string
	outputs []string
}

func newDeliveryTracker(root string) *deliveryTracker { return &deliveryTracker{root: root} }
func (d *deliveryTracker) declare(paths []string) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	next := append([]string(nil), d.outputs...)
	seen := map[string]bool{}
	for _, p := range next {
		seen[p] = true
	}
	for _, p := range paths {
		if !artifacts.ValidPath(p) {
			return fmt.Errorf("invalid workspace-relative output path %q", p)
		}
		if !seen[p] {
			next = append(next, p)
			seen[p] = true
		}
	}
	if len(next) > artifacts.MaxFiles {
		return fmt.Errorf("at most %d required output files", artifacts.MaxFiles)
	}
	d.outputs = next
	return nil
}
func (d *deliveryTracker) snapshot() []string {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.outputs...)
}
func (d *deliveryTracker) failures(ctx context.Context) []string {
	if d == nil {
		return nil
	}
	paths := d.snapshot()
	if len(paths) == 0 {
		return nil
	}
	return artifacts.Check(ctx, d.root, paths).Failures
}

type deliveryCheckTool struct{}

func (deliveryCheckTool) Name() string { return "check_delivery" }
func (deliveryCheckTool) Description() string {
	return "Check every required output declared through update_plan.outputs. Returns structured failures and hashes from actual files: nonempty files, JSON/CSV/SVG structure, HTML local resources and ZIP integrity. Run after generation, repair failures before finishing. This does not verify business formulas, citation support, manifest semantics or completeness of your declared requirements; run independent task-specific tests too."
}
func (deliveryCheckTool) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (deliveryCheckTool) Invoke(ctx context.Context, _ map[string]any) (string, error) {
	d := deliveryFrom(ctx)
	if d == nil {
		return "Delivery inspection unavailable: workspace not configured.", nil
	}
	report := artifacts.Check(ctx, d.root, d.snapshot())
	b, err := json.Marshal(report)
	return string(b), err
}

type deliveryKey struct{}

func withDelivery(ctx context.Context, d *deliveryTracker) context.Context {
	return context.WithValue(ctx, deliveryKey{}, d)
}
func deliveryFrom(ctx context.Context) *deliveryTracker {
	d, _ := ctx.Value(deliveryKey{}).(*deliveryTracker)
	return d
}
