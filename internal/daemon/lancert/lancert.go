// Package lancert owns the self-signed certificate agentred serves on its LAN
// port when the operator configured none. Automatic direct connections pin
// this certificate, so it has to survive restarts: it is generated once,
// persisted in the data directory, and replaced only when the stored pair is
// missing or can no longer be loaded.
package lancert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
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
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

const (
	certFileName = "lan-cert.pem"
	keyFileName  = "lan-key.pem"
	// validity is deliberately far out: the desktop trusts this certificate by
	// pinning its bytes, not by its validity window, and nothing regenerates it
	// on expiry — a short window would only plant a date on which a restart-
	// stable pin silently stops matching for any client that does check dates.
	validity = 100 * 365 * 24 * time.Hour
)

// Paths returns where the certificate and its private key are stored.
func Paths(dataDir string) (certFile, keyFile string) {
	return filepath.Join(dataDir, certFileName), filepath.Join(dataDir, keyFileName)
}

// LoadOrCreate returns the certificate persisted in dataDir. Only when there is
// none, or the stored pair cannot be loaded, does it generate a new one and
// persist it (private key 0600). An error means no certificate could be
// persisted; the caller must not serve an ephemeral one in its place, because a
// desktop that pinned it would stop matching after the next restart.
func LoadOrCreate(ctx context.Context, dataDir string) (tls.Certificate, error) {
	certFile, keyFile := Paths(dataDir)
	stored, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		logger.Ctx(ctx).Warn("lancert.LoadOrCreate: stored lan certificate unreadable, regenerating",
			zap.String("certFile", certFile), zap.String("keyFile", keyFile), zap.Error(err))
	}
	certPEM, keyPEM, err := generate()
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate lan certificate: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return tls.Certificate{}, fmt.Errorf("persist lan certificate: %w", err)
	}
	// Key first: a crash between the two writes leaves a pair that fails to
	// load, which the next start treats as unreadable and replaces.
	if err := writeFileAtomic(keyFile, keyPEM); err != nil {
		return tls.Certificate{}, fmt.Errorf("persist lan certificate key: %w", err)
	}
	if err := writeFileAtomic(certFile, certPEM); err != nil {
		return tls.Certificate{}, fmt.Errorf("persist lan certificate: %w", err)
	}
	generated, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load generated lan certificate: %w", err)
	}
	logger.Ctx(ctx).Info("lancert.LoadOrCreate: generated lan certificate", zap.String("certFile", certFile))
	return generated, nil
}

func generate() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "agentred"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}

// writeFileAtomic replaces path through a 0600 temp file in the same directory,
// so a reader never sees a half-written key and the key is never briefly
// readable by others.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
