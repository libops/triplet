package contracts

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var pythonInvocation = regexp.MustCompile(`(^|[[:space:];|&(!])("[^"]*/python([0-9.]*)?"|'[^']*/python([0-9.]*)?'|[^[:space:];|&()'"]*python([0-9.]*))[[:space:]]+(.*)$`)

func TestShellScriptsDoNotInlinePython(t *testing.T) {
	t.Parallel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))

	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".cache", ".git", "node_modules", "results", "site", "vendor":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(path) != ".sh" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, violation := range inlinePythonLines(string(content)) {
			violations = append(violations, fmt.Sprintf("%s:%s", strings.TrimPrefix(path, root+string(filepath.Separator)), violation))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("shell scripts may invoke packaged Python tools but may not use Python -c, stdin, or heredocs:\n%s", strings.Join(violations, "\n"))
	}
}

func TestInlinePythonLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		script     string
		violations int
	}{
		{name: "packaged script", script: `python3 scripts/report.py input.csv`, violations: 0},
		{name: "module", script: `"$VENV_DIR/bin/python" -m pip install package`, violations: 0},
		{name: "command string", script: `python3 -c 'print(1)'`, violations: 1},
		{name: "attached command string", script: `python3 -cprint(1)`, violations: 1},
		{name: "tabbed command string", script: "python3\t-c\t'print(1)'", violations: 1},
		{name: "standard input", script: `python3 - "$file"`, violations: 1},
		{name: "heredoc", script: "python3 script.py <<'PY'\nvalue = 1\nPY", violations: 1},
		{name: "continued", script: "python3 \\\n  -c 'print(1)'", violations: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := len(inlinePythonLines(test.script)); got != test.violations {
				t.Fatalf("inlinePythonLines() found %d violations, want %d", got, test.violations)
			}
		})
	}
}

func TestConformanceOwnsAllTemporaryFiles(t *testing.T) {
	t.Parallel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract test source")
	}
	scriptPath := filepath.Join(filepath.Dir(source), "..", "..", "scripts", "conformance.sh")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	script := string(content)
	if strings.Contains(script, "${TMPDIR}") || strings.Contains(script, "$TMPDIR/") {
		t.Fatal("conformance script must not depend on an inherited TMPDIR")
	}
	for _, name := range []string{"write-annotations.json", "write-annotations-updated.json"} {
		want := "${CONFORMANCE_TMP_DIR}/" + name
		if !strings.Contains(script, want) {
			t.Fatalf("conformance temporary artifact %q must be stored in the owned temporary directory", name)
		}
	}
}

