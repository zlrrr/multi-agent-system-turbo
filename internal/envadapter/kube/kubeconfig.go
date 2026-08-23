// Package kube implements a purpose-built, read-only Kubernetes REST client.
//
// It exists instead of client-go because we call six read endpoints: client-go
// would add roughly 40 MB and a very wide API surface, weakening the argument
// that this tool cannot mutate a cluster (plan.md §6). Every request this client
// can issue is a GET — there is no method capable of any other verb, which makes
// the read-only guarantee structural rather than procedural.
//
// Governs: specs/001-mvp-core/design-lld.md §2.9
package kube

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
	"gopkg.in/yaml.v3"
)

// kubeconfig models the subset of a kubeconfig this client understands.
type kubeconfig struct {
	CurrentContext string `yaml:"current-context"`
	Clusters       []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server                   string `yaml:"server"`
			CertificateAuthority     string `yaml:"certificate-authority"`
			CertificateAuthorityData string `yaml:"certificate-authority-data"`
			InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster   string `yaml:"cluster"`
			User      string `yaml:"user"`
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	} `yaml:"contexts"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Token                 string `yaml:"token"`
			TokenFile             string `yaml:"tokenFile"`
			ClientCertificate     string `yaml:"client-certificate"`
			ClientCertificateData string `yaml:"client-certificate-data"`
			ClientKey             string `yaml:"client-key"`
			ClientKeyData         string `yaml:"client-key-data"`
			Username              string `yaml:"username"`
			Password              string `yaml:"password"`
			Exec                  *struct {
				Command string `yaml:"command"`
			} `yaml:"exec"`
		} `yaml:"user"`
	} `yaml:"users"`
}

// credentials is the resolved connection material for one context.
type credentials struct {
	server        string
	namespace     string
	bearerToken   string
	caPEM         []byte
	clientCertPEM []byte
	clientKeyPEM  []byte
	username      string
	password      string
	insecure      bool
	source        string // how the credentials were obtained, for `mas doctor`
}

// inClusterDir is the standard projected service-account mount.
const inClusterDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// loadInCluster reads the projected service-account credentials.
func loadInCluster() (credentials, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return credentials{}, errs.New("MAS-4202", "in-cluster (no KUBERNETES_SERVICE_HOST)")
	}
	token, err := os.ReadFile(filepath.Join(inClusterDir, "token"))
	if err != nil {
		return credentials{}, errs.Wrap(err, "MAS-4202", "in-cluster (service-account token unreadable)")
	}
	c := credentials{
		server:      "https://" + net_JoinHostPort(host, port),
		bearerToken: strings.TrimSpace(string(token)),
		source:      "in-cluster service account",
	}
	if ca, err := os.ReadFile(filepath.Join(inClusterDir, "ca.crt")); err == nil {
		c.caPEM = ca
	}
	if ns, err := os.ReadFile(filepath.Join(inClusterDir, "namespace")); err == nil {
		c.namespace = strings.TrimSpace(string(ns))
	}
	return c, nil
}

// net_JoinHostPort avoids importing net into this file's dependency set for a
// single string join; the audit test keeps I/O packages out of the reasoning
// layer, and keeping this local documents that intent.
func net_JoinHostPort(host, port string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}

// loadKubeconfig resolves credentials from a kubeconfig file.
//
// Exec credential plugins (`users[].user.exec`) are deliberately unsupported:
// honouring them means executing an arbitrary binary named by a file, which is
// exactly what the deny-by-default command allow-list exists to prevent
// (Constitution Art. IV.2). The client reports this precisely so an operator can
// supply a token instead.
func loadKubeconfig(path, contextName string) (credentials, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-supplied path by design
	if err != nil {
		return credentials{}, errs.Wrap(err, "MAS-4202", "kubeconfig "+path)
	}
	var kc kubeconfig
	if err := yaml.Unmarshal(b, &kc); err != nil {
		return credentials{}, errs.Wrap(err, "MAS-4202", "kubeconfig "+path+" is not valid YAML")
	}

	want := contextName
	if want == "" {
		want = kc.CurrentContext
	}
	if want == "" {
		return credentials{}, errs.New("MAS-4202", "kubeconfig "+path+" has no current-context")
	}

	var clusterName, userName, namespace string
	found := false
	for _, c := range kc.Contexts {
		if c.Name == want {
			clusterName, userName, namespace = c.Context.Cluster, c.Context.User, c.Context.Namespace
			found = true
			break
		}
	}
	if !found {
		return credentials{}, errs.New("MAS-4202", "kubeconfig context "+want+" not found")
	}

	out := credentials{namespace: namespace, source: "kubeconfig " + path + " context " + want}
	for _, c := range kc.Clusters {
		if c.Name != clusterName {
			continue
		}
		out.server = strings.TrimRight(c.Cluster.Server, "/")
		out.insecure = c.Cluster.InsecureSkipTLSVerify
		if c.Cluster.CertificateAuthorityData != "" {
			pem, derr := base64.StdEncoding.DecodeString(c.Cluster.CertificateAuthorityData)
			if derr != nil {
				return credentials{}, errs.Wrap(derr, "MAS-4202", "certificate-authority-data is not base64")
			}
			out.caPEM = pem
		} else if c.Cluster.CertificateAuthority != "" {
			pem, rerr := os.ReadFile(c.Cluster.CertificateAuthority)
			if rerr != nil {
				return credentials{}, errs.Wrap(rerr, "MAS-4202", "certificate-authority file unreadable")
			}
			out.caPEM = pem
		}
	}
	if out.server == "" {
		return credentials{}, errs.New("MAS-4202", "kubeconfig cluster "+clusterName+" has no server")
	}

	for _, u := range kc.Users {
		if u.Name != userName {
			continue
		}
		switch {
		case u.User.Exec != nil:
			return credentials{}, errs.New("MAS-4202",
				"kubeconfig user "+userName+" uses an exec credential plugin, which MAS-Turbo does not run; "+
					"set envs.<name>.token to a read-only service-account token instead")
		case u.User.Token != "":
			out.bearerToken = strings.TrimSpace(u.User.Token)
		case u.User.TokenFile != "":
			tb, rerr := os.ReadFile(u.User.TokenFile)
			if rerr != nil {
				return credentials{}, errs.Wrap(rerr, "MAS-4202", "tokenFile unreadable")
			}
			out.bearerToken = strings.TrimSpace(string(tb))
		}
		if u.User.ClientCertificateData != "" {
			pem, derr := base64.StdEncoding.DecodeString(u.User.ClientCertificateData)
			if derr != nil {
				return credentials{}, errs.Wrap(derr, "MAS-4202", "client-certificate-data is not base64")
			}
			out.clientCertPEM = pem
		} else if u.User.ClientCertificate != "" {
			pem, rerr := os.ReadFile(u.User.ClientCertificate)
			if rerr != nil {
				return credentials{}, errs.Wrap(rerr, "MAS-4202", "client-certificate file unreadable")
			}
			out.clientCertPEM = pem
		}
		if u.User.ClientKeyData != "" {
			pem, derr := base64.StdEncoding.DecodeString(u.User.ClientKeyData)
			if derr != nil {
				return credentials{}, errs.Wrap(derr, "MAS-4202", "client-key-data is not base64")
			}
			out.clientKeyPEM = pem
		} else if u.User.ClientKey != "" {
			pem, rerr := os.ReadFile(u.User.ClientKey)
			if rerr != nil {
				return credentials{}, errs.Wrap(rerr, "MAS-4202", "client-key file unreadable")
			}
			out.clientKeyPEM = pem
		}
		out.username, out.password = u.User.Username, u.User.Password
	}

	if out.bearerToken == "" && out.clientCertPEM == nil && out.username == "" {
		return credentials{}, errs.New("MAS-4202",
			"kubeconfig user "+userName+" carries no usable credential (token, client certificate or basic auth)")
	}
	return out, nil
}

// DefaultKubeconfigPath returns the conventional location.
func DefaultKubeconfigPath() string {
	if p := os.Getenv("KUBECONFIG"); p != "" {
		return strings.Split(p, string(os.PathListSeparator))[0]
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kube", "config")
}

// osReadFile is a seam so client.go reads files without importing os directly,
// keeping file access in one place in this package.
func osReadFile(path string) ([]byte, error) { return os.ReadFile(path) } //nolint:gosec // operator-supplied path
