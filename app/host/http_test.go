package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPKeyEndpoints(t *testing.T) {
	app := buildFiberApp(nil, map[string]struct{}{"allowed": {}}, map[string]tz4CacheEntry{
		"allowed": {publicKey: "BLpk-test", pop: "BLsig-test"},
		"hidden":  {publicKey: "BLpk-hidden", pop: "BLsig-hidden"},
	})
	for _, tc := range []struct {
		method, path, body string
		status             int
		field, value       string
	}{
		{"GET", "/authorized_keys", "", 200, "", ""},
		{"GET", "/keys/allowed", "", 200, "public_key", "BLpk-test"},
		{"GET", "/bls_prove_possession/allowed", "", 200, "bls_prove_possession", "BLsig-test"},
		{"GET", "/keys/hidden", "", 404, "error", "key not found"},
		{"GET", "/bls_prove_possession/hidden", "", 404, "error", "key not found"},
		{"POST", "/keys/hidden", `"00"`, 404, "error", "key not found"},
		{"POST", "/keys/allowed", `"zz"`, 400, "error", "bad payload_hex:"},
		{"POST", "/keys/allowed", `{"payload":"00"}`, 400, "error", ""},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			var body map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if tc.field == "" {
				if len(body) != 0 {
					t.Fatalf("authorized_keys must be empty: %v", body)
				}
			} else if value, ok := body[tc.field]; !ok || !strings.HasPrefix(value, tc.value) {
				t.Fatalf("response = %v, want %s starting with %q", body, tc.field, tc.value)
			}
		})
	}
}
