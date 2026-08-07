package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// splitArgs separates args into the tokens flag.FlagSet.Parse should see
// (every flag, plus the following token for any flag that takes a value)
// and the positional tokens left over, in original order. Go's flag package
// stops parsing at the first token that does not start with "-", so a
// positional given before a flag - `get ABC-1 -f json`, the form used
// throughout this repo's own config and README - silently hides every flag
// after it; splitting the two apart first lets a caller use either order.
//
// valueFlags names every flag (long form, no dashes) that consumes the
// following argument as its value; anything else starting with "-" is
// treated as a boolean flag consuming nothing. A flag written as
// "-f=json"/"--f=json" already carries its value and is left alone too.
func splitArgs(args []string, valueFlags map[string]bool) (flagArgs, positionals []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		// A bare "-" is the stdin marker used as a flag's value (-B -), never
		// a flag itself, so it must fall through to the flag that consumed it
		// above rather than being treated as a positional here. It only
		// reaches this loop directly when nothing before it claimed it, i.e.
		// when it is genuinely positional (e.g. a search query of "-").
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		flagArgs = append(flagArgs, a)
		name := strings.TrimLeft(a, "-")
		if strings.ContainsRune(name, '=') {
			continue
		}
		if valueFlags[name] && i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return flagArgs, positionals
}

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
	defer func() { _ = os.Remove(path) }()
	if _, err := f.WriteString(initial); err != nil {
		_ = f.Close()
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
