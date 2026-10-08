package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// These helpers use Linux sysfs/procfs. Keeping them independent of cgo also
// allows their parsers to be tested on a Mac without a USB device.
func sysfsText(path string) string {
	data, _ := os.ReadFile(path)
	return strings.TrimSpace(string(data))
}

func sysfsInt(path string, base int) int {
	value, _ := strconv.ParseInt(sysfsText(path), base, 32)
	return int(value)
}

func discoverLinuxUSBDevice(root string) *usbDeviceStatus {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if sysfsText(filepath.Join(path, "idVendor")) != "2ca3" || sysfsText(filepath.Join(path, "idProduct")) != "4006" {
			continue
		}
		device := &usbDeviceStatus{
			Product: sysfsText(filepath.Join(path, "product")), Vendor: sysfsText(filepath.Join(path, "manufacturer")),
			VendorID: "2ca3", ProductID: "4006", LocationID: entry.Name(),
			Speed: sysfsText(filepath.Join(path, "speed")) + " Mbps", Mode: "USB mode",
		}
		if device.Product == "" {
			device.Product = "DJI 4G Module"
		}
		if device.Vendor == "" {
			device.Vendor = "DJI"
		}
		for _, intf := range entries {
			if !strings.HasPrefix(intf.Name(), entry.Name()+":") {
				continue
			}
			intfPath := filepath.Join(root, intf.Name())
			device.Interfaces = append(device.Interfaces, usbInterfaceStatus{
				Number:    sysfsInt(filepath.Join(intfPath, "bInterfaceNumber"), 16),
				Class:     sysfsInt(filepath.Join(intfPath, "bInterfaceClass"), 16),
				Subclass:  sysfsInt(filepath.Join(intfPath, "bInterfaceSubClass"), 16),
				Protocol:  sysfsInt(filepath.Join(intfPath, "bInterfaceProtocol"), 16),
				Endpoints: sysfsInt(filepath.Join(intfPath, "bNumEndpoints"), 16),
			})
		}
		sort.Slice(device.Interfaces, func(i, j int) bool { return device.Interfaces[i].Number < device.Interfaces[j].Number })
		if allVendorSpecific(device.Interfaces) {
			device.Mode = "vendor-specific QMI/diagnostic mode"
		}
		return device
	}
	return nil
}

func isUSBTrafficInterface(item macNetInterface) bool {
	if item.Status != "active" {
		return false
	}
	if item.Kind == "usb-ethernet" {
		return true
	}
	return runtime.GOOS != "linux" && item.Kind == "ethernet" && item.Name != "en0"
}

// Match the physical USB ancestry, rather than treating the container's eth0
// or another adapter as DJI cellular traffic.
func isDJINetworkDevice(path string) bool {
	path, err := filepath.EvalSymlinks(filepath.Join(path, "device"))
	if err != nil {
		return false
	}
	for path != "/" && path != "." {
		if sysfsText(filepath.Join(path, "idVendor")) == "2ca3" && sysfsText(filepath.Join(path, "idProduct")) == "4006" {
			return true
		}
		path = filepath.Dir(path)
	}
	return false
}

func discoverLinuxNetworkInterfaces(root string) []macNetInterface {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var result []macNetInterface
	for _, intf := range interfaces {
		if intf.Flags&net.FlagLoopback != 0 {
			continue
		}
		item := macNetInterface{Name: intf.Name, Status: "inactive", Kind: "other"}
		if intf.Flags&net.FlagUp != 0 && intf.Flags&net.FlagRunning != 0 {
			item.Status = "active"
		}
		if isDJINetworkDevice(filepath.Join(root, intf.Name)) {
			item.Kind = "usb-ethernet"
		}
		addresses, _ := intf.Addrs()
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil {
				item.IPv4 = ip.String()
				break
			}
		}
		result = append(result, item)
	}
	return result
}

func parseLinuxDefaultRoute(data string) macDefaultRoute {
	var result macDefaultRoute
	bestMetric := uint64(^uint64(0))
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[1] != "00000000" || fields[7] != "00000000" {
			continue
		}
		gateway, gatewayErr := strconv.ParseUint(fields[2], 16, 32)
		flags, flagsErr := strconv.ParseUint(fields[3], 16, 32)
		metric, metricErr := strconv.ParseUint(fields[6], 10, 64)
		if gatewayErr != nil || flagsErr != nil || metricErr != nil || flags&1 == 0 || metric >= bestMetric {
			continue
		}
		ip := make(net.IP, 4)
		binary.LittleEndian.PutUint32(ip, uint32(gateway))
		result = macDefaultRoute{Interface: fields[0], Gateway: ip.String()}
		bestMetric = metric
	}
	return result
}

func discoverLinuxInterfaceCounters(root string) (map[string]networkByteCounters, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	result := make(map[string]networkByteCounters)
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name(), "statistics")
		rx, rxErr := strconv.ParseUint(sysfsText(filepath.Join(path, "rx_bytes")), 10, 64)
		tx, txErr := strconv.ParseUint(sysfsText(filepath.Join(path, "tx_bytes")), 10, 64)
		if rxErr != nil || txErr != nil {
			return nil, fmt.Errorf("read network counters for %s: rx=%v tx=%v", entry.Name(), rxErr, txErr)
		}
		result[entry.Name()] = networkByteCounters{RX: rx, TX: tx}
	}
	return result, nil
}
