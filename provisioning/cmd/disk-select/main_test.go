package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const (
	expectedID = "0QEMU_QEMU_HARDDISK_labfleet-pxe-930004"
	actualID   = "0QEMU_QEMU_HARDDISK_drive-scsi0"
)

func writeInput(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0640); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRewriteFileRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	regular := writeInput(t, dir, "input.yaml", "serial: "+expectedID+"\n")
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(regular, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative.yaml", link, dir} {
		t.Run(path, func(t *testing.T) {
			if err := rewriteFile(path, expectedID, actualID); err == nil {
				t.Fatalf("rewriteFile(%q) unexpectedly succeeded", path)
			}
		})
	}
	b, err := os.ReadFile(regular)
	if err != nil || string(b) != "serial: "+expectedID+"\n" {
		t.Fatalf("rejected path modified input: %q, %v", b, err)
	}
}

func TestRewriteFilePreservesModeAndOwnerAndReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := writeInput(t, dir, "autoinstall.yaml", "storage:\n  match:\n    serial: "+expectedID+"\n")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := rewriteFile(path, expectedID, actualID); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode().Perm() != after.Mode().Perm() || after.Mode().Perm() != 0640 {
		t.Fatalf("mode changed: before=%#o after=%#o", before.Mode().Perm(), after.Mode().Perm())
	}
	oldStat, ok1 := before.Sys().(*syscall.Stat_t)
	newStat, ok2 := after.Sys().(*syscall.Stat_t)
	if ok1 && ok2 && (oldStat.Uid != newStat.Uid || oldStat.Gid != newStat.Gid) {
		t.Fatalf("owner changed: before=%d:%d after=%d:%d", oldStat.Uid, oldStat.Gid, newStat.Uid, newStat.Gid)
	}
	if ok1 && ok2 && oldStat.Ino == newStat.Ino {
		t.Fatal("replacement retained original inode; expected atomic rename replacement")
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), `serial: "`+actualID+`"`) {
		t.Fatalf("unexpected replacement contents %q, %v", b, err)
	}
}

func TestRewriteFileLeavesOriginalUnchangedOnRewriteErrors(t *testing.T) {
	for name, contents := range map[string]string{
		"mismatched": "serial: another-value\n",
		"duplicate":  "serial: " + expectedID + "\nserial: \"" + expectedID + "\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeInput(t, dir, "autoinstall.yaml", contents)
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := rewriteFile(path, expectedID, actualID); err == nil {
				t.Fatal("rewriteFile unexpectedly succeeded")
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != contents {
				t.Fatalf("original contents changed: %q, %v", got, err)
			}
			oldStat, ok1 := before.Sys().(*syscall.Stat_t)
			newStat, ok2 := after.Sys().(*syscall.Stat_t)
			if ok1 && ok2 && oldStat.Ino != newStat.Ino {
				t.Fatal("failed rewrite replaced original file")
			}
		})
	}
}
