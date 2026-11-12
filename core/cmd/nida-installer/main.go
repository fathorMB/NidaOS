// Command nida-installer is the NidaOS installer (milestone M-07).
//
// It runs on the installation media. It serves the installer page on the
// port 80: the kiosk browser of the computer shows it in full screen
// (decision D32), and a browser of an other computer can follow the
// installation. Only the screen of the computer can start it.
//
//	nida-installer [--addr :80] [--demo]
//
// --demo shows the disks of the mockup and simulates the installation. It
// changes no disk.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fathorMB/NidaOS/core/internal/installer"
	"github.com/fathorMB/NidaOS/core/internal/installerui"
	"github.com/fathorMB/NidaOS/core/internal/version"
)

// PayloadRefFile has the reference of the NidaOS image in the container
// storage of the media. The build of the installer image writes it.
const PayloadRefFile = "/usr/share/nidaos/payload-ref"

func main() {
	addr := flag.String("addr", ":80", "the listen address of the installer page")
	demo := flag.Bool("demo", false, "show the mockup disks and simulate the installation")
	target := flag.String("target", "/run/nida-install/target", "the mount point of the new system")
	exitAfter := flag.Duration("exit-after", 0, "stop after this time (for the test scripts)")
	flag.Parse()
	if *exitAfter > 0 {
		time.AfterFunc(*exitAfter, func() { os.Exit(0) })
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s := &installer.Server{Logger: logger, UI: installerui.New()}
	if *demo {
		tmp, err := os.MkdirTemp("", "nida-installer-demo-")
		if err != nil {
			logger.Error("temporary folder", "err", err)
			os.Exit(1)
		}
		defer os.RemoveAll(tmp)
		s.Disks = func(context.Context) ([]installer.Disk, error) { return installer.DemoDisks(), nil }
		s.Facts = func() installer.Facts {
			f := installer.DemoFacts()
			f.Version = version.Version
			return f
		}
		s.Seed = func(context.Context) (installer.Seed, error) {
			return installer.Seed{Found: true, HasKey: true, Key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDemo operator@windows-pc", KeyComment: "operator@windows-pc"}, nil
		}
		s.Measure = installer.DemoMeasure
		s.NewEngine = func(log func(string), step func(int, string), lines func() []string) *installer.Engine {
			return &installer.Engine{Run: installer.DemoRunner{Target: tmp + "/target", Delay: 250 * time.Millisecond},
				Target: tmp + "/target", TmpMount: tmp + "/tmp", PayloadRef: "ghcr.io/fathormb/nidaos:demo", UEFI: true,
				Log: log, Step: step, Lines: lines}
		}
		s.Power = func(action string) { logger.Info("demonstration: no " + action) }
	} else {
		quiet := installer.ExecRunner{}
		ref := readPayloadRef()
		_, uefi := os.Stat("/sys/firmware/efi")
		s.Disks = func(ctx context.Context) ([]installer.Disk, error) { return installer.ReadDisks(ctx, quiet) }
		s.Facts = func() installer.Facts {
			f := installer.ReadFacts("/")
			f.Version = version.Version
			return f
		}
		s.Seed = func(ctx context.Context) (installer.Seed, error) { return installer.FindSeed(ctx, quiet) }
		s.Measure = installer.MeasureDisk
		s.NewEngine = func(log func(string), step func(int, string), lines func() []string) *installer.Engine {
			return &installer.Engine{Run: installer.ExecRunner{Line: log}, Target: *target, TmpMount: "/run/nida-install/tmp",
				PayloadRef: ref, UEFI: uefi == nil, Log: log, Step: step, Lines: lines}
		}
		s.Power = func(action string) {
			logger.Info("the installer runs systemctl " + action)
			_, _ = quiet.Run(context.Background(), "systemctl", action)
		}
	}

	srv := &http.Server{Addr: *addr, Handler: s.Handler(ctx), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	go s.Start(ctx)
	fmt.Fprintf(os.Stderr, "nida-installer %s: the installer page is at http://%s/\n", version.Version, strings.TrimPrefix(*addr, ":"))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("the installer page cannot start", "err", err)
		os.Exit(1)
	}
}

func readPayloadRef() string {
	b, err := os.ReadFile(PayloadRefFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
