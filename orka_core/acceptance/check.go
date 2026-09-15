// Package acceptance evaluates explicit, bounded artifact assertions. It never
// treats a model-authored claim or a valid file format as proof of the full task.
package acceptance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"regexp"
	"strings"
)

const Kind = "orka.acceptance/v1"
const maxBytes = 4 << 20

type Spec struct {
	Kind         string        `json:"kind"`
	Requirements []Requirement `json:"requirements"`
}
type Requirement struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Method      string            `json:"method"` // contains, csv, manual
	File        string            `json:"file,omitempty"`
	Column      string            `json:"column,omitempty"`
	Operation   string            `json:"operation,omitempty"` // count, min, max, sum
	Where       map[string]string `json:"where,omitempty"`
	Exclude     map[string]string `json:"exclude,omitempty"`
	Compare     string            `json:"compare,omitempty"` // eq, gte, lte
	Expected    string            `json:"expected,omitempty"`
}
type Result struct {
	Requirement
	Status string `json:"status"`
	Actual string `json:"actual,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Detail string `json:"detail,omitempty"`
}
type Report struct {
	OK      bool     `json:"ok"`
	Scope   string   `json:"scope"`
	Error   string   `json:"error,omitempty"`
	Results []Result `json:"results"`
}

func ReadSpec(files fs.FS, path string) (Spec, error) {
	b, err := read(files, path)
	if err != nil {
		return Spec{}, err
	}
	return DecodeSpec(b)
}
func DecodeSpec(b []byte) (Spec, error) {
	if len(b) > maxBytes {
		return Spec{}, fmt.Errorf("acceptance spec exceeds limit")
	}
	var spec Spec
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return Spec{}, err
	}
	if dec.Decode(new(any)) != io.EOF {
		return Spec{}, fmt.Errorf("trailing acceptance data")
	}
	return spec, nil
}
func Validate(spec Spec) error {
	if spec.Kind != Kind || len(spec.Requirements) == 0 || len(spec.Requirements) > 128 {
		return fmt.Errorf("acceptance kind and 1–128 requirements are required")
	}
	seen := map[string]bool{}
	for _, r := range spec.Requirements {
		if r.ID == "" || len(r.ID) > 100 || seen[r.ID] {
			return fmt.Errorf("requirement IDs must be nonempty and unique")
		}
		seen[r.ID] = true
		if len(r.Description) > 2000 || len(r.Expected) > 16384 {
			return fmt.Errorf("requirement text exceeds limit")
		}
		switch r.Method {
		case "manual":
			continue
		case "contains", "csv":
		default:
			return fmt.Errorf("unsupported verification method %q", r.Method)
		}
		if !fs.ValidPath(r.File) || r.File == "." || strings.Contains(r.File, "\\") {
			return fmt.Errorf("invalid evidence path")
		}
		if r.Method == "contains" && r.Expected == "" {
			return fmt.Errorf("contains assertion requires expected text")
		}
		if r.Method == "csv" {
			if r.Operation != "count" && r.Operation != "min" && r.Operation != "max" && r.Operation != "sum" {
				return fmt.Errorf("invalid CSV operation")
			}
			if r.Compare != "" && r.Compare != "eq" && r.Compare != "gte" && r.Compare != "lte" {
				return fmt.Errorf("invalid comparison")
			}
			if _, ok := parseNumber(r.Expected); !ok {
				return fmt.Errorf("invalid expected number")
			}
		}
	}
	return nil
}
func Check(ctx context.Context, files fs.FS, spec Spec) Report {
	report := Report{OK: true, Scope: "Checks cover declared artifact assertions only. Requirement completeness, source support and manual judgments are not automatically verified.", Results: []Result{}}
	if err := Validate(spec); err != nil {
		report.OK = false
		report.Error = err.Error()
		return report
	}
	for _, req := range spec.Requirements {
		result := Result{Requirement: req, Status: "failed"}
		if req.Method == "manual" {
			result.Status = "unverified"
			result.Detail = "Requires independent human review; no automatic pass is recorded."
		} else if err := ctx.Err(); err != nil {
			result.Detail = err.Error()
		} else {
			body, err := read(files, req.File)
			if err != nil {
				result.Detail = err.Error()
			} else {
				digest := sha256.Sum256(body)
				result.SHA256 = hex.EncodeToString(digest[:])
				if req.Method == "contains" {
					if bytes.Contains(body, []byte(req.Expected)) {
						result.Status = "passed"
					} else {
						result.Detail = "expected text absent"
					}
				} else {
					actual, err := aggregate(ctx, body, req)
					if err != nil {
						result.Detail = err.Error()
					} else {
						result.Actual = actual.RatString()
						expected, _ := parseNumber(req.Expected)
						cmp := actual.Cmp(expected)
						passed := cmp == 0
						if req.Compare == "gte" {
							passed = cmp >= 0
						}
						if req.Compare == "lte" {
							passed = cmp <= 0
						}
						if passed {
							result.Status = "passed"
						} else {
							result.Detail = "numeric assertion failed"
						}
					}
				}
			}
		}
		if result.Status != "passed" {
			report.OK = false
		}
		report.Results = append(report.Results, result)
	}
	return report
}
func read(files fs.FS, path string) ([]byte, error) {
	if !fs.ValidPath(path) || path == "." || strings.Contains(path, "\\") {
		return nil, fmt.Errorf("invalid evidence path")
	}
	f, err := files.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("evidence must be a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBytes {
		return nil, fmt.Errorf("evidence exceeds 4 MiB")
	}
	return b, nil
}
func aggregate(ctx context.Context, body []byte, r Requirement) (*big.Rat, error) {
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})))
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	columns := map[string]int{}
	for i, name := range header {
		if _, exists := columns[name]; name == "" || exists {
			return nil, fmt.Errorf("duplicate or empty CSV header")
		}
		columns[name] = i
	}
	for _, filter := range []map[string]string{r.Where, r.Exclude} {
		for key := range filter {
			if _, ok := columns[key]; !ok {
				return nil, fmt.Errorf("unknown filter column %q", key)
			}
		}
	}
	index, exists := columns[r.Column]
	if r.Operation != "count" && !exists {
		return nil, fmt.Errorf("unknown numeric column")
	}
	value := new(big.Rat)
	count := int64(0)
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		match := true
		for key, want := range r.Where {
			if row[columns[key]] != want {
				match = false
			}
		}
		for key, exclude := range r.Exclude {
			if row[columns[key]] == exclude {
				match = false
			}
		}
		if !match {
			continue
		}
		count++
		if r.Operation == "count" {
			continue
		}
		number, ok := parseNumber(strings.TrimSpace(row[index]))
		if !ok {
			return nil, fmt.Errorf("non-numeric value in %s", r.Column)
		}
		switch r.Operation {
		case "sum":
			value.Add(value, number)
		case "min":
			if count == 1 || number.Cmp(value) < 0 {
				value.Set(number)
			}
		case "max":
			if count == 1 || number.Cmp(value) > 0 {
				value.Set(number)
			}
		}
	}
	if r.Operation == "count" {
		return value.SetInt64(count), nil
	}
	if count == 0 {
		return nil, fmt.Errorf("no matching rows")
	}
	return value, nil
}

// Decimal/scientific inputs have bounded precision and exponent. Arbitrary
// fractions would let repeated sums grow unbounded common denominators.
var decimalNumber = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]{1,2})?$`)

func parseNumber(s string) (*big.Rat, bool) {
	if len(s) > 128 || !decimalNumber.MatchString(s) {
		return nil, false
	}
	return new(big.Rat).SetString(s)
}
