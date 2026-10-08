//go:build (!darwin && !linux) || !cgo

package main

import (
	"errors"
	"time"
)

type usbAT struct{}

func openDJIUSBAT() (*usbAT, error) {
	return nil, errors.New("USB AT requires a macOS or Linux cgo build with libusb")
}

func (u *usbAT) Close() {}

func (u *usbAT) Description() string { return "USB AT unavailable" }

func (u *usbAT) Command(_ string, _ time.Duration) (string, error) {
	return "", errors.New("USB AT is unavailable in this build")
}

func (u *usbAT) CommandWithPrompt(_ string, _ []byte, _ time.Duration) (string, error) {
	return "", errors.New("USB AT is unavailable in this build")
}
