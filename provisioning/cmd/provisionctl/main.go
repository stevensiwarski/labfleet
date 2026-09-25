package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/stevensiwarski/labfleet/provisioning/internal/pxe"
)

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "provisionctl:", e)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: provisionctl render|check|serve --config FILE")
	}
	cmd := args[0]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	config := fs.String("config", "", "JSON config file")
	if e := fs.Parse(args[1:]); e != nil {
		return e
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *config == "" {
		return fmt.Errorf("--config required")
	}
	c, e := pxe.LoadConfig(*config)
	if e != nil {
		return e
	}
	switch cmd {
	case "render":
		files, e := pxe.Render(c)
		if e != nil {
			return e
		}
		return pxe.WriteRendered(c.Output, files)
	case "check":
		if e = pxe.LiveHostCheck(c, "/etc/labfleet/provisioning-owned"); e != nil {
			return e
		}
		if e = pxe.ReadAndVerifyManifest(c.Artifacts); e != nil {
			return e
		}
		if _, e = pxe.ValidateSSHKey(c.SSHKeyPath); e != nil {
			return e
		}
		if e = pxe.LiveHostCheck(c, "/etc/labfleet/provisioning-owned"); e != nil {
			return e
		}
		files, e := pxe.Render(c)
		if e != nil {
			return e
		}
		tmp := filepath.Join(c.Output, "dnsmasq.conf")
		if e = pxe.WriteRendered(c.Output, files); e != nil {
			return e
		}
		out, e := exec.Command(c.Dnsmasq, "--test", "--conf-file="+tmp).CombinedOutput()
		if e != nil {
			return fmt.Errorf("dnsmasq config test: %w: %s", e, out)
		}
		return nil
	case "serve":
		if e = pxe.LiveHostCheck(c, "/etc/labfleet/provisioning-owned"); e != nil {
			return e
		}
		if e = pxe.ReadAndVerifyManifest(c.Artifacts); e != nil {
			return e
		}
		if e = pxe.LiveHostCheck(c, "/etc/labfleet/provisioning-owned"); e != nil {
			return e
		}
		files, e := pxe.Render(c)
		if e != nil {
			return e
		}
		if e = pxe.WriteRendered(c.Output, files); e != nil {
			return e
		}
		conf := filepath.Join(c.Output, "dnsmasq.conf")
		out, e := exec.Command(c.Dnsmasq, "--test", "--conf-file="+conf).CombinedOutput()
		if e != nil {
			return fmt.Errorf("dnsmasq config test: %w: %s", e, out)
		}
		if e = pxe.LiveHostCheck(c, "/etc/labfleet/provisioning-owned"); e != nil {
			return e
		}
		listener, e := pxe.ListenHTTP(c)
		if e != nil {
			return e
		}
		// ISO streaming may take longer than a short write deadline in a nested lab.
		// Request/idle timeouts remain bounded, and shutdown forcibly closes streams.
		server := &http.Server{Handler: pxe.Handler(c), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
		defer server.Close()
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		httpErr := make(chan error, 1)
		go func() { httpErr <- server.Serve(listener) }()
		proc := exec.Command(c.Dnsmasq, "--keep-in-foreground", "--conf-file="+conf)
		// Also terminate DHCP if the supervisor dies outside its signal handler.
		proc.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
		proc.Stdout = os.Stdout
		proc.Stderr = os.Stderr
		if e = proc.Start(); e != nil {
			_ = server.Close()
			return e
		}
		done := make(chan error, 1)
		go func() { done <- proc.Wait() }()
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case e = <-httpErr:
				if e == http.ErrServerClosed {
					e = nil
				}
				_ = proc.Process.Kill()
				<-done
				return e
			case e = <-done:
				_ = server.Close()
				if e == nil {
					return fmt.Errorf("dnsmasq exited unexpectedly")
				}
				return fmt.Errorf("dnsmasq exited: %w", e)
			case <-tick.C:
				if e = pxe.LiveHostCheck(c, "/etc/labfleet/provisioning-owned"); e != nil {
					_ = proc.Process.Kill()
					<-done
					_ = server.Close()
					return fmt.Errorf("host safety changed: %w", e)
				}
			case <-ctx.Done():
				_ = proc.Process.Signal(syscall.SIGTERM)
				select {
				case e = <-done:
				case <-time.After(5 * time.Second):
					_ = proc.Process.Kill()
					e = <-done
				}
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = server.Shutdown(shutdownCtx)
				return nil
			}
		}
	default:
		return fmt.Errorf("unknown subcommand %q", cmd)
	}
}
