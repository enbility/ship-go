package api

import "net"

// RemoteMdnsService represents a SHIP service announced via mDNS
type RemoteMdnsService struct {
	Name       string               `json:"name"`
	Ski        string               `json:"ski"`
	ShipID     string               `json:"shipID"`
	Brand      string               `json:"brand"`
	Type       string               `json:"type"`
	Model      string               `json:"model"`
	Serial     string               `json:"serial"`
	Categories []DeviceCategoryType `json:"categories"`
	Addresses  []net.IP             `json:"addresses"` // addresses the service resolved to, or the configured IPv4 of a known service
}
