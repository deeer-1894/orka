package reporting

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

func renderFixture(t *testing.T, body string, bindings map[string]Binding, template string) (Result, error) {
	t.Helper()
	raw, err := json.Marshal(Spec{Kind: "orka.report/v1", Output: "report.md", Template: template, Bindings: bindings})
	if err != nil {
		t.Fatal(err)
	}
	return Render(context.Background(), fstest.MapFS{"data.csv": {Data: []byte(body)}}, raw)
}
func TestExactDecimalAggregationAndFilters(t *testing.T) {
	b := map[string]Binding{"total": {CSV: "data.csv", Column: "amount", Op: "sum"}, "low": {CSV: "data.csv", Column: "amount", Op: "min"}, "high": {CSV: "data.csv", Column: "amount", Op: "max"}, "paid": {CSV: "data.csv", Op: "count", Where: map[string]string{"status": "paid"}}, "zero": {CSV: "data.csv", Column: "amount", Op: "sum", Where: map[string]string{"status": "missing"}}}
	got, err := renderFixture(t, "status,amount\npaid,9007199254740993.01\npaid,0.09\nvoid,-0.10\n", b, "{{total}};{{low}};{{high}};{{paid}};{{zero}};{{total}}")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "9007199254740993;-0.1;9007199254740993.01;2;0;9007199254740993" {
		t.Fatal(got.Content)
	}
	if len(got.Sources) != 1 || len(got.SHA256) != 64 {
		t.Fatal("missing provenance")
	}
}
func TestLeadingZerosRemainDecimal(t *testing.T) {
	got, err := renderFixture(t, "n\n010\n0018\n", map[string]Binding{"n": {CSV: "data.csv", Column: "n", Op: "sum"}}, "{{n}}")
	if err != nil || got.Content != "28" {
		t.Fatalf("%q %v", got.Content, err)
	}
}
func TestRejectInvalidBindingsAndData(t *testing.T) {
	for _, tc := range []struct {
		name, body, template string
		b                    Binding
	}{
		{"missing column", "n\n1\n", "{{v}}", Binding{CSV: "data.csv", Column: "typo", Op: "min"}},
		{"missing filter", "n\n1\n", "{{v}}", Binding{CSV: "data.csv", Op: "count", Where: map[string]string{"typo": "1"}}},
		{"empty min", "n\n", "{{v}}", Binding{CSV: "data.csv", Column: "n", Op: "min"}},
		{"NaN", "n\nNaN\n", "{{v}}", Binding{CSV: "data.csv", Column: "n", Op: "sum"}},
		{"exponent", "n\n1e3\n", "{{v}}", Binding{CSV: "data.csv", Column: "n", Op: "sum"}},
		{"duplicate header", "n,n\n1,2\n", "{{v}}", Binding{CSV: "data.csv", Column: "n", Op: "min"}},
		{"unequal row", "n\n1,2\n", "{{v}}", Binding{CSV: "data.csv", Column: "n", Op: "min"}},
		{"unknown operation", "n\n1\n", "{{v}}", Binding{CSV: "data.csv", Column: "n", Op: "average"}},
		{"missing placeholder", "n\n1\n", "{{absent}}", Binding{CSV: "data.csv", Column: "n", Op: "min"}},
		{"invalid placeholder", "n\n1\n", "{{ v }}", Binding{CSV: "data.csv", Column: "n", Op: "min"}},
		{"unused binding", "n\n1\n", "literal", Binding{CSV: "data.csv", Column: "n", Op: "min"}},
		{"escape", "n\n1\n", "{{v}}", Binding{CSV: "../data.csv", Column: "n", Op: "min"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := renderFixture(t, tc.body, map[string]Binding{"v": tc.b}, tc.template); err == nil {
				t.Fatal("invalid report accepted")
			}
		})
	}
}
func TestResourceBoundsAndCancellation(t *testing.T) {
	raw := []byte(`{"kind":"orka.report/v1","output":"report.md","template":"{{n}}","bindings":{"n":{"csv":"data.csv","op":"count"}}}`)
	fsys := fstest.MapFS{"data.csv": {Data: []byte("n\n" + strings.Repeat("1\n", maxRows+1))}}
	if _, err := Render(context.Background(), fsys, raw); err == nil {
		t.Fatal("row limit not enforced")
	}
	fsys["data.csv"] = &fstest.MapFile{Data: []byte(strings.Repeat("n", maxCSVBytes+1))}
	if _, err := Render(context.Background(), fsys, raw); err == nil {
		t.Fatal("byte limit not enforced")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Render(ctx, fsys, raw); err != context.Canceled {
		t.Fatalf("cancel=%v", err)
	}
	if _, err := Render(context.Background(), fsys, append(raw, []byte(`{}`)...)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestPlaceholderErrorLocatesLiteralExample(t *testing.T) {
	_, err := renderFixture(t, "n\n18\n", map[string]Binding{"n": {CSV: "data.csv", Column: "n", Op: "min"}}, "结果 {{n}}\n说明：所有 {{...}} 已替换")
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "{{...}}") {
		t.Fatalf("error cannot locate offending example: %v", err)
	}
}
