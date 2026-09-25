package diskidentity

import (
	"strings"
	"testing"
)

func TestRewriteNormalizedScalars(t *testing.T) {
	expected := "0QEMU_QEMU_HARDDISK_labfleet-pxe-930004"
	for _, scalar := range []string{expected, `"` + expected + `"`, "'" + expected + "'"} {
		out, err := Rewrite([]byte("storage:\n  layout:\n    match:\n      serial: "+scalar+"\n"), expected, "observed")
		if err != nil || !strings.Contains(string(out), "      serial: \"observed\"\n") {
			t.Fatalf("scalar %s: %s %v", scalar, out, err)
		}
	}
}

func TestObservedSCSISerialByIDVariant(t *testing.T) {
	expected := "SQEMU_QEMU_HARDDISK_labfleet-pxe-930004"
	d := Disk{Name: "sda", Whole: true, IDSerial: "0QEMU_QEMU_HARDDISK_drive-scsi0", SCSISerial: "labfleet-pxe-930004", Devlinks: []string{"/dev/disk/by-id/scsi-" + expected}}
	if _, err := Select(expected, []Disk{d}); err != nil {
		t.Fatal(err)
	}
	if _, err := Select("0QEMU_QEMU_HARDDISK_labfleet-pxe-930004", []Disk{d}); err == nil {
		t.Fatal("must not substitute a different by-id variant")
	}
}
