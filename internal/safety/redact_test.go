package safety

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactPatterns(t *testing.T) {
	r := NewRedactor(nil, nil)
	cases := map[string]string{
		"Authorization: Bearer abcdef1234567890":                      "Bearer token in a header",
		"authorization=Bearer abcdef1234567890":                       "assignment form",
		"api_key: sk-abcdefghijklmnopqrstuvwxyz":                      "api key assignment",
		"password=hunter2hunter2":                                     "password assignment",
		"?token=abcdef1234567890&x=1":                                 "query parameter",
		"redis://user:supersecret@10.0.0.1:6379":                      "connection string",
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789":           "anthropic key",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghij": "JWT",
		"-----BEGIN RSA PRIVATE KEY-----":                             "private key header",
	}
	for input, why := range cases {
		got := r.Redact(input)
		if !strings.Contains(got, Mask) {
			t.Errorf("%s not redacted: %q → %q", why, input, got)
		}
	}
}

func TestRedactLeavesOrdinaryTextAlone(t *testing.T) {
	r := NewRedactor(nil, nil)
	for _, s := range []string{
		"redis_memory_used_bytes 1024",
		"pod redis-0 restarted 3 times",
		"GET /api/v1/query?query=up",
		"maxmemory-policy allkeys-lru",
	} {
		if got := r.Redact(s); got != s {
			t.Errorf("ordinary text was mangled: %q → %q", s, got)
		}
	}
}

func TestRedactLiterals(t *testing.T) {
	r := NewRedactor(nil, []string{"my-resolved-api-key-value"})
	got := r.Redact("calling with my-resolved-api-key-value now")
	if strings.Contains(got, "my-resolved-api-key-value") {
		t.Fatalf("literal not redacted: %q", got)
	}

	// Very short values are ignored: masking them would destroy output without
	// protecting anything.
	r2 := NewRedactor(nil, []string{"ab"})
	if got := r2.Redact("a table of abc"); !strings.Contains(got, "abc") {
		t.Fatalf("short literal should be ignored, got %q", got)
	}
}

func TestAddLiteralsAtRuntime(t *testing.T) {
	r := NewRedactor(nil, nil)
	const secret = "runtime-discovered-secret"
	if strings.Contains(r.Redact(secret), Mask) {
		t.Fatal("unregistered value was redacted")
	}
	r.AddLiterals(secret)
	if !strings.Contains(r.Redact("x "+secret+" y"), Mask) {
		t.Fatal("literal registered at runtime was not redacted")
	}
}

func TestRedactNestedAny(t *testing.T) {
	r := NewRedactor(nil, []string{"literal-secret-value"})
	in := map[string]any{
		"url":      "http://prom:9090/api/v1/query",
		"api_key":  "anything-at-all",
		"headers":  map[string]string{"Authorization": "Bearer xyz", "Accept": "application/json"},
		"nested":   []any{"literal-secret-value", map[string]any{"password": "p"}},
		"count":    3,
		"Token":    "abc",
		"harmless": "redis_connected_clients 42",
	}
	out := r.RedactAny(in)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	blob := string(b)
	for _, leaked := range []string{"anything-at-all", "literal-secret-value", "Bearer xyz", `"p"`, `"abc"`} {
		if strings.Contains(blob, leaked) {
			t.Errorf("leaked %q in %s", leaked, blob)
		}
	}
	for _, kept := range []string{"application/json", "redis_connected_clients 42", "9090"} {
		if !strings.Contains(blob, kept) {
			t.Errorf("harmless value %q was destroyed: %s", kept, blob)
		}
	}
}

func TestSensitiveKeyDetection(t *testing.T) {
	for _, k := range []string{"api_key", "API-KEY", "token", "Password", "x-api-key", "secret", "Authorization", "cookie"} {
		if !isSensitiveKey(k) {
			t.Errorf("%q should be treated as sensitive", k)
		}
	}
	for _, k := range []string{"url", "query", "instance", "namespace", "tokens_used"} {
		if isSensitiveKey(k) {
			t.Errorf("%q should not be treated as sensitive", k)
		}
	}
}

func TestBadExtraPatternIsSkippedNotFatal(t *testing.T) {
	r := NewRedactor([]string{"([unclosed"}, nil)
	if got := r.Redact("password=abcdefgh"); !strings.Contains(got, Mask) {
		t.Fatalf("a bad operator pattern disabled the default patterns: %q", got)
	}
}

func TestExtraPatternApplies(t *testing.T) {
	r := NewRedactor([]string{`INTERNAL-[0-9]{4}`}, nil)
	if got := r.Redact("ticket INTERNAL-1234 filed"); strings.Contains(got, "INTERNAL-1234") {
		t.Fatalf("extra pattern not applied: %q", got)
	}
}
