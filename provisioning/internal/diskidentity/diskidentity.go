// Package diskidentity selects an explicitly owned whole disk and updates the
// Subiquity match serial without relying on QEMU's synthetic ID_SERIAL.
package diskidentity

import (
	"fmt"
	"regexp"
	"strings"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

type Disk struct {
	Name       string
	Whole      bool
	IDSerial   string
	SCSISerial string
	Devlinks   []string
}

func ValidateExpected(s string) error {
	if !safeID.MatchString(s) || !strings.Contains(s, "labfleet-") {
		return fmt.Errorf("invalid expected disk ID")
	}
	return nil
}

// Select requires exactly one exact by-id target with LabFleet SCSI ownership,
// and rejects synthetic ID_SERIAL collisions across every discovered disk.
func Select(expected string, disks []Disk) (Disk, error) {
	if err := ValidateExpected(expected); err != nil {
		return Disk{}, err
	}
	want := "/dev/disk/by-id/scsi-" + expected
	var matches []Disk
	seenSerial := map[string]string{}
	for _, d := range disks {
		if !d.Whole {
			continue
		}
		if d.IDSerial != "" {
			if !safeID.MatchString(d.IDSerial) {
				return Disk{}, fmt.Errorf("unsafe disk identity")
			}
			if prior, ok := seenSerial[d.IDSerial]; ok {
				return Disk{}, fmt.Errorf("duplicate ID_SERIAL on %s and %s", prior, d.Name)
			}
			seenSerial[d.IDSerial] = d.Name
		}
		link := false
		for _, l := range d.Devlinks {
			if l == want {
				link = true
			}
		}
		if !link {
			continue
		}
		ownedID := expected == "0QEMU_QEMU_HARDDISK_"+d.SCSISerial || expected == "SQEMU_QEMU_HARDDISK_"+d.SCSISerial
		if d.Name == "" || d.IDSerial == "" || d.SCSISerial == "" || !strings.HasPrefix(d.SCSISerial, "labfleet-") || !ownedID {
			return Disk{}, fmt.Errorf("by-id target lacks matching LabFleet SCSI identity")
		}
		matches = append(matches, d)
	}
	if len(matches) != 1 {
		return Disk{}, fmt.Errorf("expected exactly one owned disk, found %d", len(matches))
	}
	return matches[0], nil
}

// Rewrite replaces exactly one YAML serial scalar equal to expected. Subiquity
// may normalize cloud-init's YAML and remove quotes before early-commands run.
func Rewrite(data []byte, expected, actual string) ([]byte, error) {
	if ValidateExpected(expected) != nil || !safeID.MatchString(actual) {
		return nil, fmt.Errorf("invalid disk serial")
	}
	lines := strings.SplitAfter(string(data), "\n")
	count := 0
	for i, line := range lines {
		body, ending := strings.TrimSuffix(line, "\n"), ""
		if strings.HasSuffix(line, "\n") {
			ending = "\n"
		}
		trim := strings.TrimSpace(body)
		if strings.HasPrefix(trim, "serial:") {
			value := strings.TrimSpace(strings.TrimPrefix(trim, "serial:"))
			if value == expected || value == `"`+expected+`"` || value == "'"+expected+"'" {
				count++
				indent := body[:len(body)-len(strings.TrimLeft(body, " \t"))]
				lines[i] = indent + `serial: "` + actual + `"` + ending
			}
		}
	}
	if count != 1 {
		return nil, fmt.Errorf("expected exactly one matching serial scalar, found %d", count)
	}
	return []byte(strings.Join(lines, "")), nil
}
