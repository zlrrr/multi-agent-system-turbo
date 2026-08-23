package errs

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// TestRegistryUnique proves no code is allocated twice (LLD §2.1 invariant).
func TestRegistryUnique(t *testing.T) {
	seen := map[string]string{}
	for _, d := range definitions {
		if prev, ok := seen[d.Code]; ok {
			t.Errorf("code %s allocated twice: %s and %s", d.Code, prev, d.Symbol)
		}
		seen[d.Code] = d.Symbol
	}
	symbols := map[string]string{}
	for _, d := range definitions {
		if prev, ok := symbols[d.Symbol]; ok {
			t.Errorf("symbol %s reused by %s and %s", d.Symbol, prev, d.Code)
		}
		symbols[d.Symbol] = d.Code
	}
}

// TestAllCodesRegistered checks every code is well-formed and resolvable.
func TestAllCodesRegistered(t *testing.T) {
	for _, d := range definitions {
		if !codePattern.MatchString(d.Code) {
			t.Errorf("malformed code %q", d.Code)
		}
		if _, ok := Lookup(d.Code); !ok {
			t.Errorf("code %s is not in the lookup map", d.Code)
		}
		if Domain(d.Code) == "unknown" {
			t.Errorf("code %s falls outside every allocation block", d.Code)
		}
	}
	if len(All()) != len(definitions) {
		t.Fatalf("All() returned %d, want %d", len(All()), len(definitions))
	}
}

// TestBilingualComplete enforces Constitution Art. III on the code registry:
// both languages present, and the same fmt verb sequence so one argument list
// renders both.
func TestBilingualComplete(t *testing.T) {
	verbs := regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)
	for _, d := range definitions {
		for name, v := range map[string]string{
			"MessageEN": d.MessageEN, "MessageZH": d.MessageZH,
			"RemedyEN": d.RemedyEN, "RemedyZH": d.RemedyZH,
			"Symbol": d.Symbol, "Severity": string(d.Severity),
		} {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s: %s is empty", d.Code, name)
			}
		}
		en, zh := verbs.FindAllString(d.MessageEN, -1), verbs.FindAllString(d.MessageZH, -1)
		if len(en) != len(zh) {
			t.Errorf("%s: verb count differs EN=%v ZH=%v", d.Code, en, zh)
			continue
		}
		for i := range en {
			if en[i] != zh[i] {
				t.Errorf("%s: verb %d differs EN=%s ZH=%s", d.Code, i, en[i], zh[i])
			}
		}
	}
}

func TestSeverityIsKnown(t *testing.T) {
	for _, d := range definitions {
		switch d.Severity {
		case SeverityError, SeverityWarn, SeverityInfo:
		default:
			t.Errorf("%s: unknown severity %q", d.Code, d.Severity)
		}
	}
}

func TestCodeOfThroughWrap(t *testing.T) {
	base := New("MAS-4001", "primary", "connection refused")
	wrapped := fmt.Errorf("collect metrics: %w", base)
	deeper := fmt.Errorf("phase 1: %w", wrapped)

	if got := CodeOf(deeper); got != "MAS-4001" {
		t.Fatalf("CodeOf = %q, want MAS-4001", got)
	}
	if !Is(deeper, "MAS-4001") {
		t.Fatal("Is should match through two wraps")
	}
	if Is(deeper, "MAS-4002") {
		t.Fatal("Is matched the wrong code")
	}
	if got := CodeOf(errors.New("plain")); got != "" {
		t.Fatalf("CodeOf(plain) = %q, want empty", got)
	}
}

func TestWrapPreservesCause(t *testing.T) {
	cause := errors.New("dial tcp: refused")
	e := Wrap(cause, "MAS-4001", "primary", cause.Error())
	if !errors.Is(e, cause) {
		t.Fatal("wrapped cause is not reachable via errors.Is")
	}
	if !strings.Contains(e.Error(), "MAS-4001") || !strings.Contains(e.Error(), "dial tcp") {
		t.Fatalf("Error() = %q, want code and cause", e.Error())
	}
}

func TestMessageRendersBothLanguages(t *testing.T) {
	e := New("MAS-1005", "redis-prod")
	if !strings.Contains(e.Message("en"), "redis-prod") {
		t.Errorf("EN message missing argument: %q", e.Message("en"))
	}
	if !strings.Contains(e.Message("zh"), "redis-prod") {
		t.Errorf("ZH message missing argument: %q", e.Message("zh"))
	}
	if e.Message("zh") == e.Message("en") {
		t.Error("ZH and EN messages are identical; translation missing")
	}
	if e.Remedy("zh") == "" || e.Remedy("en") == "" {
		t.Error("remedy missing in one language")
	}
}

func TestUnregisteredCodePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New with an unregistered code must panic")
		}
	}()
	_ = New("MAS-9999")
}

func TestLogAttrsCarryCode(t *testing.T) {
	e := New("MAS-8001", "redis-cli FLUSHALL").With("tool", "local.inspect")
	attrs := e.LogAttrs()
	joined := fmt.Sprint(attrs...)
	for _, want := range []string{"MAS-8001", "MutatingRefused", "local.inspect"} {
		if !strings.Contains(joined, want) {
			t.Errorf("LogAttrs missing %q: %v", want, attrs)
		}
	}
}

func TestAllIsSorted(t *testing.T) {
	all := All()
	for i := 1; i < len(all); i++ {
		if all[i-1].Code >= all[i].Code {
			t.Fatalf("All() not sorted at %d: %s >= %s", i, all[i-1].Code, all[i].Code)
		}
	}
}
