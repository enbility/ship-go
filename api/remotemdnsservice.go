package api

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
}
