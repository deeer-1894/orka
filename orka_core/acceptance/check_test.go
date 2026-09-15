package acceptance

import (
	"context"
	"testing"
	"testing/fstest"
)

func TestAcceptanceSeparatesEvidenceFromUnverifiedClaims(t *testing.T) {
	files := fstest.MapFS{"summary.csv": {Data: []byte("region,revenue\nNorth,30\nSouth,20\nALL,50\n")}}
	spec := Spec{Kind: "orka.acceptance/v1", Requirements: []Requirement{
		{ID: "range", Description: "regional maximum", Method: "csv", File: "summary.csv", Column: "revenue", Operation: "max", Exclude: map[string]string{"region": "ALL"}, Compare: "eq", Expected: "30"},
		{ID: "review", Description: "sources support conclusions", Method: "manual"},
	}}
	report := Check(context.Background(), files, spec)
	if report.OK || report.Results[0].Status != "passed" || report.Results[1].Status != "unverified" {
		t.Fatalf("%+v", report)
	}
	spec.Requirements[0].Expected = "50"
	report = Check(context.Background(), files, spec)
	if report.Results[0].Status != "failed" {
		t.Fatal("wrong business expectation accepted")
	}
	files["summary.csv"] = &fstest.MapFile{Data: []byte("region,revenue\nNorth,20\nSouth,35\nALL,55\n")}
	spec.Requirements[0].Expected = "35"
	if got := Check(context.Background(), files, spec); got.Results[0].Status != "passed" {
		t.Fatalf("range tied to fixture rank: %+v", got)
	}
}
func TestAcceptanceRejectsDuplicateAndUnsafeRequirements(t *testing.T) {
	for _, reqs := range [][]Requirement{{{ID: "x", Method: "contains", File: "../secret", Expected: "x"}}, {{ID: "x", Method: "manual"}, {ID: "x", Method: "manual"}}} {
		if got := Check(context.Background(), fstest.MapFS{}, Spec{Kind: "orka.acceptance/v1", Requirements: reqs}); got.OK || got.Error == "" {
			t.Fatalf("invalid accepted: %+v", got)
		}
	}
}

func TestAcceptanceNumericPrecisionAndResourceBounds(t *testing.T) {
	files := fstest.MapFS{"data.csv": {Data: []byte("value\n0.1\n0.2\n")}}
	req := Requirement{ID: "sum", Method: "csv", File: "data.csv", Column: "value", Operation: "sum", Expected: "0.3"}
	spec := Spec{Kind: Kind, Requirements: []Requirement{req}}
	if r := Check(context.Background(), files, spec); !r.OK {
		t.Fatalf("inexact decimal sum: %+v", r)
	}
	for _, bad := range []string{"1e999999999", "1/99999999999999999999999999999999999999999"} {
		spec.Requirements[0].Expected = bad
		if err := Validate(spec); err == nil {
			t.Fatal("unbounded input accepted")
		}
	}
	if _, err := DecodeSpec([]byte(`{"kind":"orka.acceptance/v1","requirements":[],"unknown":true}`)); err == nil {
		t.Fatal("unknown contract field accepted")
	}
}
