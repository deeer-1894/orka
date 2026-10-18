package browsertool

import "context"

const maxFormFields = 12

// A field is one fill or select, never a submit/click/script. Pointers preserve
// the distinction between an omitted value and an explicit empty input.
type FormField struct {
	Ref        string   `json:"ref,omitempty"`
	SnapshotID string   `json:"snapshot_id,omitempty"`
	Selector   string   `json:"selector,omitempty"`
	Frame      []string `json:"frame,omitempty"`
	Text       *string  `json:"text,omitempty"`
	Value      *string  `json:"value,omitempty"`
}

type FormResult struct {
	Completed   int  `json:"completed"`
	Total       int  `json:"total"`
	FailedIndex *int `json:"failed_index,omitempty"`
}

func (f FormField) request() Request {
	r := Request{Ref: f.Ref, SnapshotID: f.SnapshotID, Selector: f.Selector, Frame: f.Frame}
	if f.Text != nil {
		r.Action = "fill"
		r.Text = *f.Text
	} else if f.Value != nil {
		r.Action = "select"
		r.Value = *f.Value
	}
	return r
}

func fillForm(ctx context.Context, s Session, fields []FormField) (*FormResult, error) {
	out := &FormResult{Total: len(fields)}
	for i, field := range fields {
		if err := act(ctx, s, field.request()); err != nil {
			out.FailedIndex = &i
			return out, err
		}
		out.Completed++
	}
	return out, nil
}
