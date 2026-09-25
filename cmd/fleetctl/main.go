package main

import (
	"fmt"
	"os"

	"github.com/stevensiwarski/labfleet/internal/tofusafety"
)

func description() string {
	return "LabFleet command-line tool (under development)"
}

func main() {
	if len(os.Args) == 1 {
		fmt.Println(description())
		return
	}
	if os.Args[1] != "tofu-check" {
		fmt.Fprintln(os.Stderr, "usage: fleetctl tofu-check -plan PATH")
		os.Exit(2)
	}
	path := ""
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "-plan" && i+1 < len(os.Args) {
			path = os.Args[i+1]
			i++
		} else {
			fmt.Fprintln(os.Stderr, "usage: fleetctl tofu-check -plan PATH")
			os.Exit(2)
		}
	}
	if path == "" {
		fmt.Fprintln(os.Stderr, "usage: fleetctl tofu-check -plan PATH")
		os.Exit(2)
	}
	insecure := os.Getenv("PROXMOX_VE_INSECURE") == "true"
	if value := os.Getenv("PROXMOX_VE_INSECURE"); value != "" && value != "true" && value != "false" {
		fmt.Fprintln(os.Stderr, "tofu-check: PROXMOX_VE_INSECURE must be true or false")
		os.Exit(2)
	}
	count, err := tofusafety.CheckFile(path, os.Getenv("PROXMOX_VE_ENDPOINT"), os.Getenv("PROXMOX_VE_API_TOKEN"), insecure)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tofu-check: "+err.Error())
		os.Exit(1)
	}
	fmt.Printf("tofu-check: safe (%d LabFleet VM change(s))\n", count)
}
