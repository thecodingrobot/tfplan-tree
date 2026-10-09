package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Copy places s into the system clipboard. It tries native clipboard tools
// first, then falls back to the OSC52 terminal escape sequence (which also
// works over SSH and inside tmux).
func Copy(s string) error {
	var errs []string
	if err := copyWithTool(s); err == nil {
		return nil
	} else if err != errNoTool {
		errs = append(errs, err.Error())
	}
	if err := copyOSC52(s); err != nil {
		errs = append(errs, err.Error())
		return fmt.Errorf("clipboard: %s", strings.Join(errs, "; "))
	}
	return nil
}

var errNoTool = fmt.Errorf("no clipboard tool")

func copyWithTool(s string) error {
	type tool struct {
		path string
		args []string
	}
	for _, t := range []tool{
		{"pbcopy", nil},  // macOS
		{"wl-copy", nil}, // wayland
		{"xclip", []string{"-selection", "clipboard"}}, // x11
		{"xsel", []string{"--clipboard", "--input"}},   // x11
		{"termux-clipboard-set", nil},                  // termux
		{"clip.exe", nil},                              // wsl
	} {
		path, err := exec.LookPath(t.path)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, t.args...)
		cmd.Stdin = strings.NewReader(s)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return errNoTool
}

// copyOSC52 writes the OSC52 escape sequence to the controlling terminal.
func copyOSC52(s string) error {
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open tty: %w", err)
	}
	defer f.Close()

	seq := fmt.Sprintf("\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(s)))
	// When inside tmux, wrap in a passthrough escape so the outer terminal sees it.
	if os.Getenv("TMUX") != "" {
		seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	_, err = f.WriteString(seq)
	return err
}
