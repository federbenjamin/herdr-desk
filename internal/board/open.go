package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/federbenjamin/desk/internal/model"
)

// herdrID is what a workspace or pane id must look like before it reaches herdr's argv.
var herdrID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.-]*$`)

const viewerPlugin = "herdr-file-viewer"

// focusArgvs are the commands that focus a run's pane. herdr has no focus-by-id: zooming the pane on and off
// focuses it, as scripts/open-pane.sh does.
func focusArgvs(herdr string, r model.Run) ([][]string, error) {
	if herdr == "" {
		return nil, errors.New("f needs herdr, and herdr is not found")
	}
	for _, id := range []string{r.Workspace, r.Pane} {
		if !herdrID.MatchString(id) {
			return nil, fmt.Errorf("the run's workspace or pane id %q is not one herdr gives", id)
		}
	}
	return [][]string{
		{herdr, "workspace", "focus", r.Workspace},
		{herdr, "pane", "zoom", r.Pane, "--on"},
		{herdr, "pane", "zoom", r.Pane, "--off"},
	}, nil
}

func refURL(ref string) (string, bool) {
	if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
		return "", false
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return "", false
	}
	return ref, true
}

func refPath(ref, dir string) (string, error) {
	p := ref
	if !filepath.IsAbs(p) {
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("%s is not a URL, and its task has no project folder to find it in", ref)
		}
		p = filepath.Join(dir, p)
	}
	p = filepath.Clean(p)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("%s is not a URL or a file here: %w", ref, err)
	}
	return p, nil
}

func openerArgv(u string) []string {
	if runtime.GOOS == "darwin" {
		return []string{"open", u}
	}
	return []string{"xdg-open", u}
}

func viewerListArgv(herdr string) []string {
	return []string{herdr, "plugin", "list", "--plugin", viewerPlugin, "--json"}
}

func hasViewer(out []byte) bool {
	var list struct {
		Result struct {
			Plugins []json.RawMessage `json:"plugins"`
		} `json:"result"`
	}
	return json.Unmarshal(out, &list) == nil && len(list.Result.Plugins) > 0
}

func viewerArgv(herdr, path string) []string {
	return []string{herdr, "plugin", "pane", "open", "--plugin", viewerPlugin, "--entrypoint", "file-viewer",
		"--env", "HERDR_FILE_VIEWER_OPEN=" + path, "--focus"}
}
