package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const viewerPlugin = "herdr-file-viewer"

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

// listsPlugin reads herdr's plugin list answer: true when it holds a plugin. An answer that is not that JSON, or
// that holds no plugins list, is an error, never "not installed"; herdr lists no plugin as "plugins":[].
func listsPlugin(out []byte) (bool, error) {
	var list struct {
		Result *struct {
			Plugins *[]json.RawMessage `json:"plugins"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return false, err
	}
	switch {
	case list.Result == nil:
		return false, errors.New("the answer has no result")
	case list.Result.Plugins == nil:
		return false, errors.New("the answer has no plugins list")
	}
	return len(*list.Result.Plugins) > 0, nil
}

func viewerArgv(herdr, path string) []string {
	return []string{herdr, "plugin", "pane", "open", "--plugin", viewerPlugin, "--entrypoint", "file-viewer",
		"--env", "HERDR_FILE_VIEWER_OPEN=" + path, "--focus"}
}
