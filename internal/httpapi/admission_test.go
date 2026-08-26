package httpapi_test

import (
	"strings"
	"testing"

	"github.com/zlrrr/multi-agent-system-turbo/internal/config"
	"github.com/zlrrr/multi-agent-system-turbo/internal/httpapi"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

func tokenSet() config.ServerAuth {
	return config.ServerAuth{Tokens: []config.APIToken{
		{Name: "oncall", Token: "t0ken", Scopes: []string{"read", "diagnose"}},
	}}
}

// TestUnauthenticatedPublicBindIsRefused is FR-007. Anything that can reach a
// non-loopback address could list the estate, read stored diagnoses and start
// new ones, and the process must not open that listener.
func TestUnauthenticatedPublicBindIsRefused(t *testing.T) {
	for _, addr := range []string{":8080", "0.0.0.0:8080", "[::]:8080", "10.1.2.3:8080"} {
		err := httpapi.Admit(config.ServerConfig{Addr: addr})
		if err == nil {
			t.Errorf("%s opened a listener with no authentication", addr)
			continue
		}
		if got := errs.CodeOf(err); got != "MAS-7010" {
			t.Errorf("%s: code %s, want MAS-7010", addr, got)
		}
		// The refusal has to name the address, or an operator with several
		// configurations cannot tell which one it means.
		if !strings.Contains(err.Error(), strings.TrimPrefix(addr, "[")) &&
			!strings.Contains(err.Error(), addr) {
			t.Errorf("%s: the refusal does not name the address: %v", addr, err)
		}
	}
}

// TestPlaintextCredentialsOffHostAreRefused is FR-008 and CON-003. Shipping
// bearer auth over plaintext would be building something that looks secure and
// is not.
func TestPlaintextCredentialsOffHostAreRefused(t *testing.T) {
	plaintext := config.ServerConfig{Addr: "0.0.0.0:8080", Auth: tokenSet()}
	err := httpapi.Admit(plaintext)
	if err == nil || errs.CodeOf(err) != "MAS-7011" {
		t.Fatalf("plaintext credentials off-host were admitted: %v", err)
	}

	// The remedy has to name both ways out, because one of them is the only
	// correct answer for the deployment most users have.
	def, ok := errs.Lookup("MAS-7011")
	if !ok {
		t.Fatal("MAS-7011 is not registered")
	}
	for _, want := range []string{"cert_file", "terminated_by_proxy"} {
		if !strings.Contains(def.RemedyEN, want) {
			t.Errorf("the remedy does not mention %s: %q", want, def.RemedyEN)
		}
	}

	// Serving TLS is one way out.
	served := plaintext
	served.TLS = config.ServerTLS{CertFile: "/tls.crt", KeyFile: "/tls.key"}
	if err := httpapi.Admit(served); err != nil {
		t.Errorf("a TLS-serving configuration was refused: %v", err)
	}

	// Declaring a proxy is the other. It records a fact only the operator
	// knows, which is why it has to be typed rather than inferred.
	declared := plaintext
	declared.TLS = config.ServerTLS{TerminatedByProxy: true}
	if err := httpapi.Admit(declared); err != nil {
		t.Errorf("a declared-proxy configuration was refused: %v", err)
	}
}

// TestLoopbackNeedsNoConfiguration is FR-009 and NFR-004: the developer
// workflow, the demo and every existing test keep working untouched.
func TestLoopbackNeedsNoConfiguration(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080", "127.0.0.5:9"} {
		if err := httpapi.Admit(config.ServerConfig{Addr: addr}); err != nil {
			t.Errorf("%s was refused with no configuration: %v", addr, err)
		}
	}

	// And a loopback bind that *does* configure tokens is not then required to
	// serve TLS: the host is still the boundary.
	if err := httpapi.Admit(config.ServerConfig{Addr: "127.0.0.1:8080", Auth: tokenSet()}); err != nil {
		t.Errorf("a loopback bind with tokens was refused: %v", err)
	}
}
