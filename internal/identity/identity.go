package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	fileName          = "identity.pem"
	directoryMode     = 0o700
	fileMode          = 0o600
	othersCanReadMask = 0o077
	commonName        = "lanhop"
	validity          = 100 * 365 * 24 * time.Hour
	clockSkew         = time.Hour
)

type Identity struct {
	Certificate tls.Certificate
	Fingerprint Fingerprint
}

func LoadOrCreate(directory string) (*Identity, error) {
	path := filepath.Join(directory, fileName)
	identity, err := load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return identity, err
	}
	if err := create(directory, path); err != nil {
		return nil, err
	}
	return load(path)
}

func load(path string) (*Identity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&othersCanReadMask != 0 {
		return nil, fmt.Errorf("identity: %s is readable by other users (mode %04o), run: chmod 600 %s", path, info.Mode().Perm(), path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	certificate, err := tls.X509KeyPair(contents, contents)
	if err != nil {
		return nil, fmt.Errorf("identity: %s is damaged: %w", path, err)
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("identity: %s is damaged: %w", path, err)
	}
	certificate.Leaf = leaf
	return &Identity{Certificate: certificate, Fingerprint: FingerprintOf(leaf)}, nil
}

func create(directory, path string) error {
	contents, err := generate(time.Now())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, directoryMode); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return err
	}
	if _, err := file.Write(contents); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func generate(now time.Time) ([]byte, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 127)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		return nil, err
	}
	encodedKey, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	contents := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey})
	return append(contents, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})...), nil
}
