package api

import "net"

/* Mdns */

type MdnsEntry struct {
	Name       string
	Ski        string
	Identifier string   // mandatory
	Path       string   // mandatory
	Register   bool     // mandatory
	Brand      string   // optional
	Type       string   // optional
	Model      string   // optional
	Host       string   // mandatory
	Port       int      // mandatory
	Addresses  []net.IP // mandatory
}

// implemented by Hub, used by mdns
type MdnsReportInterface interface {
	ReportMdnsEntries(entries map[string]*MdnsEntry)
}

// implemented by mdns, used by Hub
type MdnsInterface interface {
	Start(cb MdnsReportInterface) error
	Shutdown()
	AnnounceMdnsEntry() error
	UnannounceMdnsEntry()
	SetAutoAccept(bool)
	RequestMdnsEntries()
}

// implemented by mdns providers, used by mdns
type MdnsProviderInterface interface {
	CheckAvailability() bool
	Shutdown()
	Announce(serviceName string, port int, txt []string) error
	Unannounce()
	ResolveEntries(callback func(elements map[string]string, name, host string, addresses []net.IP, port int, remove bool))
}
