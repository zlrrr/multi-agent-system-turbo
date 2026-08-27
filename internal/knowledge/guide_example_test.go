package knowledge_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/expr-lang/expr"

	"github.com/zlrrr/multi-agent-system-turbo/internal/knowledge"
	"github.com/zlrrr/multi-agent-system-turbo/pkg/errs"
)

// The authoring guide ends with a pack it claims "loads, validates and runs".
// Documentation that claims something the code no longer supports is worse than
// no documentation, so the claim is a test.
const guideExampleMarker = "examplewaredb"

var yamlBlock = regexp.MustCompile("(?s)```yaml\n(.*?)```")

func guideExample(t *testing.T, lang string) string {
	t.Helper()
	path := filepath.Join("..", "..", "docs", lang, "knowledge-packs.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the pack-authoring guide is missing: %v", err)
	}
	for _, m := range yamlBlock.FindAllStringSubmatch(string(b), -1) {
		if strings.Contains(m[1], guideExampleMarker) {
			return m[1]
		}
	}
	t.Fatalf("%s contains no complete example pack (no yaml block naming %q)", path, guideExampleMarker)
	return ""
}

func TestGuideExamplePackLoads(t *testing.T) {
	for _, lang := range []string{"en", "zh"} {
		t.Run(lang, func(t *testing.T) {
			pack, err := knowledge.Parse([]byte(guideExample(t, lang)), "docs/"+lang+"/knowledge-packs.md")
			if err != nil {
				t.Fatalf("the guide's example pack does not load: %v", err)
			}
			if err := pack.Validate(); err != nil {
				t.Fatalf("the guide's example pack does not validate: %v", err)
			}

			// The guide tells authors their expressions must compile; its own
			// example has to hold up to that.
			for _, pb := range pack.Playbooks {
				env := exprEnv(pb)
				for _, st := range pb.Steps {
					for label, code := range map[string]string{
						"evaluate":      st.Evaluate,
						"conclude.when": concludeWhen(st),
					} {
						if strings.TrimSpace(code) == "" {
							continue
						}
						if _, err := expr.Compile(code, expr.Env(env), expr.AsBool()); err != nil {
							t.Errorf("%s/%s %s does not compile: %v", pb.ID, st.ID, label, err)
						}
					}
				}
			}
		})
	}
}

// TestGuideExamplesMatchAcrossLanguages keeps the two guides from drifting where
// it matters most: a reader following the Chinese guide must get the same pack
// as one following the English guide (Constitution Art. III).
func TestGuideExamplesMatchAcrossLanguages(t *testing.T) {
	if en, zh := guideExample(t, "en"), guideExample(t, "zh"); en != zh {
		t.Error("the example pack differs between the English and Chinese guides")
	}
}

// TestOverridingAShippedPackIsAllowed and TestTwoLocalPacksCollide pin the two
// halves of the override rule the user manual and the authoring guide both
// state: replacing built-in knowledge is a supported workflow, while two local
// packs claiming one id is an ambiguity the loader must report rather than
// resolve by directory order.
func TestOverridingAShippedPackIsAllowed(t *testing.T) {
	dir := t.TempDir()
	writePack(t, filepath.Join(dir, "redis.yaml"), overridePack)

	lib, err := knowledge.LoadDefault([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Problems()) != 0 {
		t.Fatalf("overriding a shipped pack was reported as a problem: %v", lib.Problems())
	}
	for _, p := range lib.All() {
		if p.ID() == "redis/redis-core" && p.Metadata.Version != "9.9.9" {
			t.Errorf("the local pack did not replace the shipped one: version %s", p.Metadata.Version)
		}
	}
}

func TestTwoLocalPacksCollide(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writePack(t, filepath.Join(a, "one.yaml"), overridePack)
	writePack(t, filepath.Join(b, "two.yaml"), overridePack)

	lib, err := knowledge.LoadDefault([]string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range lib.Problems() {
		if errs.CodeOf(p) == "MAS-5002" {
			found = true
		}
	}
	if !found {
		t.Fatalf("two local packs with one id were resolved silently by directory order: %v",
			lib.Problems())
	}
}

func writePack(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// overridePack claims the shipped Redis pack's id with a different version.
const overridePack = `apiVersion: mas.turbo/v1
kind: KnowledgePack
metadata:
  middleware: redis
  name: redis-core
  version: 9.9.9
  versionRange: ">=6.0"
signals:
  - id: up
    promql: 'up{{.selector}}'
    unit: bool
    description:
      en: "Reachability."
      zh: "可达性。"
playbooks:
  - id: redis.local
    title:
      en: "Local override"
      zh: "本地覆盖"
    steps:
      - id: collect
        collect:
          tool: promql.range
          args: { query: "{{signal:up}}" }
          as: up
      - id: eval
        evaluate: "up.empty or up.latest < 1"
        onTrue:
          finding:
            severity: critical
            confidence: 0.9
            statement:
              en: "Unreachable."
              zh: "不可达。"
`
