package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/stevensiwarski/labfleet/provisioning/internal/diskidentity"
)

func main() {
	expected := flag.String("expected-id", "", "expected LabFleet SCSI disk ID")
	autoinstall := flag.String("autoinstall", "/autoinstall.yaml", "Subiquity autoinstall YAML")
	flag.Parse()
	if flag.NArg() != 0 {
		fatal("unexpected arguments")
	}
	if err := diskidentity.ValidateExpected(*expected); err != nil {
		fatal("invalid expected ID")
	}
	disks, err := discover()
	if err != nil {
		fatal(err.Error())
	}
	d, err := diskidentity.Select(*expected, disks)
	if err != nil {
		fatal(err.Error())
	}
	link := "/dev/disk/by-id/scsi-" + *expected
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil || filepath.Clean(resolved) != "/dev/"+d.Name {
		fatal("by-id symlink target mismatch")
	}
	st, err := os.Stat(resolved)
	if err != nil || st.Mode()&os.ModeDevice == 0 || st.Mode()&os.ModeCharDevice != 0 {
		fatal("selected target must be a block device")
	}
	if err := rewriteFile(*autoinstall, *expected, d.IDSerial); err != nil {
		fatal(err.Error())
	}
	fmt.Printf("selected expected=%s actual=%s device=/dev/%s\n", *expected, d.IDSerial, d.Name)
}

func discover() ([]diskidentity.Disk, error) {
	entries, err := os.ReadDir("/sys/class/block")
	if err != nil {
		return nil, fmt.Errorf("cannot enumerate block devices")
	}
	var names []string
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join("/sys/class/block", e.Name(), "partition")); os.IsNotExist(err) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]diskidentity.Disk, 0, len(names))
	for _, name := range names {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		b, err := exec.CommandContext(ctx, "udevadm", "info", "--query=property", "--name=/dev/"+name).Output()
		cancel()
		if err != nil {
			return nil, fmt.Errorf("udev properties unavailable for a whole disk")
		}
		p := map[string]string{}
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok {
				p[k] = v
			}
		}
		out = append(out, diskidentity.Disk{Name: name, Whole: true, IDSerial: p["ID_SERIAL"], SCSISerial: p["ID_SCSI_SERIAL"], Devlinks: strings.Fields(p["DEVLINKS"])})
	}
	return out, nil
}

func rewriteFile(path, expected, actual string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("autoinstall path must be absolute")
	}
	st, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("autoinstall file unavailable")
	}
	if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("autoinstall path must be a regular non-symlink file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read autoinstall file")
	}
	out, err := diskidentity.Rewrite(b, expected, actual)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".disk-select-*")
	if err != nil {
		return fmt.Errorf("cannot create atomic replacement")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(st.Mode().Perm()); err == nil {
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			err = f.Chown(int(sys.Uid), int(sys.Gid))
		}
	}
	if err == nil {
		_, err = f.Write(out)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("cannot write replacement")
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("cannot atomically replace autoinstall file")
	}
	return nil
}
func fatal(s string) { fmt.Fprintln(os.Stderr, "disk-select:", s); os.Exit(1) }
