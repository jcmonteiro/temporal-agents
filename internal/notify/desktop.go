package notify

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"temporal-agents/internal/notification"
)

// Desktop is a driven adapter that posts a macOS desktop notification via the
// built-in `osascript` interpreter. It implements notification.Notifier.
type Desktop struct{}

// NewDesktop returns a macOS desktop notifier.
func NewDesktop() Desktop { return Desktop{} }

// Notify shows a desktop notification with the given title and body. It shells
// out to osascript's `display notification` command.
func (d Desktop) Notify(ctx context.Context, n notification.Notification) error {
	script := fmt.Sprintf(
		"display notification %s with title %s",
		quoteAppleScript(n.Body), quoteAppleScript(n.Title),
	)
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("osascript display notification: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// quoteAppleScript renders s as a double-quoted AppleScript string literal,
// escaping backslashes and double quotes so the injected text cannot break out
// of the literal.
func quoteAppleScript(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
