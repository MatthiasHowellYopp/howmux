package kirocli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestListModels_ParsesJSON(t *testing.T) {
	dir := fakeCLI(t, `printf '%s\n' "$*" > "$(dirname "$0")/argv.txt"
cat <<'EOF'
{"models":[{"model_name":"auto","model_id":"auto"},{"model_name":"claude-sonnet-5.5","model_id":"claude-sonnet-5.5"},{"model_id":""}]}
EOF
`)
	got, err := ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if want := []string{"auto", "claude-sonnet-5.5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ListModels = %v, want %v", got, want)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv.txt"))
	if got := strings.TrimSpace(string(argv)); got != "chat --list-models --format json" {
		t.Errorf("argv = %q", got)
	}
}

func TestListModels_Errors(t *testing.T) {
	cases := map[string]struct{ script, want string }{
		"non-zero exit": {`echo "not logged in" >&2; exit 3`, "exited 3: not logged in"},
		"not json":      {`echo "Available models:"`, "parse kiro-cli --list-models output"},
		"empty list":    {`echo '{"models":[]}'`, "returned no models"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fakeCLI(t, tc.script+"\n")
			_, err := ListModels(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
