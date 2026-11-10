//go:build linux

package logs

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// TestRealJournal reads the real journal of the Linux computer that runs
// the test. It writes a line with logger(1) and waits for it. The test
// runs only when NIDA_TEST_JOURNAL=1, because it needs a systemd journal
// that the user can read (the workflow core sets it).
func TestRealJournal(t *testing.T) {
	if os.Getenv("NIDA_TEST_JOURNAL") != "1" {
		t.Skip("set NIDA_TEST_JOURNAL=1 to read the real journal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := []string{"journalctl"}
	if v := os.Getenv("NIDA_TEST_JOURNALCTL"); v != "" {
		cmd = []string{"sh", "-c", v + ` "$@"`, "journalctl"}
	}
	j := &Journal{Command: cmd, Logger: quiet}
	got := make(chan Line, 64)
	go j.Run(ctx, func(l Line) { got <- l })

	marker := "nida-core test " + strconv.FormatInt(time.Now().UnixNano(), 10)
	// journalctl needs a moment to start; write the line some times.
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if err := exec.Command("logger", "-t", "nida-test", "-p", "user.warning", marker).Run(); err != nil {
				t.Fatalf("logger: %v", err)
			}
		case l := <-got:
			if l.Message == marker {
				if l.Source.ID != "service/nida-test" || l.Level != LevelWarning {
					t.Errorf("line = %+v", l)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("the line of logger did not come from the journal")
		}
	}
}
