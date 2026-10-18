// Package reporting binds Markdown placeholders to exact CSV aggregates.
// It validates declared numeric bindings, not arbitrary prose or business rules.
package reporting

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
	"sort"
	"strings"
)

const MaxSpecBytes = 256 << 10
const maxCSVBytes = 4 << 20
const maxTotalBytes = 16 << 20
const maxRows = 50000

var namePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
var placeholder = regexp.MustCompile(`\{\{([A-Za-z][A-Za-z0-9_]{0,63})\}\}`)
var decimalPattern = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?$`)

type Binding struct {
	CSV    string            `json:"csv"`
	Column string            `json:"column,omitempty"`
	Op     string            `json:"op"`
	Where  map[string]string `json:"where,omitempty"`
}
type Spec struct {
	Kind     string             `json:"kind"`
	Output   string             `json:"output"`
	Template string             `json:"template"`
	Bindings map[string]Binding `json:"bindings"`
}
type Result struct {
	Output  string            `json:"output"`
	Content string            `json:"-"`
	Values  map[string]string `json:"values"`
	Sources map[string]string `json:"sources_sha256"`
	SHA256  string            `json:"sha256"`
}

func ReadSpec(fsys fs.FS, path string) ([]byte, error) { return readBounded(fsys, path, MaxSpecBytes) }

// Render reads each referenced CSV once, using only the supplied confined FS.
// All calculations use exact base-10 rationals, never float64 money.
func Render(ctx context.Context, fsys fs.FS, raw []byte) (Result, error) {
	result := Result{Values: map[string]string{}, Sources: map[string]string{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(raw) > MaxSpecBytes {
		return result, fmt.Errorf("report spec exceeds %d bytes", MaxSpecBytes)
	}
	var spec Spec
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return result, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return result, fmt.Errorf("report spec must contain one JSON object")
	}
	if spec.Kind != "orka.report/v1" || !validPath(spec.Output) || !strings.HasSuffix(spec.Output, ".md") {
		return result, fmt.Errorf("expected orka.report/v1 and a workspace-relative .md output")
	}
	if len(spec.Template) == 0 || len(spec.Template) > 128<<10 || len(spec.Bindings) == 0 || len(spec.Bindings) > 128 {
		return result, fmt.Errorf("require a nonempty template <=128 KiB and 1..128 bindings")
	}
	used := map[string]bool{}
	for _, match := range placeholder.FindAllStringSubmatch(spec.Template, -1) {
		used[match[1]] = true
	}
	unbound := placeholder.ReplaceAllString(spec.Template, "")
	at := strings.Index(unbound, "{{")
	if at < 0 {
		at = strings.Index(unbound, "}}")
	}
	if at >= 0 {
		snippet := []rune(strings.SplitN(unbound[at:], "\n", 2)[0])
		if len(snippet) > 80 {
			snippet = snippet[:80]
		}
		return result, fmt.Errorf("invalid placeholder near line %d: %q; use {{binding_name}} or remove literal double-brace examples", 1+strings.Count(unbound[:at], "\n"), string(snippet))
	}
	for key := range used {
		if _, ok := spec.Bindings[key]; !ok {
			return result, fmt.Errorf("missing binding %q", key)
		}
	}
	names := make([]string, 0, len(spec.Bindings))
	for key := range spec.Bindings {
		if !namePattern.MatchString(key) || !used[key] {
			return result, fmt.Errorf("invalid or unused binding %q", key)
		}
		names = append(names, key)
	}
	sort.Strings(names)
	tables := map[string]*table{}
	totalBytes := 0
	for _, key := range names {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		binding := spec.Bindings[key]
		if !validPath(binding.CSV) || binding.CSV == spec.Output {
			return result, fmt.Errorf("binding %s: invalid or overwritten CSV source", key)
		}
		data := tables[binding.CSV]
		if data == nil {
			if len(tables) >= 16 {
				return result, fmt.Errorf("at most 16 CSV sources")
			}
			body, err := readBounded(fsys, binding.CSV, maxCSVBytes)
			if err != nil {
				return result, fmt.Errorf("binding %s: %w", key, err)
			}
			totalBytes += len(body)
			if totalBytes > maxTotalBytes {
				return result, fmt.Errorf("CSV sources exceed 16 MiB total")
			}
			data, err = parseCSV(ctx, body)
			if err != nil {
				return result, fmt.Errorf("binding %s: %w", key, err)
			}
			tables[binding.CSV] = data
			result.Sources[binding.CSV] = digest(body)
		}
		value, err := aggregate(ctx, data, binding)
		if err != nil {
			return result, fmt.Errorf("binding %s: %w", key, err)
		}
		result.Values[key] = value
	}
	result.Output = spec.Output
	result.Content = placeholder.ReplaceAllStringFunc(spec.Template, func(s string) string { return result.Values[s[2:len(s)-2]] })
	if len(result.Content) > 1<<20 {
		return Result{}, fmt.Errorf("rendered report exceeds 1 MiB")
	}
	result.SHA256 = digest([]byte(result.Content))
	return result, ctx.Err()
}

// Check compares a fresh render with the actual output. A changed bound value
// or a manual report edit cannot silently retain a passing report check. Undeclared prose remains
// outside this contract, including any literal numbers in the template.
func Check(ctx context.Context, fsys fs.FS, raw []byte) error {
	result, err := Render(ctx, fsys, raw)
	if err != nil {
		return err
	}
	actual, err := readBounded(fsys, result.Output, 1<<20)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, []byte(result.Content)) {
		return fmt.Errorf("stale or edited report %s; rerun render_report from its .report.json spec", result.Output)
	}
	return ctx.Err()
}

func validPath(path string) bool {
	return path != "." && fs.ValidPath(path) && !strings.ContainsAny(path, "\\\x00")
}
func readBounded(fsys fs.FS, path string, limit int) ([]byte, error) {
	if !validPath(path) {
		return nil, fmt.Errorf("invalid workspace-relative path %q", path)
	}
	f, err := fsys.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > int64(limit) {
		return nil, fmt.Errorf("%s must be a regular file <=%d bytes", path, limit)
	}
	body, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if len(body) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	return body, err
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

type table struct {
	columns map[string]int
	rows    [][]string
}

func parseCSV(ctx context.Context, body []byte) (*table, error) {
	r := csv.NewReader(bytes.NewReader(body))
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	if len(header) > 128 {
		return nil, fmt.Errorf("at most 128 CSV columns")
	}
	t := &table{columns: map[string]int{}}
	for i, name := range header {
		if name == "" {
			return nil, fmt.Errorf("empty CSV column")
		}
		if _, ok := t.columns[name]; ok {
			return nil, fmt.Errorf("duplicate CSV column %q", name)
		}
		t.columns[name] = i
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(t.rows) >= maxRows {
			return nil, fmt.Errorf("CSV exceeds %d rows", maxRows)
		}
		t.rows = append(t.rows, row)
	}
	return t, nil
}
func aggregate(ctx context.Context, t *table, b Binding) (string, error) {
	switch b.Op {
	case "count", "sum", "min", "max":
	default:
		return "", fmt.Errorf("op must be count, sum, min or max")
	}
	column, ok := t.columns[b.Column]
	if b.Op != "count" && !ok {
		return "", fmt.Errorf("unknown column %q", b.Column)
	}
	if b.Op == "count" && b.Column != "" {
		return "", fmt.Errorf("count counts rows; omit column")
	}
	for key := range b.Where {
		if _, ok := t.columns[key]; !ok {
			return "", fmt.Errorf("unknown filter column %q", key)
		}
	}
	count, scale := 0, 0
	value := new(big.Rat)
	for _, row := range t.rows {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		match := true
		for key, want := range b.Where {
			if row[t.columns[key]] != want {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		count++
		if b.Op == "count" {
			continue
		}
		raw := row[column]
		if len(raw) > 80 || !decimalPattern.MatchString(raw) {
			return "", fmt.Errorf("non-decimal value in column %s", b.Column)
		}
		decimals := 0
		if at := strings.IndexByte(raw, '.'); at >= 0 {
			decimals = len(raw) - at - 1
		}
		if decimals > 18 {
			return "", fmt.Errorf("at most 18 decimal places")
		}
		scale = max(scale, decimals)
		n, ok := new(big.Rat).SetString(raw)
		if !ok {
			return "", fmt.Errorf("invalid decimal")
		}
		switch b.Op {
		case "sum":
			value.Add(value, n)
		case "min":
			if count == 1 || n.Cmp(value) < 0 {
				value.Set(n)
			}
		case "max":
			if count == 1 || n.Cmp(value) > 0 {
				value.Set(n)
			}
		}
	}
	if b.Op == "count" {
		return fmt.Sprint(count), nil
	}
	if count == 0 && b.Op != "sum" {
		return "", fmt.Errorf("%s has no matching rows", b.Op)
	}
	text := value.FloatString(scale)
	if scale > 0 {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	return text, nil
}
