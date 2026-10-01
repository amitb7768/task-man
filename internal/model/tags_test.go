package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTags(t *testing.T) {
	many := make([]string, 21)
	for i := range many {
		many[i] = "t" + strings.Repeat("x", i)
	}
	cases := []struct {
		name    string
		in      []string
		want    Tags
		wantErr string
	}{
		{"nil", nil, Tags{}, ""},
		{"empty", []string{}, Tags{}, ""},
		{"trim+lower", []string{"  Backend ", "UI"}, Tags{"backend", "ui"}, ""},
		{"drop empties", []string{"", "  ", "a", "\t"}, Tags{"a"}, ""},
		{"dedupe first wins", []string{"b", "A", "a", "B", "c"}, Tags{"b", "a", "c"}, ""},
		{"unicode ok", []string{"Ünïcode", "日本"}, Tags{"ünïcode", "日本"}, ""},
		{"exactly 30 runes", []string{strings.Repeat("é", 30)}, Tags{strings.Repeat("é", 30)}, ""},
		{"20 tags ok", many[:20], Tags(many[:20]), ""},
		{"20 after dedupe ok", append(append([]string{}, many[:20]...), "tx"), Tags(many[:20]), ""},
		{"inner space", []string{"a b"}, nil, `invalid tag "a b"`},
		{"inner tab", []string{"a\tb"}, nil, `invalid tag "a\tb"`},
		{"comma", []string{"a,b"}, nil, `invalid tag "a,b"`},
		{"hash", []string{"#a"}, nil, `invalid tag "#a"`},
		{"31 runes", []string{strings.Repeat("é", 31)}, nil, "longer than 30"},
		{"21 tags", many, nil, "too many tags (max 20)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeTags(c.in)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got == nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestTagsValueScanJSON(t *testing.T) {
	for _, in := range []Tags{nil, {}} {
		v, err := in.Value()
		if err != nil || v != "[]" {
			t.Fatalf("Value(%#v) = %#v, %v; want \"[]\"", in, v, err)
		}
		b, err := json.Marshal(in)
		if err != nil || string(b) != "[]" {
			t.Fatalf("MarshalJSON(%#v) = %s, %v; want []", in, b, err)
		}
	}
	v, _ := Tags{"a", "b"}.Value()
	if v != `["a","b"]` {
		t.Fatalf("Value = %#v", v)
	}
	// Embedded in a struct (the Task/TaskView path) nil still renders [].
	b, _ := json.Marshal(struct {
		Tags Tags `json:"tags"`
	}{})
	if string(b) != `{"tags":[]}` {
		t.Fatalf("struct marshal = %s", b)
	}

	for _, src := range []any{[]byte(`["x","y"]`), `["x","y"]`} {
		var got Tags
		if err := got.Scan(src); err != nil || !reflect.DeepEqual(got, Tags{"x", "y"}) {
			t.Fatalf("Scan(%T) = %#v, %v", src, got, err)
		}
	}
	got := Tags{"stale"}
	if err := got.Scan(nil); err != nil || got != nil {
		t.Fatalf("Scan(nil) = %#v, %v", got, err)
	}
	if err := got.Scan(42); err == nil {
		t.Fatal("Scan(int) must fail")
	}
	if err := got.Scan([]byte(`{}`)); err == nil {
		t.Fatal("Scan(non-array) must fail")
	}
}
