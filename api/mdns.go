package api

import "net"

/* Mdns */

type MdnsEntry struct {
	Name       string               // the mDNS service name
	Ski        string               // mandatory the certificates SKI
	Identifier string               // mandatory, the identifier used for SHIP ID
	Path       string               // mandatory, the websocket path
	Register   bool                 // mandatory, wether auto accept is enabled
	Brand      string               // optional, the brand of the device
	Type       string               // optional, the type of the device
	Model      string               // optional, the model of the device
	Serial     string               // recommended, the serial number of the device
	Categories []DeviceCategoryType // mandatory, the device categories of the device. Can be empty when the device does not conform to SHIP Requirements for Installation Process
	Host       string               // mandatory, the host name
	Port       int                  // mandatory, the port for the websocket service
	Addresses  []net.IP             // mandatory, the IP addresses used by the service
}

// implemented by Hub, used by mdns
type MdnsReportInterface interface {
	ReportMdnsEntries(entries map[string]*MdnsEntry, newEntries bool)
}

// implemented by mdns, used by Hub
type MdnsInterface interface {
	Start(cb MdnsReportInterface) error
	Shutdown()
	AnnounceMdnsEntry() error
	UnannounceMdnsEntry()
	SetAutoAccept(bool)

	// Returns the QR code text for the service
	// as defined in SHIP Requirements for Installation Process V1.0.0
	QRCodeText() string

	RequestMdnsEntries()
}

// implemented by mdns, used by Providers
type MdnsResolveCB func(elements map[string]string, name, host string, addresses []net.IP, port int, remove bool)

// implemented by mdns providers, used by mdns
type MdnsProviderInterface interface {
	Start(autoReconnect bool, cb MdnsResolveCB) bool
	Shutdown()
	Announce(serviceName string, port int, txt []string) error
	Unannounce()
}
