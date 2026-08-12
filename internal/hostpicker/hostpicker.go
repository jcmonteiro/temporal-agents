// Package hostpicker opens graphical selection dialogs on the machine that runs the
// hub. A browser cannot disclose an absolute local path, so repository selection
// must happen beside the worker that will use the path.
package hostpicker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Picker opens the native directory chooser available on the current host.
type Picker struct{}

// PickDirectory returns the selected absolute directory. Closing the dialog is a
// successful request with selected=false.
func (Picker) PickDirectory(ctx context.Context) (directory string, selected bool, err error) {
	command, cancelledExitCode, err := pickerCommand(ctx)
	if err != nil {
		return "", false, err
	}
	output, err := command.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) &&
			(exitError.ExitCode() == cancelledExitCode ||
				runtime.GOOS == "darwin" && strings.Contains(string(exitError.Stderr), "(-128)")) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("open the native folder picker: %w", err)
	}
	directory = strings.TrimSpace(string(output))
	if directory == "" {
		return "", false, nil
	}
	directory = filepath.Clean(directory)
	if !filepath.IsAbs(directory) {
		return "", false, fmt.Errorf("the native folder picker returned a relative path")
	}
	return directory, true, nil
}

func pickerCommand(ctx context.Context) (*exec.Cmd, int, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "/usr/bin/osascript", "-e",
			`POSIX path of (choose folder with prompt "Select a repository")`), -128, nil
	case "linux":
		if zenity, err := exec.LookPath("zenity"); err == nil {
			return exec.CommandContext(ctx, zenity,
				"--file-selection", "--directory", "--title=Select a repository"), 1, nil
		}
		if kdialog, err := exec.LookPath("kdialog"); err == nil {
			return exec.CommandContext(ctx, kdialog, "--getexistingdirectory", ".",
				"--title", "Select a repository"), 1, nil
		}
		return nil, 0, errors.New("no native folder picker is installed; install zenity or kdialog")
	default:
		return nil, 0, fmt.Errorf("native folder selection is not supported on %s", runtime.GOOS)
	}
}
