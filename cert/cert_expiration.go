package cert

import (
	"crypto/x509"
	"time"

	"github.com/enbility/ship-go/logging"
)

// CertificateExpirationStatus represents the expiration status of a certificate
type CertificateExpirationStatus int

const (
	// CertificateValid indicates the certificate is valid and not expiring soon
	CertificateValid CertificateExpirationStatus = iota
	// CertificateExpiringSoon indicates the certificate will expire within 30 days
	CertificateExpiringSoon
	// CertificateExpired indicates the certificate has already expired
	CertificateExpired
)

// CertificateExpirationInfo contains detailed information about certificate expiration
type CertificateExpirationInfo struct {
	Status              CertificateExpirationStatus
	IsExpired           bool
	ExpiresWithin30Days bool
	DaysUntilExpiration int
	ExpirationDate      time.Time
}

// CheckCertificateExpiration checks the expiration status of a certificate
func CheckCertificateExpiration(cert *x509.Certificate) CertificateExpirationInfo {
	now := time.Now()
	expirationDate := cert.NotAfter
	duration := expirationDate.Sub(now)

	// Calculate days with proper rounding for both positive and negative durations
	hours := duration.Hours()
	var daysUntilExpiration int
	if hours >= 0 {
		daysUntilExpiration = int((hours + 12) / 24)
	} else {
		daysUntilExpiration = int((hours - 12) / 24)
	}

	info := CertificateExpirationInfo{
		DaysUntilExpiration: daysUntilExpiration,
		ExpirationDate:      expirationDate,
	}

	if duration <= 0 {
		// Certificate has expired
		info.Status = CertificateExpired
		info.IsExpired = true
		info.ExpiresWithin30Days = false
	} else if duration <= 30*24*time.Hour {
		// Certificate expires within 30 days
		info.Status = CertificateExpiringSoon
		info.IsExpired = false
		info.ExpiresWithin30Days = true
	} else {
		// Certificate is valid
		info.Status = CertificateValid
		info.IsExpired = false
		info.ExpiresWithin30Days = false
	}

	return info
}

// LogCertificateExpiration logs certificate expiration warnings according to SHIP spec 12.1.1
// This logs warnings but does not affect the connection (per spec: "SHOULD still allow communication")
func LogCertificateExpiration(cert *x509.Certificate, ski string) {
	info := CheckCertificateExpiration(cert)

	commonName := cert.Subject.CommonName
	if commonName == "" {
		commonName = "unknown"
	}

	switch info.Status {
	case CertificateExpired:
		// Log as error level for expired certificates
		if info.DaysUntilExpiration == 0 {
			logging.Log().Errorf("Certificate for SKI %s (CN: %s) expired today", ski, commonName)
		} else {
			logging.Log().Errorf("Certificate for SKI %s (CN: %s) expired %d days ago", ski, commonName, -info.DaysUntilExpiration)
		}
	case CertificateExpiringSoon:
		// Log as info level for certificates expiring soon
		logging.Log().Infof("Certificate for SKI %s (CN: %s) expires in %d days", ski, commonName, info.DaysUntilExpiration)
	case CertificateValid:
		// No logging needed for valid certificates
	}
}
