package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeSysfsFixture(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxUSBDiscoveryMatchesDJIAndSortsInterfaces(t *testing.T) {
	root := t.TempDir()
	for name, value := range map[string]string{
		"1-1/idVendor": "1234", "1-1/idProduct": "4006",
		"1-2/idVendor": "2ca3", "1-2/idProduct": "4006", "1-2/product": "Baiwang", "1-2/manufacturer": "BAIWANG", "1-2/speed": "480",
		"1-2:1.10/bInterfaceNumber": "0a", "1-2:1.10/bInterfaceClass": "ff", "1-2:1.10/bNumEndpoints": "02",
		"1-2:1.2/bInterfaceNumber": "02", "1-2:1.2/bInterfaceClass": "ff", "1-2:1.2/bNumEndpoints": "03",
	} {
		writeSysfsFixture(t, root, name, value)
	}
	got := discoverLinuxUSBDevice(root)
	if got == nil || got.Product != "Baiwang" || got.LocationID != "1-2" || got.Mode != "vendor-specific QMI/diagnostic mode" {
		t.Fatalf("unexpected device: %+v", got)
	}
	if len(got.Interfaces) != 2 || got.Interfaces[0].Number != 2 || got.Interfaces[1].Number != 10 || got.Interfaces[1].Endpoints != 2 {
		t.Fatalf("unexpected interfaces: %+v", got.Interfaces)
	}
	if err := os.Remove(filepath.Join(root, "1-2/idProduct")); err != nil {
		t.Fatal(err)
	}
	if discoverLinuxUSBDevice(root) != nil {
		t.Fatal("must not report a disconnected/nonmatching device")
	}
}

func TestLinuxRouteUsesLowestActiveMetricAndLittleEndianGateway(t *testing.T) {
	data := `Iface Destination Gateway Flags RefCnt Use Metric Mask
eth0 00000000 010011AC 0003 0 0 100 00000000
usb0 00000000 01E1A8C0 0003 0 0 50 00000000
down0 00000000 0100000A 0002 0 0 1 00000000
usb0 00E1A8C0 00000000 0001 0 0 0 00FFFFFF`
	got := parseLinuxDefaultRoute(data)
	if got.Interface != "usb0" || got.Gateway != "192.168.225.1" {
		t.Fatalf("unexpected route: %+v", got)
	}
	if got := parseLinuxDefaultRoute("invalid input"); got.Interface != "" {
		t.Fatalf("invalid input must yield no route: %+v", got)
	}
}

func TestLinuxNetworkUSBAncestryAndTraffic(t *testing.T) {
	root := t.TempDir()
	writeSysfsFixture(t, root, "usb/idVendor", "2ca3")
	writeSysfsFixture(t, root, "usb/idProduct", "4006")
	if err := os.MkdirAll(filepath.Join(root, "usb/interface"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "net/usb0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "usb/interface"), filepath.Join(root, "net/usb0/device")); err != nil {
		t.Fatal(err)
	}
	if !isDJINetworkDevice(filepath.Join(root, "net/usb0")) || isDJINetworkDevice(filepath.Join(root, "net/eth0")) {
		t.Fatal("DJI USB ancestry must distinguish a modem from Docker's virtual network")
	}
	writeSysfsFixture(t, root, "net/usb0/statistics/rx_bytes", "4096")
	writeSysfsFixture(t, root, "net/usb0/statistics/tx_bytes", "2048")
	counters, err := discoverLinuxInterfaceCounters(filepath.Join(root, "net"))
	if err != nil || counters["usb0"].RX != 4096 || counters["usb0"].TX != 2048 {
		t.Fatalf("unexpected counters: %+v %v", counters, err)
	}
	if runtime.GOOS == "linux" {
		if hasLikelyUSBNetworkInterface([]macNetInterface{{Name: "eth0", Kind: "ethernet", Status: "active"}}) {
			t.Fatal("virtual Ethernet must not be reported as the DJI USB network")
		}
	}
}
