package session

import "testing"

func TestNewAcquisition(t *testing.T) {
	got, err := NewAcquisition(" device-1 ", " gateway-1 ", " client-1 ")
	if err != nil {
		t.Fatalf("NewAcquisition() error = %v", err)
	}
	if got.DeviceID != "device-1" || got.GatewayID != "gateway-1" || got.ClientInstanceID != "client-1" {
		t.Fatalf("NewAcquisition() = %+v", got)
	}
}

func TestNewAcquisitionRejectsInvalidIdentifiers(t *testing.T) {
	for _, values := range [][3]string{
		{"", "gateway", "client"},
		{"device", "bad gateway", "client"},
		{"device", "gateway", "client/path"},
	} {
		if _, err := NewAcquisition(values[0], values[1], values[2]); err == nil {
			t.Fatalf("NewAcquisition(%q, %q, %q) unexpectedly succeeded", values[0], values[1], values[2])
		}
	}
}
