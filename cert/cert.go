package cert

//nolint:gosec
import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"time"

	"github.com/enbility/ship-go/api"
) // #nosec G505

// SHIP 9.1: the ciphers are reported insecure but are defined to be used by SHIP
var CipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256, // SHIP 9.1: required cipher suite
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, // SHIP 9.1: optional cipher suite
}

// Create a ship compatible self signed certificate
// organizationalUnit is the OU of the certificate
// organization is the O of the certificate
// country is the C of the certificate
// commonName is the CN of the certificate
// Example for commonName: "deviceModel-deviceSerialNumber"
func CreateCertificate(organizationalUnit, organization, country, commonName string) (tls.Certificate, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Create the EEBUS service SKI using the public key
	ski, err := skiFromECDSAKey(&privateKey.PublicKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	subject := pkix.Name{
		OrganizationalUnit: []string{organizationalUnit},
		Organization:       []string{organization},
		Country:            []string{country},
		CommonName:         commonName,
	}

	// Create a random serial big int value
	maxValue := new(big.Int)
	maxValue.Exp(big.NewInt(2), big.NewInt(130), nil).Sub(maxValue, big.NewInt(1))
	serialNumber, err := rand.Int(rand.Reader, maxValue)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		SerialNumber:          serialNumber,
		Subject:               subject,
		NotBefore:             time.Now(),                                // Valid starting now
		NotAfter:              time.Now().Add(time.Hour * 24 * 365 * 10), // Valid for 10 years
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          []byte(ski),
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	tlsCertificate := tls.Certificate{
		Certificate:                  [][]byte{certBytes},
		PrivateKey:                   privateKey,
		SupportedSignatureAlgorithms: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
	}

	return tlsCertificate, nil
}

func SkiFromCertificate(cert *x509.Certificate) (string, error) {
	unknownSubject := "unknown"
	subject := cert.Subject.String()
	if subject == "" {
		subject = unknownSubject
	}

	// check if the clients certificate provides a SKI
	subjectKeyId := cert.SubjectKeyId
	if len(subjectKeyId) != 20 {
		return "", fmt.Errorf("%w (subject: %s, SKI length: %d, expected: 20)",
			api.ErrInvalidSKI, subject, len(subjectKeyId))
	}

	// calculate the SKI from the public key of the certificate

	// Convert ECDSA key to ECDH for raw bytes (same as CreateCertificate)
	ecdsaKey, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return "", fmt.Errorf("unsupported key type for SHIP: expected ECDSA")
	}

	ski, err := skiFromECDSAKey(ecdsaKey)
	if err != nil {
		return "", fmt.Errorf("failed to convert ECDSA key: %w", err)
	}

	// now check if the subjectKeyId and the ski match
	if !bytes.Equal(subjectKeyId, []byte(ski)) {
		return "", fmt.Errorf("%w (subject: %s, SKI: %0x, expected: %0x)",
			api.ErrInvalidSKI, subject, ski[:], subjectKeyId)
	}

	return fmt.Sprintf("%0x", subjectKeyId), nil
}

func skiFromECDSAKey(ecdsaKey *ecdsa.PublicKey) (string, error) {
	// Convert ECDSA key to ECDH for raw bytes (same as CreateCertificate)
	ecdhKey, err := ecdsaKey.ECDH()
	if err != nil {
		return "", fmt.Errorf("failed to convert ECDSA key: %w", err)
	}

	// Calculate SHA-1 per RFC 3280 4.2.1.2 method (1)
	// #nosec G401 - SHA1 required by SHIP specification
	ski := sha1.Sum(ecdhKey.Bytes())

	return string(ski[:]), nil
}
