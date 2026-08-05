package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// readFileOrStdin implements the old CLI's "-D -" / "-B -" convention: a
// path of "-" means read the body from stdin instead of a file, so a pipe
// (`git diff | jira comment add ABC-1 -B -`) works without a temp file.
func readFileOrStdin(path string) (string, error) {
	if path == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return string(b), nil
}

// editorCommand is $EDITOR unless the flag overrides it, matching the old
// CLI's own fallback (a bare "-e" with no value names no editor to run).
func editorCommand(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("EDITOR")
}

// openInEditor writes initial to a temp file, runs cmdLine against it
// through a shell (the same "sh -c" pattern view.go already uses to run a
// configured command line, since an editor spec like "code --wait" is a
// command plus arguments, not a single executable path), and returns
// whatever the editor left behind.
//
// Editor and terminal both need direct access to the controlling tty, which
// is why Stdin/Stdout/Stderr are wired straight through rather than
// captured.
func openInEditor(cmdLine, initial string) (string, error) {
	if cmdLine == "" {
		return "", fmt.Errorf("no editor configured: pass -e or set $EDITOR")
	}
	f, err := os.CreateTemp("", "jira-edit-*.md")
	if err != nil {
		return "", fmt.Errorf("creating a scratch file for the editor: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString(initial); err != nil {
		f.Close()
		return "", fmt.Errorf("writing the scratch file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	cmd := exec.Command("sh", "-c", cmdLine+` "$1"`, "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("running the editor: %w", err)
	}

	edited, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading the edited file back: %w", err)
	}
	return string(edited), nil
}

// resolvedFieldsJSON is what --fields-json / --fields-json-file decode to:
// an arbitrary object, since the whole point of the flag is fields this
// program does not otherwise know the name of (customfield_NNNNN literals,
// per the migration plan's ban on hardcoding them).
//
// parseFieldsJSON merges the two sources the old CLI accepted (an inline
// string and a file), file first so the inline flag - the one typed at the
// command line, most likely to be a deliberate override - wins a key
// collision.
func parseFieldsJSON(inline, file string) (map[string]any, error) {
	merged := map[string]any{}
	if file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", file, err)
		}
		if err := mergeFieldsJSON(merged, raw, file); err != nil {
			return nil, err
		}
	}
	if inline != "" {
		if err := mergeFieldsJSON(merged, []byte(inline), "--fields-json"); err != nil {
			return nil, err
		}
	}
	return merged, nil
}

func mergeFieldsJSON(into map[string]any, raw []byte, source string) error {
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("%s is not a JSON object: %w", source, err)
	}
	for k, v := range decoded {
		into[k] = v
	}
	return nil
}

// splitTrimmed splits a comma-separated flag value the way search's -L and
// create/edit's -l already promise, dropping blanks so a trailing comma
// does not become an empty label.
func splitTrimmed(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
