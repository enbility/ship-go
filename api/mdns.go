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
type MdnsSearchInterface interface {
	ReportMdnsEntries(entries map[string]*MdnsEntry)
}

// implemented by mdns, used by Hub
type MdnsInterface interface {
	SetupMdnsService() error
	ShutdownMdnsService()
	AnnounceMdnsEntry() error
	UnannounceMdnsEntry()
	RegisterMdnsSearch(cb MdnsSearchInterface)
	UnregisterMdnsSearch(cb MdnsSearchInterface)
	SetAutoAccept(bool)
}

// implemented by mdns providers, used by mdns
type MdnsProviderInterface interface {
	CheckAvailability() bool
	Shutdown()
	Announce(serviceName string, port int, txt []string) error
	Unannounce()
	ResolveEntries(cancelChan chan bool, callback func(elements map[string]string, name, host string, addresses []net.IP, port int, remove bool))
}
