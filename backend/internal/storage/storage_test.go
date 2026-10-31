package storage

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// Without a Content-Length (chunked), the size is checked while reading.
func TestExactReader(t *testing.T) {
	for _, tc := range []struct {
		body string
		size int64
		err  error
	}{
		{"ciphertext", 10, nil},
		{"ciphertext", 11, errSize},
		{"ciphertext", 9, errSize},
		{"", 0, nil},
	} {
		r := &exactReader{r: io.NopCloser(strings.NewReader(tc.body)), left: tc.size}
		_, err := io.ReadAll(r)
		if !errors.Is(err, tc.err) && err != tc.err {
			t.Errorf("%q as %d bytes: %v, want %v", tc.body, tc.size, err, tc.err)
		}
	}
}
