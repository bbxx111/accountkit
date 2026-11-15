package httpcredentials

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name               string
		headers            []string
		scheme, credential string
		ok                 bool
	}{
		{"missing", nil, "", "", false},
		{"case spaces", []string{"bEaReR   abc.def"}, "bearer", "abc.def", true},
		{"basic", []string{"bAsIc   YTpi=="}, "basic", "YTpi==", true},
		{"duplicate", []string{"Bearer abc", "Bearer abc"}, "", "", false},
		{"mixed", []string{"Basic YTpi", "Bearer abc"}, "", "", false},
		{"empty", []string{"Bearer "}, "bearer", "", false},
		{"tab separator", []string{"Bearer\tabc"}, "bearer\tabc", "", false},
		{"tab", []string{"Bearer \tabc"}, "bearer", "", false},
		{"control", []string{"Bearer abc\x7f"}, "bearer", "", false},
		{"trailing space", []string{"Bearer abc "}, "bearer", "", false},
		{"newline", []string{"Bearer abc\n"}, "bearer", "", false},
		{"comma", []string{"Bearer abc,def"}, "bearer", "", false},
		{"embedded", []string{"Bearer abc Bearer def"}, "bearer", "", false},
		{"unicode space", []string{"Bearer abc\u00a0"}, "bearer", "", false},
		{"limit", []string{"Bearer " + strings.Repeat("a", MaxBearerLength)}, "bearer", strings.Repeat("a", MaxBearerLength), true},
		{"over limit", []string{"Bearer " + strings.Repeat("a", MaxBearerLength+1)}, "bearer", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header["Authorization"] = tc.headers
			s, c, ok := Parse(r)
			if s != tc.scheme || c != tc.credential || ok != tc.ok {
				t.Fatalf("scheme=%q credential length=%d ok=%v", s, len(c), ok)
			}
		})
	}
}
