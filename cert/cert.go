package cert

//nolint:gosec
import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"time"
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
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Create a random serial big int value
	maxValue := new(big.Int)
	maxValue.Exp(big.NewInt(2), big.NewInt(130), nil).Sub(maxValue, big.NewInt(1))
	serialNumber, err := rand.Int(rand.Reader, maxValue)
	if err != nil {
		return tls.Certificate{}, err
	}

	subject := pkix.Name{
		OrganizationalUnit: []string{organizationalUnit},
		Organization:       []string{organization},
		Country:            []string{country},
		CommonName:         commonName,
	}

	rootTemplate := &x509.Certificate{
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		SerialNumber:          serialNumber,
		Subject:               subject,
		NotBefore:             time.Now(),                                // Valid starting now
		NotAfter:              time.Now().Add(time.Hour * 24 * 365 * 10), // Valid for 10 years
		KeyUsage:              x509.KeyUsageCRLSign | x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	rootCertBytes, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	rootCert, err := x509.ParseCertificate(rootCertBytes)
	if err != nil {
		return tls.Certificate{}, err
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Create the EEBUS service SKI using the private key
	asn1, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	// SHIP 12.2: Required to be created according to RFC 3280 4.2.1.2
	// #nosec G401
	ski := sha1.Sum(asn1)

	template := &x509.Certificate{
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		SerialNumber:          serialNumber,
		Subject:               subject,
		NotBefore:             time.Now(),                                // Valid starting now
		NotAfter:              time.Now().Add(time.Hour * 24 * 365 * 10), // Valid for 10 years
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          ski[:],
		AuthorityKeyId:        ski[:],
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, template, rootCert, &privateKey.PublicKey, rootKey)
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
	// check if the clients certificate provides a SKI
	subjectKeyId := cert.SubjectKeyId
	if len(subjectKeyId) != 20 {
		return "", errors.New("Client certificate does not provide a SKI")
	}

	return fmt.Sprintf("%0x", subjectKeyId), nil
}
