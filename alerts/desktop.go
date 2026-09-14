package alerts

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Desktop raises a notification through notify-send.
func Desktop(urgency, title, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "notify-send",
		"--app-name=sitesentinel",
		"--urgency="+urgency,
		title, body,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notify-send: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
