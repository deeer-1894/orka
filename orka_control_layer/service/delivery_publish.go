package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/orka-oss/orka_core/acceptance"
	"github.com/orka-oss/orka_core/artifacts"
	"github.com/orka-oss/orka_core/delivery"
)

// publishDelivery is the control plane's only snapshot writer. The gateway can
// modify a session workspace but cannot publish or alter accepted deliveries.
func (s *ChatService) publishDelivery(ctx context.Context) error {
	d := deliveryFrom(ctx)
	if d == nil || len(d.snapshot()) == 0 {
		return nil
	}
	scope, ok := ctx.Value(acceptanceScopeKey{}).(acceptanceScope)
	if !ok || scope.runID == "" {
		return nil
	} // isolated callers without a persisted run
	paths := d.snapshot()
	_, err := delivery.PublishChecked(scope.base, scope.owner, scope.conversation, scope.runID, paths, func(files fs.FS) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		report := artifacts.CheckFS(ctx, files, paths)
		if !report.OK {
			return fmt.Errorf("snapshot checks: %s", strings.Join(report.Failures, "; "))
		}
		// Re-evaluate requirements against the copied bytes, including the protected
		// history of prior IDs. Never validate the live files then publish newer ones.
		for _, path := range paths {
			if !strings.HasSuffix(path, ".acceptance.json") {
				continue
			}
			spec, err := acceptance.ReadSpec(files, path)
			if err != nil {
				return err
			}
			if report := checkAcceptanceHistory(ctx, files, path, spec); !report.OK {
				return fmt.Errorf("snapshot acceptance not verified: %s", path)
			}
		}
		return ctx.Err()
	})
	if errors.Is(err, delivery.ErrExists) {
		// Finalization is idempotent after a previously committed snapshot. An
		// incomplete reservation (e.g. process death) is deliberately not a pass.
		all, listErr := delivery.List(scope.base, scope.owner, scope.conversation)
		if listErr != nil {
			return listErr
		}
		for _, m := range all {
			if m.RunID == scope.runID {
				return nil
			}
		}
	}
	return err
}
