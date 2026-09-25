package diskidentity

import (
	"reflect"
	"testing"
)

func owned() Disk {
	return Disk{Name: "sda", Whole: true, IDSerial: "0QEMU_QEMU_HARDDISK_drive-scsi0", SCSISerial: "labfleet-pxe-930004", Devlinks: []string{"/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_labfleet-pxe-930004"}}
}
func TestSelect(t *testing.T) {
	d := owned()
	got, err := Select("0QEMU_QEMU_HARDDISK_labfleet-pxe-930004", []Disk{d})
	if err != nil || got.Name != "sda" {
		t.Fatalf("got %+v, %v", got, err)
	}
	cases := [][]Disk{nil, {func() Disk { x := d; x.SCSISerial = "other"; return x }()}, {func() Disk { x := d; x.Whole = false; return x }()}, {d, func() Disk { x := d; x.Name = "sdb"; return x }()}, {d, func() Disk { x := d; x.Name = "sdb"; x.IDSerial = "duplicate"; return x }()}}
	for i, ds := range cases {
		if _, err := Select("0QEMU_QEMU_HARDDISK_labfleet-pxe-930004", ds); err == nil {
			t.Errorf("case %d unexpectedly selected", i)
		}
	}
	if err := ValidateExpected("not-owned"); err == nil {
		t.Fatal("accepted unowned ID")
	}
	if err := ValidateExpected("labfleet-" + string(make([]byte, 129))); err == nil {
		t.Fatal("accepted long ID")
	}
}
func TestRewrite(t *testing.T) {
	in := []byte("storage:\n  config:\n    - match:\n        serial: \"EXPECTED_labfleet-disk\"\nother: keep\n")
	want := []byte("storage:\n  config:\n    - match:\n        serial: \"QEMU_sda\"\nother: keep\n")
	out, err := Rewrite(in, "EXPECTED_labfleet-disk", "QEMU_sda")
	if err != nil || !reflect.DeepEqual(out, want) {
		t.Fatalf("%s %v", out, err)
	}
	if _, err = Rewrite([]byte("serial: \"EXPECTED_labfleet-disk\"\nserial: \"EXPECTED_labfleet-disk\"\n"), "EXPECTED_labfleet-disk", "actual"); err == nil {
		t.Fatal("accepted ambiguous YAML")
	}
}