func TestComposePassesPresentationWriteEnvironment(t *testing.T) {
	t.Parallel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	composeBody, err := os.ReadFile(filepath.Join(root, "deploy", "compose", "docker-compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var compose struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
			Volumes     []string          `yaml:"volumes"`
			User        string            `yaml:"user"`
			Command     []string          `yaml:"command"`
			CapAdd      []string          `yaml:"cap_add"`
			DependsOn   map[string]struct {
				Condition string `yaml:"condition"`
			} `yaml:"depends_on"`
		} `yaml:"services"`
		Volumes map[string]any `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(composeBody, &compose); err != nil {
		t.Fatalf("parse Compose file: %v", err)
	}
	environment := compose.Services["triplet"].Environment
	for _, name := range []string{"TRIPLET_PRESENTATION_WRITE_ENABLED", "TRIPLET_PRESENTATION_WRITE_TOKEN"} {
		if _, ok := environment[name]; !ok {
			t.Fatalf("Triplet Compose service must pass %s into the config-expansion environment", name)
		}
	}
	tripletService := compose.Services["triplet"]
	mounts := tripletService.Volumes
	initService, ok := compose.Services["triplet-volume-init"]
	if !ok {
		t.Fatal("Compose must initialize writable volume ownership before Triplet starts")
	}
	if initService.User != "0:0" || !containsExact(initService.CapAdd, "CHOWN") {
		t.Fatal("volume initializer must run with only the ownership capability it needs")
	}
	if len(initService.Command) == 0 || initService.Command[0] != "triplet:triplet" {
		t.Fatal("volume initializer must assign writable roots to the rootless Triplet UID and GID")
	}
	dependency, ok := tripletService.DependsOn["triplet-volume-init"]
	if !ok || dependency.Condition != "service_completed_successfully" {
		t.Fatal("Triplet must wait for successful writable-volume initialization")
	}
	for _, name := range []string{"presentation", "cache", "source-cache"} {
		if _, ok := compose.Volumes[name]; !ok {
			t.Fatalf("Compose must declare the rootless-writable %s named volume", name)
		}
		want := name + ":/var/lib/triplet/" + name
		if !containsExact(mounts, want) {
			t.Fatalf("Triplet Compose service must mount %q", want)
		}
		if !containsExact(initService.Volumes, want) {
			t.Fatalf("volume initializer must mount %q", want)
		}
		if !containsExact(initService.Command, "/var/lib/triplet/"+name) {
			t.Fatalf("volume initializer must assign ownership of /var/lib/triplet/%s", name)
		}
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfileText := string(dockerfile)
	for _, path := range []string{"/var/lib/triplet/presentation", "/var/lib/triplet/cache", "/var/lib/triplet/source-cache"} {
		if !strings.Contains(dockerfileText, path) {
			t.Fatalf("runtime image must pre-create %s before Docker initializes its named volume", path)
		}
	}
	if !strings.Contains(dockerfileText, "chown -R triplet:triplet /var/lib/triplet") {
		t.Fatal("runtime image must make named-volume roots writable by the rootless Triplet user")
	}
}

func TestRuntimeSeccompAllowsConfiguredFileStores(t *testing.T) {
	t.Parallel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract test source")
	}
	profileBody, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "deploy", "seccomp-triplet.json"))
	if err != nil {
		t.Fatal(err)
	}
	var profile struct {
		DefaultAction string `json:"defaultAction"`
		Syscalls      []struct {
			Names  []string `json:"names"`
			Action string   `json:"action"`
		} `json:"syscalls"`
	}
	if err := json.Unmarshal(profileBody, &profile); err != nil {
		t.Fatalf("parse seccomp profile: %v", err)
	}
	if profile.DefaultAction != "SCMP_ACT_ERRNO" {
		t.Fatalf("seccomp default action = %q, want SCMP_ACT_ERRNO", profile.DefaultAction)
	}
	allowed := make(map[string]bool)
	for _, rule := range profile.Syscalls {
		if rule.Action != "SCMP_ACT_ALLOW" {
			continue
		}
		for _, name := range rule.Names {
			allowed[name] = true
		}
	}
	// The configured filesystem Presentation and cache stores create shard
	// directories and commit durable files atomically. Keep this list explicit
	// so the deny-by-default profile cannot silently turn configured writes into
	// HTTP 500 responses.
	for _, name := range []string{"fchmod", "fsync", "mkdirat", "renameat", "unlinkat"} {
		if !allowed[name] {
			t.Errorf("seccomp profile blocks required file-store syscall %q", name)
		}
	}
}

func containsExact(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func inlinePythonLines(script string) []string {
	// Join shell continuations so splitting a forbidden flag onto the next line
	// cannot evade the contract check.
	script = strings.ReplaceAll(script, "\\\r\n", " ")
	script = strings.ReplaceAll(script, "\\\n", " ")
	lines := strings.Split(script, "\n")
	var violations []string
	for index, line := range lines {
		match := pythonInvocation.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		arguments := strings.TrimSpace(match[len(match)-1])
		fields := strings.Fields(arguments)
		if len(fields) == 0 {
			continue
		}
		first := fields[0]
		if first == "-" || strings.HasPrefix(first, "-c") || strings.Contains(arguments, "<<") {
			violations = append(violations, fmt.Sprintf("%d: %s", index+1, strings.TrimSpace(line)))
		}
	}
	return violations
}
