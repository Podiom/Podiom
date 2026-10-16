package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadRawSubscription(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantRaw       string
		wantEndpoint  string
		wantErr       error
		wantErrIs     error
		wantTypeError bool
	}{
		{
			name:         "valid subscription",
			body:         `{"endpoint":"https://x","keys":{"p256dh":"p","auth":"a"}}`,
			wantRaw:      `{"endpoint":"https://x","keys":{"p256dh":"p","auth":"a"}}`,
			wantEndpoint: "https://x",
		},
		{
			name:         "unknown fields are removed",
			body:         `{"endpoint":"https://x","expirationTime":null,"keys":{"p256dh":"p","auth":"a"}}`,
			wantRaw:      `{"endpoint":"https://x","keys":{"p256dh":"p","auth":"a"}}`,
			wantEndpoint: "https://x",
		},
		{
			name:         "missing keys",
			body:         `{"endpoint":"https://x"}`,
			wantRaw:      `{"endpoint":"https://x","keys":{"p256dh":"","auth":""}}`,
			wantEndpoint: "https://x",
		},
		{
			name:    "empty endpoint",
			body:    `{"endpoint":"   "}`,
			wantErr: errEmptyEndpoint,
		},
		{
			name:      "empty body",
			body:      "",
			wantErrIs: io.EOF,
		},
		{
			name:          "endpoint has wrong JSON type",
			body:          `{"endpoint":123}`,
			wantTypeError: true,
		},
		{
			name:         "endpoint whitespace is preserved",
			body:         `{"endpoint":"  https://x  "}`,
			wantRaw:      `{"endpoint":"  https://x  ","keys":{"p256dh":"","auth":""}}`,
			wantEndpoint: "  https://x  ",
		},
		{
			name:    "null body",
			body:    `null`,
			wantErr: errEmptyEndpoint,
		},
		{
			name:         "trailing invalid content is ignored",
			body:         `{"endpoint":"https://x"} NOT JSON AT ALL`,
			wantRaw:      `{"endpoint":"https://x","keys":{"p256dh":"","auth":""}}`,
			wantEndpoint: "https://x",
		},
		{
			name:         "second JSON object is ignored",
			body:         `{"endpoint":"https://a"}{"endpoint":"https://b"}`,
			wantRaw:      `{"endpoint":"https://a","keys":{"p256dh":"","auth":""}}`,
			wantEndpoint: "https://a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(
				http.MethodPost,
				"/",
				strings.NewReader(tt.body),
			)

			got, err := readRawSubscription(req)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if tt.wantErrIs != nil {
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("error = %v, want error matching %v", err, tt.wantErrIs)
				}
				return
			}

			if tt.wantTypeError {
				var typeErr *json.UnmarshalTypeError
				if !errors.As(err, &typeErr) {
					t.Fatalf("error = %v, want JSON type error", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("readRawSubscription() error = %v", err)
			}

			if got.raw != tt.wantRaw {
				t.Errorf("raw = %s, want %s", got.raw, tt.wantRaw)
			}

			if got.parsed.Endpoint != tt.wantEndpoint {
				t.Errorf(
					"parsed.Endpoint = %q, want %q",
					got.parsed.Endpoint,
					tt.wantEndpoint,
				)
			}
		})
	}
}
