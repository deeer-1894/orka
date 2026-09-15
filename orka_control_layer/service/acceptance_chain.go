package service

import (
	"context"
	"errors"
	"strings"
)

// Each checkpoint carries a bounded, explicit lineage. Unrelated executions in
// the same conversation are never discovered by scanning its archive directory.
const maxAcceptanceRuns = 32

func acceptanceRunIDs(ctx context.Context) []string {
	scope, ok := ctx.Value(acceptanceScopeKey{}).(acceptanceScope)
	if !ok || scope.runID == "" {
		return nil
	}
	return append([]string{scope.runID}, scope.inherited...)
}

func validateAcceptanceIDs(ids []string) error {
	if len(ids) > maxAcceptanceRuns {
		return errors.New("acceptance inheritance exceeds 32 runs")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > 128 || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") || seen[id] {
			return errors.New("invalid or cyclic acceptance inheritance")
		}
		seen[id] = true
	}
	return nil
}

// readAcceptance follows only trusted checkpoint/contract references within the
// same owner and conversation. Contract references preserve the lineage for API
// reads after restart; the active checkpoint also protects the pre-contract phase.
func readAcceptance(scope acceptanceScope) (AcceptanceHistory, error) {
	if err := validateAcceptanceIDs(append([]string{scope.runID}, scope.inherited...)); err != nil {
		return AcceptanceHistory{}, err
	}
	visited, active := map[string]bool{}, map[string]bool{}
	lineage := []string{}
	checks := []AcceptanceRecord{}
	var current AcceptanceHistory
	var visit func(string, []string) error
	visit = func(id string, extra []string) error {
		if active[id] {
			return errors.New("cyclic acceptance inheritance")
		}
		if visited[id] {
			return nil
		}
		if len(visited) >= maxAcceptanceRuns {
			return errors.New("acceptance inheritance exceeds 32 runs")
		}
		visited[id], active[id] = true, true
		if id != scope.runID {
			lineage = append(lineage, id)
		}
		one := scope
		one.runID, one.inherited = id, nil
		history, err := readAcceptanceRun(one)
		if err != nil {
			return err
		}
		if history.Contract.RunID != id {
			return errors.New("acceptance contract identity mismatch")
		}
		inherited := history.Contract.InheritedRunIDs
		if err := validateAcceptanceIDs(append([]string{id}, inherited...)); err != nil {
			return err
		}
		if id == scope.runID {
			current = history
		}
		// Both inputs are explicit, protected references; overlaps between a flattened
		// checkpoint and an ancestor's own contract are shared ancestors, not cycles.
		for _, parent := range append(append([]string(nil), extra...), inherited...) {
			if err := visit(parent, nil); err != nil {
				return err
			}
		}
		active[id] = false
		for _, check := range history.Checks {
			if check.RunID != "" && check.RunID != id {
				return errors.New("acceptance record identity mismatch")
			}
			check.RunID = id
			checks = append(checks, check)
		}
		return nil
	}
	if err := visit(scope.runID, scope.inherited); err != nil {
		return AcceptanceHistory{}, err
	}
	current.Contract.InheritedRunIDs = lineage
	current.Checks = checks
	return current, nil
}
